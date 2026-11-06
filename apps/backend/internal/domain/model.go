// Package domain holds Bento's desired-state model and its invariants. It has
// no knowledge of HTTP, SQLite, or Docker.
package domain

import (
	"strings"
	"time"
)

type RuntimeKind string

const (
	RuntimePHP  RuntimeKind = "php-fpm"
	RuntimeHTTP RuntimeKind = "http-process"
)

type DesiredRuntime string

const (
	DesiredStopped DesiredRuntime = "stopped"
	DesiredRunning DesiredRuntime = "running"
)

type IngressMode string

const (
	IngressManaged  IngressMode = "managed"
	IngressExternal IngressMode = "external"
	IngressNone     IngressMode = "none"
)

type Publication string

const (
	Unpublished Publication = "unpublished"
	Published   Publication = "published"
)

// PHPFrontendPort is the private app-local Nginx port inside PHP containers.
const PHPFrontendPort = 8080

// App is one immutable app incarnation. Slug and home are stable; ID, UID, and
// GID never change for the incarnation.
type App struct {
	ID             string
	Slug           string
	UID            int
	GID            int
	Runtime        Runtime
	Resources      Resources
	DesiredRuntime DesiredRuntime
	Ingress        IngressMode
	Publication    Publication
	// AccessLog enables the app-local Nginx access log (PHP apps). Edge access
	// logs are per host.
	AccessLog bool
	// Valkey runs an app-local Valkey server under the app's supervisor:
	// loopback only, snapshots in the app home. Off by default.
	Valkey bool
	// Minicrond runs the app's scheduler (jobs, workers and Bento's internal
	// tasks). On by default.
	Minicrond bool
	// Protection says which hostile request classes the app's two Nginx
	// layers (the app-local one and the edge routes that target the app)
	// answer with 404. All on by default.
	Protection       Protection
	ConfigGeneration int64
	// CredentialsGeneration increments whenever secret material mounted into
	// the app changes. It is tracked separately so secrets never enter the
	// non-secret configuration fingerprint.
	CredentialsGeneration int64
	Provisioned           bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
	// HomePath is the in-container home when it differs from /home/<slug>
	// (set only when an app is created from a backup). Empty means the default.
	HomePath string

	Bindings []Binding
	// Hosts are the Ingress hosts that target this app, display host first.
	// They are loaded with the app; Ingress owns them.
	Hosts []Host
}

// Redactor replaces the app's known secret values in app-controlled output.
func (a App) Redactor() *strings.Replacer {
	var pairs []string
	for _, b := range a.Bindings {
		if b.Password != "" {
			pairs = append(pairs, b.Password, "[redacted]")
		}
	}
	return strings.NewReplacer(pairs...)
}

// ContainerHome is the home path inside containers.
func (a App) ContainerHome() string {
	if a.HomePath != "" {
		return a.HomePath
	}
	return "/home/" + a.Slug
}

// ContainerCode is the fixed source-code directory inside containers.
func (a App) ContainerCode() string { return a.ContainerHome() + "/app" }

// HTTPPort is the private HTTP port the app listens on inside its namespace.
func (a App) HTTPPort() int {
	if a.Runtime.Kind == RuntimePHP {
		return PHPFrontendPort
	}
	if a.Runtime.HTTP != nil {
		return a.Runtime.HTTP.Port
	}
	return 0
}

func (a App) ReadyPath() string {
	switch {
	case a.Runtime.PHP != nil && a.Runtime.PHP.ReadyPath != "":
		return a.Runtime.PHP.ReadyPath
	case a.Runtime.HTTP != nil && a.Runtime.HTTP.ReadyPath != "":
		return a.Runtime.HTTP.ReadyPath
	}
	return "/"
}

// HasExplicitReadyPath reports whether the operator configured a ready path,
// which makes a 5xx answer from it fail readiness.
func (a App) HasExplicitReadyPath() bool {
	return (a.Runtime.PHP != nil && a.Runtime.PHP.ReadyPath != "") ||
		(a.Runtime.HTTP != nil && a.Runtime.HTTP.ReadyPath != "")
}

// DisplayHost is the host shown as the app's address: its first enabled
// host, else "".
func (a App) DisplayHost() (Host, bool) {
	for _, h := range a.Hosts {
		if h.Enabled {
			return h, true
		}
	}
	return Host{}, false
}

