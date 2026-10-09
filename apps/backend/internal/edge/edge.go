// Package edge renders the optional shared edge Nginx configuration and
// manages its generations (candidate -> validate -> atomic swap).
package edge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// Container paths.
const (
	ConfMount   = "/etc/bento-edge"
	CertsMount  = "/etc/bento-edge-certs"
	CustomMount = "/etc/bento-edge-custom"
	ACMEMount   = "/var/lib/bento-acme"
	// HooksMount holds the backend's webhook socket. It is the only backend
	// endpoint the edge can reach and serves nothing but webhook deliveries.
	HooksMount = "/var/lib/bento-hooks"
	LiveConf   = ConfMount + "/live/nginx.conf"
	// StubStatusPort is the edge's loopback-only stub_status listener. It sits
	// in an uncommon high range so operator drop-ins are unlikely to collide.
	StubStatusPort = 57319
)

// Route is one managed edge route.
type Route struct {
	Kind        string
	Name        string
	ServerNames string
	TLS         bool
	ACME        bool
	CertFile    string
	KeyFile     string
	Redirect    bool
	AccessLog   bool
	StaticCache bool
	// AppUpstream is the app container alias host:port, re-resolved at runtime.
	AppUpstream         string
	ProxyUpstreams      []string
	UpstreamName        string
	UpstreamScheme      string
	UpstreamURI         string
	MaxBodyMB           int
	Unavailable         bool
	HTTP3               bool
	HTTPSPortSuffix     string
	HTTPSAdvertisedPort int
	// UtilsUpstream is the backend's utils listener (host:port) that
	// /_bento/webhook/* is proxied to; "" leaves the path to the upstream.
	UtilsUpstream string
	// RedirectURL is set for redirect hosts: the scheme://host[:port] base
	// that every request is permanently redirected to.
	RedirectURL string
	// Includes are the operator drop-in route directories included in the
	// server block: the host's own, plus app-<slug> for app targets.
	Includes []string
}

// AppUpstream is one app's shared upstream block. It is rendered once per app
// however many hosts target it.
type AppUpstream struct {
	Name   string
	Server string
}

// Input is everything needed to render one edge generation.
type Input struct {
	Settings domain.EdgeSettings
	Apps     []domain.App
	Hosts    []domain.Host
	// Running reports which published apps currently have a running instance.
	Running map[string]bool
	// UtilsUpstream is the backend's utils listener on the apps network.
	UtilsUpstream string
}

// RouteName is the edge route (site file and drop-in directory) of a host.
func RouteName(host string) string { return "host-" + host }

