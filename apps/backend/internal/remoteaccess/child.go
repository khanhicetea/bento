package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tailscale/tailcat"
	"golang.org/x/sys/unix"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
	"tailscale.com/wgengine/filter"
)

// ChildCommand is the hidden subcommand that runs one service's Tailcat
// server: `/proc/self/exe internal-tailcat`.
const ChildCommand = "internal-tailcat"

// childSocketFD is the inherited descriptor of run/tailcat (opened by root),
// so the child needs no traversal rights on the stack root's ancestors.
const childSocketFD = 3

// childMaxConns bounds tunnel connections the child holds at once; the
// backend enforces the real per-service caps behind it.
const childMaxConns = 64

// childStartTimeout bounds DERP map fetch, region pick, and server start.
const childStartTimeout = 60 * time.Second

// childConfig is what the backend writes on the child's stdin. It is the only
// place the key travels: never argv, the environment, or a log.
type childConfig struct {
	Private      key.NodePrivate      `json:"private"`
	PresharedKey tailcat.PresharedKey `json:"presharedKey"`
	RegionID     int                  `json:"regionId"`
	// Port is the only tunnel port served (the engine port: 5432 or 3306).
	Port uint16 `json:"port"`
	// Socket is the backend socket's name inside the inherited directory.
	Socket string `json:"socket"`
	// DERPMapURL overrides tailcat's default DERP map (tests only).
	DERPMapURL string `json:"derpMapUrl,omitempty"`
}

// childReport is the one JSON line the child prints on stdout once its
// server is listening. The backend reads it and never logs it.
type childReport struct {
	Address  string `json:"address"`
	RegionID int    `json:"regionId"`
	// NetSandbox says whether Landlock limits the child's outbound TCP to the
	// relay ports (false on kernels before 6.7).
	NetSandbox bool `json:"netSandbox"`
}

// relayTCPPorts are the only TCP ports the child may connect to: HTTPS and
// HTTP for the DERP map and relays, each relay node's own DERP port, and the
// port of an overridden DERP map URL. Nothing else, so the child cannot reach
// the data network even though it shares the host's network namespace.
func relayTCPPorts(cfg childConfig, region *tailcfg.DERPRegion) []uint16 {
	ports := []uint16{443, 80}
	if region != nil {
		for _, n := range region.Nodes {
			if n != nil && n.DERPPort > 0 && n.DERPPort < 1<<16 {
				ports = append(ports, uint16(n.DERPPort))
			}
		}
	}
	if cfg.DERPMapURL != "" {
		if u, err := url.Parse(cfg.DERPMapURL); err == nil {
			if p, err := strconv.ParseUint(u.Port(), 10, 16); err == nil && p > 0 {
				ports = append(ports, uint16(p))
			}
		}
	}
	return ports
}

func tailcfgRegion(id int) tailcfg.DERPRegionID { return tailcfg.DERPRegionID(id) }