// Runtime is a finite, validated union discriminated by Kind.
type Runtime struct {
	Kind RuntimeKind  `json:"kind"`
	PHP  *PHPRuntime  `json:"php,omitempty"`
	HTTP *HTTPRuntime `json:"http,omitempty"`
	// Env is operator-defined environment exposed to the app process. It is
	// persisted with the runtime but edited independently of it.
	Env []EnvVar `json:"env,omitempty"`
}

// EnvVar is one operator-defined app environment variable.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type PHPRuntime struct {
	Version      string `json:"version"`
	DocumentRoot string `json:"documentRoot"`
	Routing      string `json:"routing"`
	// Mode is a PHPModes key. The optional overrides below are zero when the
	// mode default (or, for MaxWorkers, memory-based sizing) applies.
	Mode                string `json:"mode"`
	MaxWorkers          int    `json:"maxWorkers,omitempty"`
	WebMemoryLimitMB    int    `json:"webMemoryLimitMb,omitempty"`
	CLIMemoryLimitMB    int    `json:"cliMemoryLimitMb,omitempty"`
	MaxExecutionSeconds int    `json:"maxExecutionSeconds,omitempty"`
	MaxInputVars        int    `json:"maxInputVars,omitempty"`
	ReadyPath           string `json:"readyPath,omitempty"`
	// ReleaseSymlink optionally names a symlink below the code directory (for
	// example "current") that the document root may traverse deliberately.
	ReleaseSymlink string `json:"releaseSymlink,omitempty"`
	UploadLimitMB  int    `json:"uploadLimitMb"`
}

const (
	RoutingFrontController = "front-controller"
	RoutingLegacy          = "legacy"
)

type HTTPRuntime struct {
	Toolchain string   `json:"toolchain"`
	Version   string   `json:"version"`
	Argv      []string `json:"argv"`
	Workdir   string   `json:"workdir"`
	Port      int      `json:"port"`
	ReadyPath string   `json:"readyPath,omitempty"`
}

type Resources struct {
	MemoryMB  int `json:"memoryMb"`
	CPUMillis int `json:"cpuMillis"`
	PIDs      int `json:"pids"`
}

type TLSMode string

const (
	TLSNone       TLSMode = "none"
	TLSSelfSigned TLSMode = "self-signed"
	TLSACME       TLSMode = "acme"
	TLSExternal   TLSMode = "external"
)

// Route holds managed-edge presentation settings for one host.
type Route struct {
	TLS           TLSMode `json:"tls"`
	CertName      string  `json:"certName,omitempty"`
	RedirectHTTPS bool    `json:"redirectHttps"`
	AccessLog     bool    `json:"accessLog"`
	// StaticCache lets the edge cache static-extension responses. The
	// operator asserts those responses are not per-user; proxies additionally
	// honor the upstream's Cache-Control and Set-Cookie.
	StaticCache bool `json:"staticCache,omitempty"`
	// MaxBodyMB limits request bodies through the edge; 0 uses the PHP
	// app's upload limit, or 64 MB for HTTP apps and upstreams.
	MaxBodyMB int `json:"maxBodyMb,omitempty"`
}

type Engine string

const (
	EngineMySQL    Engine = "mysql"
	EnginePostgres Engine = "postgres"
	EngineSQLite   Engine = "sqlite"
)

// Binding is an add-only data binding. Relational bindings reference a
// managed service; SQLite bindings own a private file directory.
type Binding struct {
	ID           string
	AppID        string
	Engine       Engine
	Service      string
	Username     string
	Password     string
	Databases    []string
	SQLiteFileID string
	Vacuum       *VacuumSlot
	CreatedAt    time.Time
}

type VacuumSlot struct {
	DayOfWeek int `json:"dayOfWeek"`
	Hour      int `json:"hour"`
	Minute    int `json:"minute"`
}

// SQLiteContainerDir is where a SQLite binding directory is mounted.
func (b Binding) SQLiteContainerDir() string { return "/var/lib/bento/sqlite/" + b.SQLiteFileID }

// Protection selects the request classes an app's Nginx layers refuse with a
// plain 404 (never 403, so a scanner learns nothing about what exists).
// The zero value turns everything off; use DefaultProtection for a new app.
type Protection struct {
	// BlockDotFiles refuses every path with a dot-prefixed segment except
	// /.well-known/.
	BlockDotFiles bool `json:"blockDotFiles"`
	// BlockPHPProbes refuses WordPress paths, database admin tools, known
	// webshell names, PHP source extensions and PHP under upload, cache and
	// dependency directories. Turn it off for WordPress or legacy PHP sites
	// that serve such paths themselves.
	BlockPHPProbes bool `json:"blockPhpProbes"`
	// BlockSensitiveFiles refuses backup, dump, swap, archive, source map and
	// package-manager metadata files.
	BlockSensitiveFiles bool `json:"blockSensitiveFiles"`
}

