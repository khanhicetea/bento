package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// UtilsAppsHost is the --utils-listen host that means "the host's address
// on the stack's apps network", where the edge and cloudflared can reach it.
const UtilsAppsHost = "apps"

// DefaultUtilsListen serves utils routes to host proxies (loopback) and to
// the edge and tunnel containers (apps network gateway).
var DefaultUtilsListen = []string{"127.0.0.1:7781", UtilsAppsHost + ":7781"}

type utilsAddr struct {
	host string // an IP, or UtilsAppsHost
	port int
}

func (a utilsAddr) String() string { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }

// ParseUtilsListen validates --utils-listen values. Unlike the management
// listener, any address is allowed: the utils listener serves only routes
// that authenticate themselves (webhooks, ticketed DB browser). "off" disables it.
func ParseUtilsListen(values []string) ([]utilsAddr, error) {
	if len(values) == 1 && values[0] == "off" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []utilsAddr
	for _, v := range values {
		host, portStr, err := net.SplitHostPort(v)
		if err != nil {
			return nil, fmt.Errorf("invalid utils listen address %q (expected HOST:PORT, apps:PORT, or off)", v)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid utils listen port %q", portStr)
		}
		if host == "localhost" {
			host = "127.0.0.1"
		}
		if host != UtilsAppsHost {
			ip := net.ParseIP(host)
			if ip == nil {
				return nil, fmt.Errorf("utils listen host %q must be an IP address or %q", host, UtilsAppsHost)
			}
			host = ip.String()
		}
		a := utilsAddr{host: host, port: port}
		if seen[a.String()] {
			return nil, fmt.Errorf("duplicate utils listen address %q", v)
		}
		seen[a.String()] = true
		out = append(out, a)
	}
	return out, nil
}

// appsPort returns the port requested on the apps network gateway, or 0.
func appsPort(addrs []utilsAddr) int {
	for _, a := range addrs {
		if a.host == UtilsAppsHost {
			return a.port
		}
	}
	return 0
}

// utilsListeners runs the utils HTTP listeners. Fixed addresses bind at
// start (failure is fatal); the apps-network address follows the network plan
// and binds once the bridge exists, because the network is created lazily.
type utilsListeners struct {
	handler  http.Handler
	log      *slog.Logger
	gateway  func(context.Context) (string, error)
	appsPort int

	mu        sync.Mutex
	servers   map[string]*http.Server
	appsBound string
	appsWarn  string
}

func newUtilsServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
}

func (p *utilsListeners) serve(addr string, errc chan<- error) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := newUtilsServer(p.handler)
	p.mu.Lock()
	p.servers[addr] = srv
	p.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && errc != nil {
			errc <- err
		}
	}()
	return nil
}

func (p *utilsListeners) startFixed(addrs []utilsAddr, errc chan<- error) error {
	for _, a := range addrs {
		if a.host == UtilsAppsHost {
			continue
		}
		if err := p.serve(a.String(), errc); err != nil {
			return fmt.Errorf("utils listener: %w", err)
		}
	}
	return nil
}

// followApps keeps the apps-network listener bound to the current gateway.
func (p *utilsListeners) followApps(ctx context.Context) {
	if p.appsPort == 0 {
		return
	}
	// Stack networks are created lazily by the first operation that needs
	// them, so poll briefly; a sync is a settings read and an interface scan.
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		p.syncApps(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *utilsListeners) syncApps(ctx context.Context) {
	gw, err := p.gateway(ctx)
	if err != nil {
		p.log.Warn("utils listener: read network plan", "err", err)
		return
	}
	want := ""
	if gw != "" {
		want = net.JoinHostPort(gw, strconv.Itoa(p.appsPort))
	}
	p.mu.Lock()
	bound := p.appsBound
	p.mu.Unlock()
	if want == bound {
		return
	}
	if bound != "" {
		p.close(bound)
		p.log.Info("utils listener closed", "addr", bound)
	}
	p.mu.Lock()
	p.appsBound = ""
	p.mu.Unlock()
	if want == "" {
		return
	}
	if err := p.serve(want, nil); err != nil {
		if p.appsWarn != want {
			p.log.Warn("utils listener: cannot bind the apps network gateway yet", "addr", want, "err", err)
			p.appsWarn = want
		}
		return
	}
	p.mu.Lock()
	p.appsBound = want
	p.mu.Unlock()
	p.log.Info("utils listener ready", "addr", want)
}

func (p *utilsListeners) close(addr string) {
	p.mu.Lock()
	srv := p.servers[addr]
	delete(p.servers, addr)
	p.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// Addrs lists the addresses currently served.
func (p *utilsListeners) Addrs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []string{}
	for a := range p.servers {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func (p *utilsListeners) shutdown(ctx context.Context) {
	p.mu.Lock()
	servers := p.servers
	p.servers = map[string]*http.Server{}
	p.mu.Unlock()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}
