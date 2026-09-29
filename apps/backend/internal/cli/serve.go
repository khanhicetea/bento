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
	"sync"
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
	// UtilsListen are the utils listener addresses (see ParseUtilsListen).
	UtilsListen []string
	// OpConcurrency bounds operations executed at once (0 = default).
	OpConcurrency int
	Version       string
}

// MaxOpConcurrency caps --op-concurrency: parallel operations share one
// SQLite connection and one Docker daemon, so more mostly adds contention.
const MaxOpConcurrency = 16

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
		return "", 0, fmt.Errorf(
			"refusing non-loopback listen address %q: the management API is loopback-only in this release",
			addr,
		)
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

// validateOrigin accepts only a bare http(s) origin: scheme and host, an
// optional trailing slash, no path or query.
func validateOrigin(o string) error {
	if u, err := url.Parse(o); err == nil {
		validScheme := u.Scheme == "http" || u.Scheme == "https"
		bareHost := u.Host != "" && (u.Path == "" || u.Path == "/") && u.RawQuery == ""
		if validScheme && bareHost {
			return nil
		}
	}
	return fmt.Errorf("invalid origin %q (expected scheme://host[:port])", o)
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
	utilsListen := opts.UtilsListen
	if len(utilsListen) == 0 {
		utilsListen = DefaultUtilsListen
	}
	utilsAddrs, err := ParseUtilsListen(utilsListen)
	if err != nil {
		return err
	}
	if opts.OpConcurrency == 0 {
		opts.OpConcurrency = operations.DefaultConcurrency
	}
	if opts.OpConcurrency < 1 || opts.OpConcurrency > MaxOpConcurrency {
		return fmt.Errorf("--op-concurrency must be between 1 and %d", MaxOpConcurrency)
	}
	if _, err := os.Stat(layout.Database()); err != nil {
		if m := stack.DetectForeign(layout.Root); m != "" {
			return fmt.Errorf("%s is not a Bento stack root (found %s); it was left untouched", layout.Root, m)
		}
		// Lost state fails closed; it never triggers empty-stack initialization.
		return fmt.Errorf(
			"no Bento state at %s (run `bento init` for a new stack; restore from backup if state was lost)",
			layout.Database(),
		)
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
	ctrl, err := operations.NewController(operations.Deps{
		Store:         st,
		Engine:        engine,
		Layout:        layout,
		HostIDs:       platform.FileHostIDs{},
		Log:           log,
		UtilsAppsPort: appsPort(utilsAddrs),
		Concurrency:   opts.OpConcurrency,
	})
	if err != nil {
		return err
	}
	if err := ctrl.Recover(ctx); err != nil {
		return err
	}
	ctrl.Start(ctx)
	rec := reconcile.New(ctrl, log)
	// Background loops end with ctx and are joined before the store closes.
	var bg sync.WaitGroup
	defer func() { cancel(); bg.Wait() }() // also on early error returns
	bg.Go(func() { rec.Run(ctx) })
	bg.Go(func() { ctrl.RunSchedule(ctx) })
	// The relay child runs as an app UID; /proc/self/exe is a magic link, so
	// the binary need not live in a directory that UID can traverse.
	relay := scheduler.NewRelayManager(layout, "/proc/self/exe", log)
	bg.Go(func() {
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
	})
	utils := &utilsListeners{
		log:      log,
		gateway:  ctrl.AppsGateway,
		appsPort: appsPort(utilsAddrs),
		servers:  map[string]*http.Server{},
	}
	srv := &api.Server{C: ctrl, R: rec, Store: st, Layout: layout, Log: log, Version: opts.Version, StartedAt: time.Now(),
		AllowedOrigins: origins, WebUI: webui.FS(), Relay: relay, UtilsAddrs: utils.Addrs}
	handler := srv.Handler()
	utils.handler = srv.UtilsHandler()

	tcp, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return err
	}
	_ = os.Remove(layout.ControlSocket()) // stale socket of a previous run; Listen reports real problems
	unixLn, err := net.Listen("unix", layout.ControlSocket())
	if err != nil {
		return err
	}
	if err := os.Chmod(layout.ControlSocket(), 0o600); err != nil {
		return err
	}
	web := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	local := &http.Server{
		Handler:           srv.LocalOnly(handler),
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext:       api.ConnContext,
	}
	// One slot per listener, so no Serve goroutine blocks on a report nobody reads.
	errc := make(chan error, 2+len(utilsAddrs))
	go func() { errc <- web.Serve(tcp) }()
	go func() { errc <- local.Serve(unixLn) }()
	// The utils listener serves only self-authenticating routes (webhooks and
	// the ticketed database browser), never the UI or management API, so it
	// may be proxied from the internet.
	if err := utils.startFixed(utilsAddrs, errc); err != nil {
		return err
	}
	// followApps is stopped before the utils listeners shut down, so it cannot
	// bind a new apps-network listener during the drain.
	followCtx, stopFollow := context.WithCancel(ctx)
	defer stopFollow()
	followDone := make(chan struct{})
	go func() {
		defer close(followDone)
		utils.followApps(followCtx)
	}()
	log.Info(
		"bento backend ready",
		"stack",
		ctrl.Stack.Name,
		"root",
		layout.Root,
		"listen",
		tcp.Addr().String(),
		"control",
		layout.ControlSocket(),
		"utils",
		utilsListen,
		"opConcurrency",
		opts.OpConcurrency,
		"ui",
		webui.Built(),
	)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	var listenErr error
	select {
	case s := <-sigs:
		log.Info("shutdown requested", "signal", s.String())
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Returned after the graceful shutdown below so the process exits
			// non-zero and the service manager can restart it.
			listenErr = fmt.Errorf("listener failed: %w", err)
		}
	}
	// Stop accepting, checkpoint/drain within a bound; the data plane keeps running.
	// A shutdown that hits the deadline only drops lingering connections.
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	_ = web.Shutdown(sctx)
	_ = local.Shutdown(sctx)
	stopFollow()
	<-followDone
	utils.shutdown(sctx)
	ctrl.Shutdown(60 * time.Second)
	cancel()
	bg.Wait()
	relay.Reap(true)
	_ = os.Remove(layout.ControlSocket())
	log.Info("backend stopped; app containers, ingress, and schedulers keep running")
	return listenErr
}