// DefaultProtection is the protection a new app gets: everything on.
func DefaultProtection() Protection {
	return Protection{BlockDotFiles: true, BlockPHPProbes: true, BlockSensitiveFiles: true}
}

// ProtectionPatch is a partial protection change: nil fields keep the value
// they are applied to.
type ProtectionPatch struct {
	BlockDotFiles       *bool `json:"blockDotFiles,omitempty"`
	BlockPHPProbes      *bool `json:"blockPhpProbes,omitempty"`
	BlockSensitiveFiles *bool `json:"blockSensitiveFiles,omitempty"`
}

// Apply returns p with the patch's set fields replaced.
func (p Protection) Apply(patch *ProtectionPatch) Protection {
	if patch == nil {
		return p
	}
	if patch.BlockDotFiles != nil {
		p.BlockDotFiles = *patch.BlockDotFiles
	}
	if patch.BlockPHPProbes != nil {
		p.BlockPHPProbes = *patch.BlockPHPProbes
	}
	if patch.BlockSensitiveFiles != nil {
		p.BlockSensitiveFiles = *patch.BlockSensitiveFiles
	}
	return p
}

// HostTarget says where an Ingress host sends its requests.
type HostTarget string

const (
	HostTargetApp      HostTarget = "app"
	HostTargetUpstream HostTarget = "upstream"
	HostTargetRedirect HostTarget = "redirect"
)

// Host is one Ingress host name. Ingress owns host names: each name is unique
// and points at exactly one target, with its own TLS and edge settings.
type Host struct {
	Name   string
	Target HostTarget
	// AppID is set only for app targets.
	AppID string
	// Upstreams are set only for upstream targets (load-balanced).
	Upstreams []string
	// RedirectTo is set only for redirect targets: the host name requests are
	// permanently redirected to, keeping the path and query.
	RedirectTo string
	Route      Route
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type DataService struct {
	Name    string
	Engine  Engine
	Version string
	Image   string
	Volume  string
	// Settings are tuned server parameters (MySQL/PostgreSQL only).
	Settings  ServiceSettings
	CreatedAt time.Time
}

type EdgeSettings struct {
	Enabled   bool   `json:"enabled"`
	Bind      string `json:"bind"`
	HTTPPort  int    `json:"httpPort"`
	HTTPSPort int    `json:"httpsPort"`
	HTTP3     bool   `json:"http3"`
	ACMEEmail string `json:"acmeEmail"`
	ACMEURL   string `json:"acmeUrl"`
	// FrontProxy adds listeners for a TCP proxy in front of the edge (for
	// example a public VPS forwarding over a VPN) that speaks PROXY protocol.
	FrontProxy FrontProxySettings `json:"frontProxy"`
}

// FrontProxySettings are extra edge listeners that expect a PROXY protocol
// header. The header's client address is believed only when the connecting
// peer is in TrustedProxies. Visitors on this path reached the front proxy on
// the standard ports 80/443, so the edge forwards those ports and no suffix.
type FrontProxySettings struct {
	Enabled        bool     `json:"enabled"`
	Bind           string   `json:"bind"`
	HTTPPort       int      `json:"httpPort"`
	HTTPSPort      int      `json:"httpsPort"`
	TrustedProxies []string `json:"trustedProxies"`
}

func DefaultEdgeSettings() EdgeSettings {
	return EdgeSettings{
		Enabled: false, Bind: "0.0.0.0", HTTPPort: 80, HTTPSPort: 443,
		ACMEURL:    "https://acme-v02.api.letsencrypt.org/directory",
		FrontProxy: FrontProxySettings{Bind: "0.0.0.0", HTTPPort: 8080, HTTPSPort: 8443, TrustedProxies: []string{}},
	}
}

type TunnelSettings struct {
	Enabled bool `json:"enabled"`
	// TokenGeneration increments on each token replacement; the token itself
	// lives only in a private secret file.
	TokenGeneration int64 `json:"tokenGeneration"`
}

// UIDRange is the configured inclusive allocation range; UID == GID.
type UIDRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

func DefaultUIDRange() UIDRange { return UIDRange{First: 10000, Last: 19999} }
