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
	AccessLog        bool
	Redis            RedisIdentity
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
	if a.Redis.Password != "" {
		pairs = append(pairs, a.Redis.Password, "[redacted]")
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
}

type RedisIdentity struct {
	Mode     string `json:"mode"` // shared | acl
	Prefix   string `json:"prefix"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type Engine string

const (
	EngineMySQL    Engine = "mysql"
	EnginePostgres Engine = "postgres"
	EngineSQLite   Engine = "sqlite"
	EngineRedis    Engine = "redis"
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
	Name      string
	Engine    Engine
	Version   string
	Image     string
	Volume    string
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
}

func DefaultEdgeSettings() EdgeSettings {
	return EdgeSettings{
		Enabled: false, Bind: "0.0.0.0", HTTPPort: 80, HTTPSPort: 443,
		ACMEURL: "https://acme-v02.api.letsencrypt.org/directory",
	}
}

type TunnelSettings struct {
	Enabled bool `json:"enabled"`
	// TokenGeneration increments on each token replacement; the token itself
	// lives only in a private secret file.
	TokenGeneration int64 `json:"tokenGeneration"`
}

// BackupSchedule is one of the stack's backup schedules. Scope selects the
// databases: "all", every database of one app ("app"), or named databases
// of one app ("database").
type BackupSchedule struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Cron         string   `json:"cron"`
	Scope        string   `json:"scope"`
	AppID        string   `json:"appId,omitempty"`
	Databases    []string `json:"databases,omitempty"`
	Compression  string   `json:"compression"`
	Retain       int      `json:"retain"`
	RcloneRemote string   `json:"rcloneRemote"`
}

func DefaultBackupSchedule() BackupSchedule {
	return BackupSchedule{Name: "All backups", Enabled: false, Cron: "30 2 * * *", Scope: "all", Compression: "zstd", Retain: 7}
}

// UIDRange is the configured inclusive allocation range; UID == GID.
type UIDRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

func DefaultUIDRange() UIDRange { return UIDRange{First: 10000, Last: 19999} }