// RunChild is the child's main: it refuses to run with privileges, reads its
// key on stdin, starts the Tailcat server and forwards each tunnel connection
// to the engine port into the backend's socket. It holds no database
// credential, and Landlock (Linux 6.7+) keeps its outbound TCP to the relay
// ports so it cannot open connections to the data network.
func RunChild() error {
	// Libraries that log without a Logf must not reach stderr (the backend
	// logs that); only RunChild's own error is printed.
	log.SetOutput(io.Discard)
	if err := dropPrivilegeLeftovers(); err != nil {
		return err
	}
	cfg, err := readChildConfig(os.Stdin)
	if err != nil {
		return err
	}
	if err := syscall.Fchdir(childSocketFD); err != nil {
		return fmt.Errorf("enter socket directory: %w", err)
	}
	_ = syscall.Close(childSocketFD)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	sctx, cancel := context.WithTimeout(ctx, childStartTimeout)
	defer cancel()

	region, err := pickRegion(sctx, cfg)
	if err != nil {
		return err
	}
	sandboxed, err := restrictTCPConnect(relayTCPPorts(cfg, region))
	if err != nil {
		return fmt.Errorf("restrict outbound TCP: %w", err)
	}
	sem := make(chan struct{}, childMaxConns)
	forward := func(c net.Conn) {
		defer c.Close()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			return
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		up, err := d.Dial("unix", cfg.Socket) // relative to the inherited directory
		if err != nil {
			return
		}
		tailcat.ProxyConns(c, up)
	}
	srv := &tailcat.Server{
		Key:          cfg.Private,
		PresharedKey: cfg.PresharedKey,
		Region:       region,
		// Tailscale's networking logs can name peers and keys; the backend
		// logs only the child's own error lines.
		Logf:           logger.Discard,
		ServedTCPPorts: []filter.PortRange{{First: cfg.Port, Last: cfg.Port}},
		OnTCP: func(port uint16) func(net.Conn) {
			if port != cfg.Port {
				return nil // RST
			}
			return forward
		},
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start tailcat server: %w", err)
	}
	defer srv.Close()
	k := Key{Version: keyFileVersion, Private: cfg.Private, PresharedKey: cfg.PresharedKey, RegionID: int(region.RegionID)}
	addr, err := k.Address()
	if err != nil {
		return err
	}
	line, _ := json.Marshal(childReport{Address: string(addr), RegionID: int(region.RegionID), NetSandbox: sandboxed})
	if _, err := fmt.Fprintf(os.Stdout, "%s\n", line); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

// dropPrivilegeLeftovers refuses root or any capability, then sets
// no_new_privs and clears the dumpable flag before the key is read, so no
// other process of the same UID can ptrace the child or read its memory.
func dropPrivilegeLeftovers() error {
	if os.Getuid() == 0 || os.Geteuid() == 0 || os.Getgid() == 0 || os.Getegid() == 0 {
		return errors.New(ChildCommand + " must be started by the backend as the Tailcat identity, never as root")
	}
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return fmt.Errorf("read capabilities: %w", err)
	}
	for _, d := range data {
		if d.Effective != 0 || d.Permitted != 0 || d.Inheritable != 0 {
			return errors.New(ChildCommand + " refuses to run with capabilities")
		}
	}
	if err := setNoNewPrivsAllThreads(); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("clear dumpable: %w", err)
	}
	return nil
}

func readChildConfig(r io.Reader) (childConfig, error) {
	var cfg childConfig
	b, err := io.ReadAll(io.LimitReader(r, 16<<10))
	if err != nil {
		return cfg, fmt.Errorf("read configuration: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, errors.New("configuration on stdin does not parse")
	}
	switch {
	case cfg.Private.IsZero() || cfg.PresharedKey.IsZero():
		return cfg, errors.New("configuration has no key")
	case cfg.Port == 0:
		return cfg, errors.New("configuration has no port")
	case cfg.Socket == "" || strings.ContainsAny(cfg.Socket, "/\x00") || cfg.Socket == "." || cfg.Socket == "..":
		return cfg, errors.New("configuration has an invalid socket name")
	case cfg.RegionID < 0:
		return cfg, errors.New("configuration has an invalid region")
	}
	return cfg, nil
}

// pickRegion resolves the recorded DERP region, or the nearest one for a new
// key (region 0), from the DERP map.
func pickRegion(ctx context.Context, cfg childConfig) (*tailcfg.DERPRegion, error) {
	ci := tailcat.ConnInfo{RegionID: tailcfgRegion(cfg.RegionID)}
	if cfg.RegionID == 0 {
		ci.RegionID = -1 // nearest by latency
	}
	opts := []any{tailcat.ExpandForServer}
	if cfg.DERPMapURL != "" {
		opts = append(opts, tailcat.DERPMapURL(cfg.DERPMapURL))
	}
	if err := ci.Expand(ctx, opts...); err != nil {
		return nil, fmt.Errorf("resolve the DERP relay region: %w", err)
	}
	if len(ci.Region) == 0 || ci.Region[0] == nil || ci.Region[0].RegionID <= 0 {
		return nil, errors.New("no DERP relay region available")
	}
	return ci.Region[0], nil
}
