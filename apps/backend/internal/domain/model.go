// Package domain holds Bento's desired-state model and its invariants. It has
// no knowledge of HTTP, SQLite, or Docker.
package domain

import "time"

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
	ID               string
	Slug             string
	UID              int
	GID              int
	Runtime          Runtime
	Resources        Resources
	DesiredRuntime   DesiredRuntime
	Ingress          IngressMode
	Publication      Publication
	Route            Route
	Redis            RedisIdentity
	ConfigGeneration int64
	// CredentialsGeneration increments whenever secret material mounted into
	// the app changes. It is tracked separately so secrets never enter the
	// non-secret configuration fingerprint.
	CredentialsGeneration int64
	Provisioned           bool
	CreatedAt             time.Time
	UpdatedAt             time.Time

	Bindings []Binding
	Domains  []DomainLink
}

// ContainerHome is the home path inside containers.
func (a App) ContainerHome() string { return "/home/" + a.Slug }

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

func (a App) PrimaryDomain() string {
	for _, d := range a.Domains {
		if d.Primary {
			return d.Name
		}
	}
	return ""
}

// Runtime is a finite, validated union discriminated by Kind.
type Runtime struct {
	Kind RuntimeKind  `json:"kind"`
	PHP  *PHPRuntime  `json:"php,omitempty"`
	HTTP *HTTPRuntime `json:"http,omitempty"`
}

type PHPRuntime struct {
	Version      string `json:"version"`
	DocumentRoot string `json:"documentRoot"`
	Routing      string `json:"routing"`
	Pool         string `json:"pool"`
	ReadyPath    string `json:"readyPath,omitempty"`
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

// Route holds managed-edge presentation settings for an app or proxy.
type Route struct {
	TLS           TLSMode `json:"tls"`
	CertName      string  `json:"certName,omitempty"`
	RedirectHTTPS bool    `json:"redirectHttps"`
	AccessLog     bool    `json:"accessLog"`
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

type DomainLink struct {
	Name    string
	Primary bool
}

type DataService struct {
	Name      string
	Engine    Engine
	Version   string
	Image     string
	Volume    string
	CreatedAt time.Time
}

type Proxy struct {
	ID        string
	Name      string
	Upstreams []string
	Route     Route
	Enabled   bool
	Domains   []DomainLink
	CreatedAt time.Time
	UpdatedAt time.Time
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

type BackupSchedule struct {
	Enabled      bool   `json:"enabled"`
	Cron         string `json:"cron"`
	Compression  string `json:"compression"`
	Retain       int    `json:"retain"`
	RcloneRemote string `json:"rcloneRemote"`
}

func DefaultBackupSchedule() BackupSchedule {
	return BackupSchedule{Enabled: false, Cron: "30 2 * * *", Compression: "zstd", Retain: 7}
}

// UIDRange is the configured inclusive allocation range; UID == GID.
type UIDRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

func DefaultUIDRange() UIDRange { return UIDRange{First: 10000, Last: 19999} }
