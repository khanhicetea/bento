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
	"maps"
	"math/big"
	"net/netip"
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
	// PendingMount holds the placeholder certificate an ACME host presents
	// until its first certificate is issued (EnsurePendingCert).
	PendingMount = "/etc/bento-edge-pending"
	// HooksMount holds the backend's webhook socket. It is the only backend
	// endpoint the edge can reach and serves nothing but webhook deliveries.
	HooksMount = "/var/lib/bento-hooks"
	// LogsMount holds the per-host traffic access logs (TrafficLogFile),
	// rotated by the backend's traffic loop.
	LogsMount = "/var/log/bento-edge"
	LiveConf  = ConfMount + "/live/nginx.conf"
	// StubStatusPort is the edge's loopback-only stub_status listener. It sits
	// in an uncommon high range so operator drop-ins are unlikely to collide.
	StubStatusPort = 57319
	// FrontHTTPPort and FrontHTTPSPort are the container ports of the PROXY
	// protocol listeners. They are fixed so the config can tell the paths
	// apart by $server_port whatever host ports they are published on.
	FrontHTTPPort  = 8080
	FrontHTTPSPort = 8443
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
	// TrafficLog is the host's filtered access log file for its traffic
	// report; set whenever AccessLog is.
	TrafficLog  string
	StaticCache bool
	// Protection is the rendered protection location blocks (see
	// runtime.ProtectionConfig). Only hosts that target an app carry it:
	// external upstreams and redirects are operator-owned.
	Protection string
	// AppUpstream is the app container alias host:port, re-resolved at runtime.
	AppUpstream    string
	ProxyUpstreams []string
	UpstreamName   string
	UpstreamScheme string
	UpstreamURI    string
	// UpstreamTLSName is set for an HTTPS upstream named by a DNS name: the
	// edge sends it as SNI, verifies the upstream's certificate against it,
	// and sends UpstreamHostHeader as Host. An HTTPS upstream named by an IP
	// address leaves both empty: the connection is encrypted but the
	// certificate is not verified, and Host stays the visitor's.
	UpstreamTLSName     string
	UpstreamHostHeader  string
	MaxBodyMB           int
	Unavailable         bool
	HTTP3               bool
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
	// FrontHTTPPort and FrontHTTPSPort are the PROXY protocol listeners, or 0
	// when no front proxy is configured.
	FrontHTTPPort  int
	FrontHTTPSPort int
	// ClientAddr is the nginx variable holding the visitor's address.
	ClientAddr string
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
	// TunnelIP is the reserved apps-network address of the stack's
	// cloudflared. Forwarding headers (scheme, visitor IP) are trusted only
	// on connections from it; "" trusts none.
	TunnelIP string
	// ACMEHold names ACME hosts whose issuance is backing off (see
	// PlanACME): they are served with the boot certificate and no
	// acme_certificate, so the module does not retry them on every reload.
	ACMEHold map[string]bool
}

// servedHosts returns the hosts the edge serves at all, by name.
func servedHosts(hosts []domain.Host, apps map[string]domain.App) map[string]domain.Host {
	served := map[string]domain.Host{}
	for _, h := range hosts {
		if !h.Enabled {
			continue
		}
		var ok bool
		switch h.Target {
		case domain.HostTargetApp:
			a, found := apps[h.AppID]
			ok = found && a.Ingress == domain.IngressManaged && a.Publication == domain.Published
		case domain.HostTargetUpstream:
			ok = len(h.Upstreams) > 0
		case domain.HostTargetRedirect:
			ok = h.RedirectTo != ""
		}
		if ok {
			served[h.Name] = h
		}
	}
	return served
}