// Render produces the full candidate file set, keyed by relative path: one
// site per enabled host whose target is routable, and one upstream per app
// that at least one host routes to.
func Render(in Input) (map[string][]byte, error) {
	s := in.Settings
	var suffix, httpSuffix string
	if s.HTTPSPort != 443 {
		suffix = ":" + strconv.Itoa(s.HTTPSPort)
	}
	if s.HTTPPort != 80 && s.HTTPPort != 0 {
		httpSuffix = ":" + strconv.Itoa(s.HTTPPort)
	}
	apps := make(map[string]domain.App, len(in.Apps))
	for _, a := range in.Apps {
		apps[a.ID] = a
	}
	// routable reports whether a host is served at all.
	routable := func(h domain.Host) bool {
		if !h.Enabled {
			return false
		}
		switch h.Target {
		case domain.HostTargetApp:
			a, ok := apps[h.AppID]
			return ok && a.Ingress == domain.IngressManaged && a.Publication == domain.Published
		case domain.HostTargetUpstream:
			return len(h.Upstreams) > 0
		case domain.HostTargetRedirect:
			return h.RedirectTo != ""
		}
		return false
	}
	served := map[string]domain.Host{}
	for _, h := range in.Hosts {
		if routable(h) {
			served[h.Name] = h
		}
	}
	var routes []Route
	upstreams := map[string]AppUpstream{}
	var anyACME bool
	for _, h := range in.Hosts {
		if _, ok := served[h.Name]; !ok {
			continue
		}
		r := baseRoute(string(h.Target), RouteName(h.Name), h.Name, h.Route, s, suffix)
		r.UtilsUpstream = in.UtilsUpstream
		r.MaxBodyMB = 64
		r.StaticCache = h.Route.StaticCache
		switch h.Target {
		case domain.HostTargetApp:
			a := apps[h.AppID]
			r.UpstreamName = "bento_app_" + strings.ReplaceAll(a.Slug, "-", "_")
			r.AppUpstream = fmt.Sprintf("%s:%d", runtime.AppAlias(a.ID), a.HTTPPort())
			r.UpstreamScheme = "http"
			if a.Runtime.PHP != nil && a.Runtime.PHP.UploadLimitMB > 0 {
				r.MaxBodyMB = a.Runtime.PHP.UploadLimitMB
			}
			r.Unavailable = !in.Running[a.ID]
			r.Includes = append(r.Includes, "app-"+a.Slug)
			upstreams[a.Slug] = AppUpstream{Name: r.UpstreamName, Server: r.AppUpstream}
		case domain.HostTargetUpstream:
			r.UpstreamName = "bento_host_" + upstreamIdent(h.Name)
			scheme, uri := "http", ""
			for _, u := range h.Upstreams {
				sch, host, path := splitUpstream(u)
				scheme, uri = sch, path
				r.ProxyUpstreams = append(r.ProxyUpstreams, host)
			}
			r.UpstreamScheme, r.UpstreamURI = scheme, uri
		case domain.HostTargetRedirect:
			// A redirect to a host the edge serves uses that host's scheme
			// and port; any other host keeps the request's scheme.
			r.UtilsUpstream, r.StaticCache = "", false
			r.RedirectURL = "$scheme://" + h.RedirectTo
			if to, ok := served[h.RedirectTo]; ok {
				if to.Route.TLS == domain.TLSNone {
					r.RedirectURL = "http://" + h.RedirectTo + httpSuffix
				} else {
					r.RedirectURL = "https://" + h.RedirectTo + suffix
				}
			}
		}
		anyACME = anyACME || r.ACME
		routes = append(routes, r)
	}
	slices.SortFunc(routes, func(a, b Route) int { return strings.Compare(a.Name, b.Name) })
	files := map[string][]byte{}
	main, err := assets.Render("edge-nginx.conf.tmpl", map[string]any{
		"ACME": anyACME, "ACMEURL": s.ACMEURL, "ACMEEmail": s.ACMEEmail, "HTTP3": s.HTTP3,
		"UtilsUpstream": in.UtilsUpstream, "StubStatusPort": StubStatusPort,
	})
	if err != nil {
		return nil, err
	}
	files["nginx.conf"] = main
	for slug, u := range upstreams {
		b, err := assets.Render("edge-upstream.conf.tmpl", u)
		if err != nil {
			return nil, err
		}
		files["upstreams/app-"+slug+".conf"] = b
	}
	for _, r := range routes {
		b, err := assets.Render("edge-site.conf.tmpl", r)
		if err != nil {
			return nil, err
		}
		files["sites/"+r.Name+".conf"] = b
	}
	return files, nil
}

// upstreamIdent makes a host name a unique nginx upstream identifier: dots
// and dashes both become "_", so a short hash keeps "a-b.c" and "a.b-c" apart.
func upstreamIdent(host string) string {
	sum := sha256.Sum256([]byte(host))
	return strings.NewReplacer(".", "_", "-", "_").Replace(host) + "_" + hex.EncodeToString(sum[:4])
}

