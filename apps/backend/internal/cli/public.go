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

// PublicAppsHost is the --public-listen host that means "the host's address
// on the stack's apps network", where the edge and cloudflared can reach it.
const PublicAppsHost = "apps"

// DefaultPublicListen serves public routes to host proxies (loopback) and to
// the edge and tunnel containers (apps network gateway).
var DefaultPublicListen = []string{"127.0.0.1:7781", PublicAppsHost + ":7781"}

type publicAddr struct {
	host string // an IP, or PublicAppsHost
	port int
}

func (a publicAddr) String() string { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }

// ParsePublicListen validates --public-listen values. Unlike the management
// listener, any address is allowed: the public listener serves only routes
// that authenticate themselves (webhooks). "off" disables it.
func ParsePublicListen(values []string) ([]publicAddr, error) {
	if len(values) == 1 && values[0] == "off" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []publicAddr
	for _, v := range values {
		host, portStr, err := net.SplitHostPort(v)
		if err != nil {
			return nil, fmt.Errorf("invalid public listen address %q (expected HOST:PORT, apps:PORT, or off)", v)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid public listen port %q", portStr)
		}
		if host == "localhost" {
			host = "127.0.0.1"
		}
		if host != PublicAppsHost {
			ip := net.ParseIP(host)
			if ip == nil {
				return nil, fmt.Errorf("public listen host %q must be an IP address or %q", host, PublicAppsHost)
			}
			host = ip.String()
		}
		a := publicAddr{host: host, port: port}
		if seen[a.String()] {
			return nil, fmt.Errorf("duplicate public listen address %q", v)
		}
		seen[a.String()] = true
		out = append(out, a)
	}
	return out, nil
}

// appsPort returns the port requested on the apps network gateway, or 0.
func appsPort(addrs []publicAddr) int {
	for _, a := range addrs {
		if a.host == PublicAppsHost {
			return a.port
		}
	}
	return 0
}

// publicListeners runs the public HTTP listeners. Fixed addresses bind at
// start (failure is fatal); the apps-network address follows the network plan
// and binds once the bridge exists, because the network is created lazily.
type publicListeners struct {
	handler  http.Handler
	log      *slog.Logger
	gateway  func(context.Context) (string, error)
	appsPort int

	mu        sync.Mutex
	servers   map[string]*http.Server
	appsBound string
	appsWarn  string
}

func newPublicServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
}

func (p *publicListeners) serve(addr string, errc chan<- error) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := newPublicServer(p.handler)
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

func (p *publicListeners) startFixed(addrs []publicAddr, errc chan<- error) error {
	for _, a := range addrs {
		if a.host == PublicAppsHost {
			continue
		}
		if err := p.serve(a.String(), errc); err != nil {
			return fmt.Errorf("public listener: %w", err)
		}
	}
	return nil
}

// followApps keeps the apps-network listener bound to the current gateway.
func (p *publicListeners) followApps(ctx context.Context) {
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

func (p *publicListeners) syncApps(ctx context.Context) {
	gw, err := p.gateway(ctx)
	if err != nil {
		p.log.Warn("public listener: read network plan", "err", err)
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
		p.log.Info("public listener closed", "addr", bound)
	}
	p.mu.Lock()
	p.appsBound = ""
	p.mu.Unlock()
	if want == "" {
		return
	}
	if err := p.serve(want, nil); err != nil {
		if p.appsWarn != want {
			p.log.Warn("public listener: cannot bind the apps network gateway yet", "addr", want, "err", err)
			p.appsWarn = want
		}
		return
	}
	p.mu.Lock()
	p.appsBound = want
	p.mu.Unlock()
	p.log.Info("public listener ready", "addr", want)
}

func (p *publicListeners) close(addr string) {
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
func (p *publicListeners) Addrs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []string{}
	for a := range p.servers {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func (p *publicListeners) shutdown(ctx context.Context) {
	p.mu.Lock()
	servers := p.servers
	p.servers = map[string]*http.Server{}
	p.mu.Unlock()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}