// ACMEHosts lists the served hosts whose certificate comes from ACME, in
// name order.
func ACMEHosts(in Input) []ACMEHost {
	apps := make(map[string]domain.App, len(in.Apps))
	for _, a := range in.Apps {
		apps[a.ID] = a
	}
	var out []ACMEHost
	for _, h := range servedHosts(in.Hosts, apps) {
		if h.Route.TLS == domain.TLSACME {
			out = append(out, ACMEHost{Name: h.Name, UpdatedAt: h.UpdatedAt})
		}
	}
	slices.SortFunc(out, func(a, b ACMEHost) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// RouteName is the edge route (site file and drop-in directory) of a host.
func RouteName(host string) string { return "host-" + host }

// TrafficLogFile is the name of a host's traffic access log in LogsMount (and
// in the stack's traffic/logs directory). Host names are normalized domains,
// so the name is a safe single path element.
func TrafficLogFile(host string) string { return host + ".log" }

// Render produces the full candidate file set, keyed by relative path: one
// site per enabled host whose target is routable, and one upstream per app
// that at least one host routes to.
func Render(in Input) (map[string][]byte, error) {
	s := in.Settings
	var suffix, httpSuffix string
	if s.HTTPSPort != 443 {
		suffix = ":" + strconv.Itoa(s.HTTPSPort)
	}
	httpPort := s.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	if httpPort != 80 {
		httpSuffix = ":" + strconv.Itoa(httpPort)
	}
	front, trusted, err := frontProxy(s.FrontProxy)
	if err != nil {
		return nil, err
	}
	apps := make(map[string]domain.App, len(in.Apps))
	for _, a := range in.Apps {
		apps[a.ID] = a
	}
	served := servedHosts(in.Hosts, apps)
	var routes []Route
	upstreams := map[string]AppUpstream{}
	var anyACME, anyTraffic bool
	for _, h := range in.Hosts {
		if _, ok := served[h.Name]; !ok {
			continue
		}
		r := baseRoute(string(h.Target), RouteName(h.Name), h.Name, h.Route, s)
		if r.ACME && in.ACMEHold[h.Name] {
			r.ACME, r.CertFile, r.KeyFile = false, CertsMount+"/boot.crt", CertsMount+"/boot.key"
		}
		r.ClientAddr = "$remote_addr"
		if front {
			r.FrontHTTPPort, r.FrontHTTPSPort, r.ClientAddr = FrontHTTPPort, FrontHTTPSPort, "$bento_client_addr"
		}
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
			r.Protection = runtime.ProtectionConfig(a.Protection, "  ")
			r.Includes = append(r.Includes, "app-"+a.Slug)
			upstreams[a.Slug] = AppUpstream{Name: r.UpstreamName, Server: r.AppUpstream}
		case domain.HostTargetUpstream:
			r.UpstreamName = "bento_host_" + upstreamIdent(h.Name)
			// The first URL decides the scheme, path and TLS name; validation
			// keeps the others consistent with it.
			scheme, first, uri := splitUpstream(h.Upstreams[0])
			for _, u := range h.Upstreams {
				_, host, _ := splitUpstream(u)
				r.ProxyUpstreams = append(r.ProxyUpstreams, host)
			}
			r.UpstreamScheme, r.UpstreamURI = scheme, uri
			if scheme == "https" {
				r.UpstreamTLSName, r.UpstreamHostHeader = upstreamTLSName(first)
			}
		case domain.HostTargetRedirect:
			// A redirect to a host the edge serves uses that host's scheme
			// and port; any other host keeps the request's scheme.
			r.UtilsUpstream, r.StaticCache = "", false
			// The suffixes are the edge's published ports, or none for a
			// visitor who came through the tunnel.
			r.RedirectURL = "$bento_proto://" + h.RedirectTo
			if to, ok := served[h.RedirectTo]; ok {
				if to.Route.TLS == domain.TLSNone {
					r.RedirectURL = "http://" + h.RedirectTo + "$bento_http_suffix"
				} else {
					r.RedirectURL = "https://" + h.RedirectTo + "$bento_https_suffix"
				}
			}
		}
		if h.Route.MaxBodyMB > 0 {
			r.MaxBodyMB = h.Route.MaxBodyMB
		}
		if r.AccessLog {
			r.TrafficLog = LogsMount + "/" + TrafficLogFile(h.Name)
			anyTraffic = true
		}
		anyACME = anyACME || r.ACME
		routes = append(routes, r)
	}
	slices.SortFunc(routes, func(a, b Route) int { return strings.Compare(a.Name, b.Name) })
	files := map[string][]byte{}
	main, err := assets.Render("edge-nginx.conf.tmpl", map[string]any{
		"ACME": anyACME, "ACMEURL": s.ACMEURL, "ACMEEmail": s.ACMEEmail, "HTTP3": s.HTTP3,
		"UtilsUpstream": in.UtilsUpstream, "StubStatusPort": StubStatusPort,
		"TunnelIP": in.TunnelIP, "HTTPSPortSuffix": suffix, "HTTPPortSuffix": httpSuffix,
		"HTTPPort": httpPort, "HTTPSPort": s.HTTPSPort,
		"FrontProxy": front, "FrontTrusted": trusted,
		"FrontHTTPPort": FrontHTTPPort, "FrontHTTPSPort": FrontHTTPSPort,
		"Traffic": anyTraffic,
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

// frontProxy reports whether PROXY protocol listeners are rendered and the
// peers trusted on them. Settings are validated on save; anything that does
// not parse as a non-catch-all prefix here is refused rather than rendered.
func frontProxy(fp domain.FrontProxySettings) (bool, []string, error) {
	if !fp.Enabled {
		return false, nil, nil
	}
	if len(fp.TrustedProxies) == 0 {
		return false, nil, errors.New("front proxy has no trusted proxies")
	}
	out := make([]string, 0, len(fp.TrustedProxies))
	for _, t := range fp.TrustedProxies {
		p, err := netip.ParsePrefix(t)
		if err != nil || p.Bits() == 0 {
			return false, nil, fmt.Errorf("front proxy trusted proxy %q is not a CIDR", t)
		}
		out = append(out, p.Masked().String())
	}
	return true, out, nil
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
) Route {
	r := Route{
		Kind: kind, Name: name, ServerNames: host, AccessLog: route.AccessLog,
		HTTP3: s.HTTP3, HTTPSAdvertisedPort: s.HTTPSPort,
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

// upstreamTLSName returns the name an HTTPS upstream is verified against and
// the Host header it is sent, or "" for both when it is named by an IP
// address (no SNI is possible and a certificate for an address is rare).
// hostport always carries a port (splitUpstream adds the default).
func upstreamTLSName(hostport string) (name, hostHeader string) {
	host, port := hostport, ""
	if i := strings.LastIndex(hostport, ":"); i >= 0 && !strings.HasSuffix(hostport, "]") {
		host, port = hostport[:i], hostport[i+1:]
	}
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return "", ""
	}
	if port == "443" || port == "" {
		return host, host
	}
	return host, host + ":" + port
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

// loadedFile, beside the generation directories, records the digest of the
// live generation the running edge last loaded: by starting on it, or by a
// reload that was delivered. Promotion changes live before the reload, so a
// lost reload leaves live and the loaded digest apart until a retry.
const loadedFile = "loaded"

// Digest identifies a generation's file set.
func Digest(files map[string][]byte) string {
	h := sha256.New()
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(files[rel]))
		h.Write(files[rel])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// MarkLoaded records that the running edge serves files.
func (g Generations) MarkLoaded(files map[string][]byte) error {
	return platform.AtomicWrite(filepath.Join(g.Dir, loadedFile), []byte(Digest(files)+"\n"), 0o644, platform.RootOwner)
}

// Loaded reports whether the running edge was last recorded serving files.
// A missing record (a fresh root, or an edge from before the record existed)
// reports false, which costs one redundant reload.
// Revert puts the previous generation back live and drops the one it
// replaces: an edge recreate that fails after promoting new routes restarts
// the old edge on what it last ran, never on routes only the new image
// validated. Without a previous generation it does nothing.
func (g Generations) Revert() error {
	prev := filepath.Join(g.Dir, "previous")
	if _, err := os.Lstat(prev); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := unix.Renameat2(unix.AT_FDCWD, prev, unix.AT_FDCWD, g.Live(), unix.RENAME_EXCHANGE); err != nil {
		return fmt.Errorf("revert edge generation: %w", err)
	}
	return os.RemoveAll(prev)
}

func (g Generations) Loaded(files map[string][]byte) bool {
	b, err := os.ReadFile(filepath.Join(g.Dir, loadedFile))
	return err == nil && strings.TrimSpace(string(b)) == Digest(files)
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
	return ensureSelfSigned(certsDir, "boot", "bento-boot", platform.RootOwner)
}

// EnsurePendingCert creates the placeholder certificate once: a self-signed
// pair that ACME hosts present until their first certificate is issued. The
// ACME module's certificate variables are empty until then, and nginx loads
// a variable certificate per handshake in the unprivileged worker, so this
// pair is owned by the worker (owner). It is used for nothing else, and no
// client trusts it.
func EnsurePendingCert(dir string, owner platform.Owner) error {
	return ensureSelfSigned(dir, "pending", "bento-pending", owner)
}

// ensureSelfSigned writes <name>.key (0600) and <name>.crt (0644) in dir,
// owned by owner, unless both exist.
func ensureSelfSigned(dir, name, commonName string, owner platform.Owner) error {
	crt, key := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
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
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName, Organization: []string{"Bento"}},
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
	if err := platform.AtomicWrite(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600, owner); err != nil {
		return err
	}
	return platform.AtomicWrite(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644, owner)
}