func baseRoute(
	kind, name, host string,
	route domain.Route,
	s domain.EdgeSettings,
	suffix string,
) Route {
	r := Route{
		Kind: kind, Name: name, ServerNames: host, AccessLog: route.AccessLog,
		HTTP3: s.HTTP3, HTTPSPortSuffix: suffix, HTTPSAdvertisedPort: s.HTTPSPort,
		Includes: []string{name},
	}
	switch route.TLS {
	case domain.TLSSelfSigned:
		r.TLS, r.CertFile, r.KeyFile = true, CertsMount+"/boot.crt", CertsMount+"/boot.key"
	case domain.TLSACME:
		r.TLS, r.ACME = true, true
	case domain.TLSExternal:
		r.TLS = true
		r.CertFile = CertsMount + "/external/" + route.CertName + "/fullchain.pem"
		r.KeyFile = CertsMount + "/external/" + route.CertName + "/privkey.pem"
	}
	r.Redirect = r.TLS && route.RedirectHTTPS
	return r
}

func splitUpstream(u string) (scheme, hostport, path string) {
	scheme, port := "http", ":80"
	if strings.HasPrefix(u, "https://") {
		scheme, port = "https", ":443"
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	hostport, path, found := strings.Cut(rest, "/")
	if found {
		path = "/" + path
	}
	if !strings.Contains(hostport, ":") {
		hostport += port
	}
	return scheme, hostport, path
}

// Generations manages the on-disk config directories under edge/conf.
type Generations struct {
	Dir string // <root>/edge/conf
}

func (g Generations) Live() string { return filepath.Join(g.Dir, "live") }

// Stage writes a complete candidate directory and returns its name.
func (g Generations) Stage(files map[string][]byte) (string, error) {
	name := "candidate-" + platform.RandomHex(6)
	dir := filepath.Join(g.Dir, name)
	for _, sub := range []string{"sites", "upstreams"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	for rel, data := range files {
		if err := platform.AtomicWrite(filepath.Join(dir, rel), data, 0o644, platform.RootOwner); err != nil {
			_ = os.RemoveAll(dir) // discard the incomplete candidate
			return "", err
		}
	}
	return name, nil
}

func (g Generations) Discard(name string) { _ = os.RemoveAll(filepath.Join(g.Dir, name)) }

// Promote atomically exchanges the candidate with live. The previous live
// generation is kept as "previous" for inspection.
func (g Generations) Promote(name string) error {
	cand := filepath.Join(g.Dir, name)
	live := g.Live()
	if _, err := os.Lstat(live); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(cand, live)
	}
	if err := unix.Renameat2(unix.AT_FDCWD, cand, unix.AT_FDCWD, live, unix.RENAME_EXCHANGE); err != nil {
		return fmt.Errorf("swap edge generation: %w", err)
	}
	prev := filepath.Join(g.Dir, "previous")
	if err := os.RemoveAll(prev); err != nil {
		return fmt.Errorf("remove previous edge generation: %w", err)
	}
	if err := os.Rename(cand, prev); err != nil {
		return fmt.Errorf("keep previous edge generation: %w", err)
	}
	return nil
}

// Same reports whether files equal the live generation byte for byte.
func (g Generations) Same(files map[string][]byte) bool {
	live := g.Live()
	var count int
	err := filepath.Walk(live, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		count++
		rel, _ := filepath.Rel(live, p)
		want, ok := files[rel]
		got, rerr := os.ReadFile(p)
		if !ok || rerr != nil || string(got) != string(want) {
			return errors.New("differs")
		}
		return nil
	})
	return err == nil && count == len(files)
}

// EnsureBootCert creates the shared self-signed boot certificate once.
func EnsureBootCert(certsDir string) error {
	crt, key := filepath.Join(certsDir, "boot.crt"), filepath.Join(certsDir, "boot.key")
	if _, err := os.Stat(crt); err == nil {
		if _, err := os.Stat(key); err == nil {
			return nil
		}
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "bento-boot", Organization: []string{"Bento"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(30, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	if err := platform.AtomicWrite(
		key,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}),
		0o600,
		platform.RootOwner,
	); err != nil {
		return err
	}
	return platform.AtomicWrite(
		crt,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		0o644,
		platform.RootOwner,
	)
}
