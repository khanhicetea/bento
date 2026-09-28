// Package cli implements the bento command: the resident backend (serve),
// offline stack commands (init, import), and thin client commands that talk
// to the running backend over its peer-checked control socket.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/reconcile"
	"github.com/khanhicetea/bento/apps/backend/internal/scheduler"
	"github.com/khanhicetea/bento/apps/backend/internal/stack"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/webui"
)

type ServeOptions struct {
	Root    string
	Listen  string
	Origins []string
	// PublicListen are the public listener addresses (see ParsePublicListen).
	PublicListen []string
	Version      string
}

// ValidateListen requires a loopback address. Non-loopback exposure is
// rejected in this release: loopback is not authentication, and remote
// access needs a separately reviewed TLS/proxy design.
func ValidateListen(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen address %q", addr)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid listen port %q", portStr)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", 0, fmt.Errorf("refusing non-loopback listen address %q: the management API is loopback-only in this release", addr)
	}
	return host, port, nil
}

// DefaultOrigins derives the exact allowed browser origins for a listener.
func DefaultOrigins(host string, port int) []string {
	out := []string{fmt.Sprintf("http://%s:%d", host, port)}
	if host == "127.0.0.1" {
		out = append(out, fmt.Sprintf("http://localhost:%d", port))
	}
	return out
}

func validateOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return fmt.Errorf("invalid origin %q (expected scheme://host[:port])", o)
	}
	return nil
}

func Serve(opts ServeOptions) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	layout, err := platform.NewLayout(opts.Root)
	if err != nil {
		return err
	}
	host, port, err := ValidateListen(opts.Listen)
	if err != nil {
		return err
	}
	origins := DefaultOrigins(host, port)
	for _, o := range opts.Origins {
		if err := validateOrigin(o); err != nil {
			return err
		}
		origins = append(origins, strings.TrimSuffix(o, "/"))
	}
	publicListen := opts.PublicListen
	if len(publicListen) == 0 {
		publicListen = DefaultPublicListen
	}
	publicAddrs, err := ParsePublicListen(publicListen)
	if err != nil {
		return err
	}
	if _, err := os.Stat(layout.Database()); err != nil {
		if m := stack.DetectForeign(layout.Root); m != "" {
			return fmt.Errorf("%s is not a Bento stack root (found %s); it was left untouched", layout.Root, m)
		}
		// Lost state fails closed; it never triggers empty-stack initialization.
		return fmt.Errorf("no Bento state at %s (run `bento init` for a new stack; restore from backup if state was lost)", layout.Database())
	}
	lock, err := platform.TryLock(layout.ControllerLock())
	if err != nil {
		return fmt.Errorf("another backend or offline command holds this stack: %w", err)
	}
	defer lock.Release()
	st, err := store.Open(layout.Database())
	if err != nil {
		return err
	}
	defer st.Close()
	engine, err := docker.NewSDK()
	if err != nil {
		return err
	}
	defer engine.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if v, err := engine.Version(ctx); err != nil {
		log.Warn("docker engine unavailable; management continues, effects will fail until it returns", "err", err)
	} else {
		log.Info("docker engine", "version", v.ServerVersion, "api", v.APIVersion, "arch", v.Arch)
	}
	ctrl, err := operations.NewController(operations.Deps{Store: st, Engine: engine, Layout: layout, HostIDs: platform.FileHostIDs{}, Log: log,
		PublicAppsPort: appsPort(publicAddrs)})
	if err != nil {
		return err
	}
	if err := ctrl.Recover(ctx); err != nil {
		return err
	}
	ctrl.Start(ctx)
	rec := reconcile.New(ctrl, log)
	go rec.Run(ctx)
	go ctrl.RunSchedule(ctx)
	// The relay child runs as an app UID; /proc/self/exe is a magic link, so
	// the binary need not live in a directory that UID can traverse.
	relay := scheduler.NewRelayManager(layout, "/proc/self/exe", log)
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				relay.Reap(false)
			}
		}
	}()
	public := &publicListeners{log: log, gateway: ctrl.AppsGateway, appsPort: appsPort(publicAddrs), servers: map[string]*http.Server{}}
	srv := &api.Server{C: ctrl, R: rec, Store: st, Layout: layout, Log: log, Version: opts.Version, StartedAt: time.Now(),
		AllowedOrigins: origins, WebUI: webui.FS(), Relay: relay, PublicAddrs: public.Addrs}
	handler := srv.Handler()
	public.handler = srv.PublicHandler()

	tcp, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return err
	}
	_ = os.Remove(layout.ControlSocket())
	unixLn, err := net.Listen("unix", layout.ControlSocket())
	if err != nil {
		return err
	}
	if err := os.Chmod(layout.ControlSocket(), 0o600); err != nil {
		return err
	}
	web := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	local := &http.Server{Handler: srv.LocalOnly(handler), ReadHeaderTimeout: 10 * time.Second, ConnContext: api.ConnContext}
	errc := make(chan error, 4)
	go func() { errc <- web.Serve(tcp) }()
	go func() { errc <- local.Serve(unixLn) }()
	// The public listener serves only self-authenticating routes (webhooks),
	// never the UI or management API, so it may be proxied from the internet.
	if err := public.startFixed(publicAddrs, errc); err != nil {
		return err
	}
	go public.followApps(ctx)
	log.Info("bento backend ready", "stack", ctrl.Stack.Name, "root", layout.Root, "listen", tcp.Addr().String(), "control", layout.ControlSocket(), "public", publicListen, "ui", webui.Built())

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	select {
	case s := <-sigs:
		log.Info("shutdown requested", "signal", s.String())
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listener failed", "err", err)
		}
	}
	// Stop accepting, checkpoint/drain within a bound; the data plane keeps running.
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	_ = web.Shutdown(sctx)
	_ = local.Shutdown(sctx)
	public.shutdown(sctx)
	ctrl.Shutdown(60 * time.Second)
	cancel()
	relay.Reap(true)
	_ = os.Remove(layout.ControlSocket())
	log.Info("backend stopped; app containers, ingress, and schedulers keep running")
	return nil
}
