// Package dto defines the public REST wire contract for /api/v1. These types
// are the single source for the generated frontend TypeScript
// (apps/web/src/api/generated/types.ts via tygo). They never embed
// persistence or Docker SDK structs.
//
// Wire conventions: JSON field names are camelCase; timestamps are RFC 3339
// UTC strings with millisecond precision (empty string when unknown);
// counters are JSON numbers well inside JavaScript's safe integer range;
// optional request fields are pointers or omitted.
package dto

// ---- errors ----

type ErrorCode string

const (
	ErrorCodeValidation   ErrorCode = "validation"
	ErrorCodeNotFound     ErrorCode = "not_found"
	ErrorCodeConflict     ErrorCode = "conflict"
	ErrorCodePrecondition ErrorCode = "precondition_failed"
	ErrorCodeConfirmation ErrorCode = "confirmation_required"
	ErrorCodeUnauthorized ErrorCode = "unauthorized"
	ErrorCodeForbidden    ErrorCode = "forbidden"
	ErrorCodeRateLimited  ErrorCode = "rate_limited"
	ErrorCodeUnavailable  ErrorCode = "unavailable"
	ErrorCodeInternal     ErrorCode = "internal"
	ErrorCodeTooLarge     ErrorCode = "too_large"
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ErrorBody struct {
	Code    ErrorCode    `json:"code"`
	Message string       `json:"message"`
	Fields  []FieldError `json:"fields,omitempty"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ---- enums ----

type RuntimeKind string

const (
	RuntimeKindPHP  RuntimeKind = "php-fpm"
	RuntimeKindHTTP RuntimeKind = "http-process"
)

type DesiredRuntime string

const (
	DesiredRuntimeStopped DesiredRuntime = "stopped"
	DesiredRuntimeRunning DesiredRuntime = "running"
)

type IngressMode string

const (
	IngressModeManaged  IngressMode = "managed"
	IngressModeExternal IngressMode = "external"
	IngressModeNone     IngressMode = "none"
)

type Publication string

const (
	PublicationUnpublished Publication = "unpublished"
	PublicationPublished   Publication = "published"
)

type TLSMode string

const (
	TLSModeNone       TLSMode = "none"
	TLSModeSelfSigned TLSMode = "self-signed"
	TLSModeACME       TLSMode = "acme"
	TLSModeExternal   TLSMode = "external"
)

type Engine string

const (
	EngineMySQL    Engine = "mysql"
	EnginePostgres Engine = "postgres"
	EngineSQLite   Engine = "sqlite"
)

// ObservedState is Docker-observed runtime state, never persisted intent.
type ObservedState string

const (
	ObservedStateAbsent    ObservedState = "absent"
	ObservedStateStarting  ObservedState = "starting"
	ObservedStateHealthy   ObservedState = "healthy"
	ObservedStateUnhealthy ObservedState = "unhealthy"
	ObservedStateStopped   ObservedState = "stopped"
	ObservedStateFailed    ObservedState = "failed"
	ObservedStateBlocked   ObservedState = "blocked"
)

type OperationState string

const (
	OperationStateQueued      OperationState = "queued"
	OperationStateRunning     OperationState = "running"
	OperationStateSucceeded   OperationState = "succeeded"
	OperationStateFailed      OperationState = "failed"
	OperationStateCancelled   OperationState = "cancelled"
	OperationStateInterrupted OperationState = "interrupted"
)

// ---- runtime variants ----

// RuntimeSpec is a validated union discriminated by Kind: exactly one of php
// (kind "php-fpm") or http (kind "http-process") is present.
type RuntimeSpec struct {
	Kind RuntimeKind  `json:"kind"`
	PHP  *PHPRuntime  `json:"php,omitempty"`
	HTTP *HTTPRuntime `json:"http,omitempty"`
}

type PHPRuntime struct {
	Version      string `json:"version"`
	DocumentRoot string `json:"documentRoot"`
	Routing      string `json:"routing"`
	// Mode is "standard" or "high-concurrency". Zero-valued overrides use the
	// mode default; maxWorkers 0 sizes workers from the app memory limit.
	Mode                string `json:"mode"`
	MaxWorkers          int    `json:"maxWorkers,omitempty"`
	WebMemoryLimitMB    int    `json:"webMemoryLimitMb,omitempty"`
	CLIMemoryLimitMB    int    `json:"cliMemoryLimitMb,omitempty"`
	MaxExecutionSeconds int    `json:"maxExecutionSeconds,omitempty"`
	MaxInputVars        int    `json:"maxInputVars,omitempty"`
	ReadyPath           string `json:"readyPath,omitempty"`
	ReleaseSymlink      string `json:"releaseSymlink,omitempty"`
	UploadLimitMB       int    `json:"uploadLimitMb"`
}

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

type Route struct {
	TLS           TLSMode `json:"tls"`
	CertName      string  `json:"certName,omitempty"`
	RedirectHTTPS bool    `json:"redirectHttps"`
	AccessLog     bool    `json:"accessLog"`
	StaticCache   bool    `json:"staticCache,omitempty"`
	// MaxBodyMB limits request bodies through the edge; 0 or absent uses the
	// PHP app's upload limit, or 64 MB for HTTP apps and upstreams.
	MaxBodyMB int `json:"maxBodyMb,omitempty"`
}

// Binding never carries a password.
type Binding struct {
	ID         string   `json:"id"`
	Engine     Engine   `json:"engine"`
	Service    string   `json:"service,omitempty"`
	Username   string   `json:"username,omitempty"`
	Databases  []string `json:"databases"`
	SQLitePath string   `json:"sqlitePath,omitempty"`
	CreatedAt  string   `json:"createdAt"`
}

type Observed struct {
	State             ObservedState `json:"state"`
	ContainerID       string        `json:"containerId,omitempty"`
	GenerationCurrent bool          `json:"generationCurrent"`
	Health            string        `json:"health,omitempty"`
	StartedAt         string        `json:"startedAt,omitempty"`
	ExitCode          int           `json:"exitCode"`
	Message           string        `json:"message,omitempty"`
}

type Reconcile struct {
	Failures    int    `json:"failures"`
	Blocked     bool   `json:"blocked"`
	NextAttempt string `json:"nextAttempt,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

// ReconcileTarget is a reconciler target that is failing, blocked, or has an
// operation pending. ID is an app id, "edge", "tunnel", "dbadmin", or
// "service:<name>".
type ReconcileTarget struct {
	ID               string    `json:"id"`
	Reconcile        Reconcile `json:"reconcile"`
	PendingOperation string    `json:"pendingOperation,omitempty"`
}

type ReconcileStatus struct {
	Targets []ReconcileTarget `json:"targets"`
}

// IngressInfo explains who owns the app's public route.
type IngressInfo struct {
	Mode          IngressMode `json:"mode"`
	BentoControls bool        `json:"bentoControls"`
	InternalURL   string      `json:"internalUrl"`
	Note          string      `json:"note"`
}

type AppSummary struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	UID            int            `json:"uid"`
	Kind           RuntimeKind    `json:"kind"`
	Toolchain      string         `json:"toolchain"`
	Version        string         `json:"version"`
	DesiredRuntime DesiredRuntime `json:"desiredRuntime"`
	Ingress        IngressMode    `json:"ingress"`
	Publication    Publication    `json:"publication"`
	// Hosts are the names of the Ingress hosts that target the app, display
	// host first (enabled before disabled).
	Hosts            []string `json:"hosts"`
	Provisioned      bool     `json:"provisioned"`
	ConfigGeneration int      `json:"configGeneration"`
	Observed         Observed `json:"observed"`
	// BindingSummary lists data bindings without credentials, for list views.
	BindingSummary []BindingSummary `json:"bindingSummary"`
	Resources      Resources        `json:"resources"`
}

type BindingSummary struct {
	Engine    Engine `json:"engine"`
	Service   string `json:"service,omitempty"`
	Databases int    `json:"databases"`
}

type App struct {
	AppSummary `tstype:",extends"`
	GID        int    `json:"gid"`
	Home       string `json:"home"`
	// HomePath is the stored in-container home; empty means /home/<slug>. Read-only.
	HomePath string      `json:"homePath,omitempty"`
	Runtime  RuntimeSpec `json:"runtime"`
	// AccessLog is the app-local Nginx access log; edge logs are per host.
	AccessLog bool `json:"accessLog"`
	// Valkey runs the app's own Valkey on 127.0.0.1:6379 inside its
	// container, with snapshots in the app home.
	Valkey bool `json:"valkey"`
	// Minicrond runs the app's scheduler (jobs and workers).
	Minicrond bool `json:"minicrond"`
	// Protection is what the app's Nginx layers answer with 404.
	Protection  Protection  `json:"protection"`
	Env         []EnvVar    `json:"env"`
	Bindings    []Binding   `json:"bindings"`
	IngressInfo IngressInfo `json:"ingressInfo"`
	Reconcile   Reconcile   `json:"reconcile"`
	CreatedAt   string      `json:"createdAt"`
	UpdatedAt   string      `json:"updatedAt"`
}

// Protection selects the request classes an app's Nginx layers (the app-local
// one and the edge routes that target the app) refuse with a plain 404.
type Protection struct {
	// BlockDotFiles refuses dot files and directories except /.well-known/.
	BlockDotFiles bool `json:"blockDotFiles"`
	// BlockPhpProbes refuses WordPress paths, database admin tools, webshell
	// names, PHP source extensions and PHP under upload and dependency
	// directories. Turn it off for WordPress or legacy PHP sites that serve
	// such paths themselves.
	BlockPhpProbes bool `json:"blockPhpProbes"`
	// BlockSensitiveFiles refuses backup, dump, swap, archive, source map and
	// package-manager metadata files.
	BlockSensitiveFiles bool `json:"blockSensitiveFiles"`
}

// ProtectionPatch changes some protection settings; absent fields are kept
// (all on for a new app).
type ProtectionPatch struct {
	BlockDotFiles       *bool `json:"blockDotFiles,omitempty"`
	BlockPhpProbes      *bool `json:"blockPhpProbes,omitempty"`
	BlockSensitiveFiles *bool `json:"blockSensitiveFiles,omitempty"`
}

type AppList struct {
	Apps []AppSummary `json:"apps"`
}

// ---- requests ----

type BindingRequest struct {
	Engine  Engine `json:"engine"`
	Service string `json:"service,omitempty"`
}

type CreateAppRequest struct {
	Slug      string      `json:"slug"`
	Runtime   RuntimeSpec `json:"runtime"`
	Resources *Resources  `json:"resources,omitempty"`
	Ingress   IngressMode `json:"ingress,omitempty"`
	AccessLog bool        `json:"accessLog,omitempty"`
	// Valkey runs the app's own Valkey server (default off).
	Valkey bool `json:"valkey,omitempty"`
	// Minicrond runs the app's scheduler (default on).
	Minicrond *bool `json:"minicrond,omitempty"`
	// Protection overrides the all-on default.
	Protection *ProtectionPatch `json:"protection,omitempty"`
	// Hosts are Ingress hosts created together with the app, targeting it.
	Hosts    []AppHostRequest `json:"hosts"`
	Bindings []BindingRequest `json:"bindings"`
}

// AppHostRequest is an Ingress host created with its app.
type AppHostRequest struct {
	Name  string `json:"name"`
	Route *Route `json:"route,omitempty"`
}

type UpdateAppRequest struct {
	ExpectedGeneration int              `json:"expectedGeneration,omitempty"`
	Runtime            *RuntimeSpec     `json:"runtime,omitempty"`
	Resources          *Resources       `json:"resources,omitempty"`
	Ingress            *IngressMode     `json:"ingress,omitempty"`
	AccessLog          *bool            `json:"accessLog,omitempty"`
	Valkey             *bool            `json:"valkey,omitempty"`
	Minicrond          *bool            `json:"minicrond,omitempty"`
	Protection         *ProtectionPatch `json:"protection,omitempty"`
	// Env, when present, replaces the app's environment variables.
	Env *[]EnvVar `json:"env,omitempty"`
}

// EnvVar is one operator-defined environment variable exposed to the app
// process. Values are operator configuration and are shown back verbatim.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ConfirmRequest carries an exact destructive confirmation phrase.
type ConfirmRequest struct {
	Confirm string `json:"confirm"`
}

type AddDatabaseRequest struct {
	Name string `json:"name"`
}

type PermissionsRequest struct {
	Mode string `json:"mode"`
}

// ---- git source ----

// GitSource is an app's repository source. The deploy private key is never
// part of the wire contract; only its public half and fingerprint are shown.
type GitSource struct {
	Configured     bool   `json:"configured"`
	RepoURL        string `json:"repoUrl"`
	Branch         string `json:"branch"`
	UsesSSH        bool   `json:"usesSsh"`
	PublicKey      string `json:"publicKey"`
	Fingerprint    string `json:"fingerprint"`
	KeyCreatedAt   string `json:"keyCreatedAt"`
	DeployedCommit string `json:"deployedCommit"`
	DeployedAt     string `json:"deployedAt"`
}

type GitSourceRequest struct {
	RepoURL   string `json:"repoUrl"`
	Branch    string `json:"branch"`
	RotateKey bool   `json:"rotateKey,omitempty"`
}

// ---- utils listener ----

// UtilsSettings: BaseURL is where the operator's ingress exposes the utils
// listener (for example https://hooks.example.com); webhook URLs use it.
type UtilsSettings struct {
	BaseURL string `json:"baseUrl"`
	// Targets are the addresses the utils listener serves (read-only).
	Targets []string `json:"targets"`
}

type UtilsSettingsRequest struct {
	BaseURL string `json:"baseUrl"`
}

// ---- database browser ----

// DBAdminStatus reports the shared database browser (Adminer) container.
type DBAdminStatus struct {
	Enabled bool          `json:"enabled"`
	State   ObservedState `json:"state"`
}

type DBAdminSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// DBAdminTicket opens one binding in the database browser. Path is a
// single-use path on the utils listener that expires at ExpiresAt; open it
// under BaseURL (the utils base URL) or, when that is empty, on the current
// host at LoopbackPort (0 when the utils listener has no loopback address).
type DBAdminTicket struct {
	Path         string `json:"path"`
	BaseURL      string `json:"baseUrl"`
	LoopbackPort int    `json:"loopbackPort"`
	ExpiresAt    string `json:"expiresAt"`
}

// ---- deploy webhook ----

// Webhook is an app's deploy webhook. The secret is never part of this shape;
// it is returned once, in WebhookSecret, by the call that generates it.
type Webhook struct {
	Enabled bool `json:"enabled"`
	// Path is served on every edge-routed domain.
	Path string `json:"path"`
	// URL uses the utils base URL, else the app's primary domain when the
	// edge forwards webhooks to Bento, else "". Behind a tunnel or a
	// loopback-bound edge it is https://<domain> without the edge port.
	URL string `json:"url"`
	// Targets are the backend's utils listeners; point a host proxy or a
	// Cloudflare Tunnel path rule for /_bento/webhook/* at one of them.
	Targets         []string          `json:"targets"`
	SecretCreatedAt string            `json:"secretCreatedAt"`
	Deliveries      []WebhookDelivery `json:"deliveries"`
}

type WebhookDelivery struct {
	At          string `json:"at"`
	Provider    string `json:"provider"`
	Event       string `json:"event"`
	DeliveryID  string `json:"deliveryId"`
	Ref         string `json:"ref"`
	Commit      string `json:"commit"`
	Pusher      string `json:"pusher"`
	Auth        string `json:"auth"`
	Result      string `json:"result"`
	Detail      string `json:"detail"`
	OperationID string `json:"operationId"`
	// OperationState is the deploy operation's state (queued, running,
	// succeeded, failed, cancelled, interrupted): Result "deployed" only
	// records that the deploy was submitted. Empty without an operation or
	// once it was pruned from history.
	OperationState string `json:"operationState"`
}

// WebhookSecret answers enabling or rotating a webhook. Secret is shown only
// here; store it in the git host's webhook settings.
type WebhookSecret struct {
	Webhook `tstype:",extends"`
	Secret  string `json:"secret"`
}

// ---- operations ----

type OperationEvent struct {
	Seq     int    `json:"seq"`
	At      string `json:"at"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type Operation struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	TargetKind   string         `json:"targetKind"`
	TargetID     string         `json:"targetId"`
	State        OperationState `json:"state"`
	Phase        string         `json:"phase"`
	Origin       string         `json:"origin"`
	ErrorCode    string         `json:"errorCode,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Guidance     string         `json:"guidance,omitempty"`
	Result       map[string]any `json:"result,omitempty"`
	CreatedAt    string         `json:"createdAt"`
	StartedAt    string         `json:"startedAt,omitempty"`
	FinishedAt   string         `json:"finishedAt,omitempty"`
	// WaitingOn is set on a queued operation that waits behind another
	// operation touching the same app, service or stack-wide state.
	WaitingOn string           `json:"waitingOn,omitempty"`
	Events    []OperationEvent `json:"events,omitempty"`
}

type OperationList struct {
	Operations []Operation `json:"operations"`
}

// Accepted is returned with 202 for long-running mutations. Repeating the
// request with the same Idempotency-Key returns the same operation.
type Accepted struct {
	Operation Operation `json:"operation"`
	StatusURL string    `json:"statusUrl"`
	App       *App      `json:"app,omitempty"`
}

// ---- data services ----

type Service struct {
	Name        string        `json:"name"`
	Engine      Engine        `json:"engine"`
	Version     string        `json:"version"`
	Image       string        `json:"image"`
	Volume      string        `json:"volume"`
	Initialized bool          `json:"initialized"`
	State       ObservedState `json:"state"`
	Message     string        `json:"message,omitempty"`
	// Settings are the saved tuned parameters; unset keys use the default.
	Settings map[string]string `json:"settings"`
	// SettingsApplied is false while the container runs with other settings
	// than the saved ones (a configure did not finish yet).
	SettingsApplied bool `json:"settingsApplied"`
	// UpdateAvailable is true when this Bento ships a newer image of the
	// service's version (a patch release); a restart moves the service onto it.
	UpdateAvailable bool `json:"updateAvailable"`
	// Tunables lists the parameters this engine allows tuning.
	Tunables []ServiceTunable `json:"tunables"`
}

type TunableKind string

const (
	TunableKindSize TunableKind = "size"
	TunableKindInt  TunableKind = "int"
)

type ServiceTunable struct {
	Key     string      `json:"key"`
	Label   string      `json:"label"`
	Kind    TunableKind `json:"kind"`
	Default string      `json:"default"`
	Min     string      `json:"min"`
	Max     string      `json:"max"`
	// Memory marks parameters bounded by host memory.
	Memory bool   `json:"memory"`
	Help   string `json:"help"`
}

// ConfigureServiceRequest replaces a service's tuned settings and restarts
// it to apply them. An empty value resets a parameter to its default.
type ConfigureServiceRequest struct {
	Settings map[string]string `json:"settings"`
}

type ServiceList struct {
	Services []Service `json:"services"`
}

type CreateServiceRequest struct {
	Engine  Engine `json:"engine"`
	Version string `json:"version"`
}

// ---- ingress ----

type EdgeSettings struct {
	Enabled   bool   `json:"enabled"`
	Bind      string `json:"bind"`
	HTTPPort  int    `json:"httpPort"`
	HTTPSPort int    `json:"httpsPort"`
	HTTP3     bool   `json:"http3"`
	ACMEEmail string `json:"acmeEmail"`
	ACMEURL   string `json:"acmeUrl"`
	// FrontProxy adds PROXY protocol listeners for a TCP proxy in front of
	// the edge, such as a public VPS forwarding to a homelab over a VPN.
	FrontProxy FrontProxySettings `json:"frontProxy"`
	// HTTPProxies are reverse proxies or CDNs (IP addresses or CIDRs) in
	// front of the edge's own ports whose X-Forwarded-For and
	// X-Forwarded-Proto headers are believed.
	HTTPProxies []string `json:"httpProxies"`
}

// FrontProxySettings are edge listeners that expect a PROXY protocol header.
// The client address in it is trusted only from TrustedProxies (IP addresses
// or CIDRs); visitors on this path are treated as using ports 80/443.
type FrontProxySettings struct {
	Enabled        bool     `json:"enabled"`
	Bind           string   `json:"bind"`
	HTTPPort       int      `json:"httpPort"`
	HTTPSPort      int      `json:"httpsPort"`
	TrustedProxies []string `json:"trustedProxies"`
}

type EdgeStatus struct {
	Settings EdgeSettings  `json:"settings"`
	State    ObservedState `json:"state"`
	Routes   []string      `json:"routes"`
}

// EdgeMetrics is a snapshot of the edge's nginx stub_status counters.
// Rates are computed against the previous sample taken by the backend and are
// zero on the first sample or after an edge restart.
type EdgeMetrics struct {
	SampledAt         string  `json:"sampledAt"`
	Active            int64   `json:"active"`
	Reading           int64   `json:"reading"`
	Writing           int64   `json:"writing"`
	Waiting           int64   `json:"waiting"`
	Accepts           int64   `json:"accepts"`
	Handled           int64   `json:"handled"`
	Requests          int64   `json:"requests"`
	Dropped           int64   `json:"dropped"`
	RequestsPerSecond float64 `json:"requestsPerSecond"`
	AcceptsPerSecond  float64 `json:"acceptsPerSecond"`
}

type TunnelStatus struct {
	Enabled         bool          `json:"enabled"`
	TokenGeneration int           `json:"tokenGeneration"`
	State           ObservedState `json:"state"`
	Note            string        `json:"note"`
}

// SetTunnelTokenRequest replaces the tunnel token; an empty token disables
// the tunnel. The token is never returned by any endpoint.
type SetTunnelTokenRequest struct {
	Token string `json:"token"`
}

type HostTargetKind string

const (
	HostTargetKindApp      HostTargetKind = "app"
	HostTargetKindUpstream HostTargetKind = "upstream"
	HostTargetKindRedirect HostTargetKind = "redirect"
)

// HostTarget is where an Ingress host sends requests: an app (by slug), one or
// more load-balanced upstream URLs, or another host name (permanent redirect).
type HostTarget struct {
	Kind       HostTargetKind `json:"kind"`
	App        string         `json:"app,omitempty"`
	Upstreams  []string       `json:"upstreams,omitempty"`
	RedirectTo string         `json:"redirectTo,omitempty"`
}

// Host is one Ingress host name with its target and edge settings.
type Host struct {
	Name    string     `json:"name"`
	Target  HostTarget `json:"target"`
	Route   Route      `json:"route"`
	Enabled bool       `json:"enabled"`
	// Live reports whether the running edge generation serves this host.
	Live bool `json:"live"`
	// Certificate is set for a live ACME host.
	Certificate *HostCertificate `json:"certificate,omitempty"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
}

type HostCertificateState string

const (
	// HostCertificateStateIssued: a valid certificate is served.
	HostCertificateStateIssued HostCertificateState = "issued"
	// HostCertificateStateRequesting: the edge is asking the CA now.
	HostCertificateStateRequesting HostCertificateState = "requesting"
	// HostCertificateStateWaiting: the last attempt got no certificate; the
	// boot certificate is served until RetryAt (saving the host retries now).
	HostCertificateStateWaiting HostCertificateState = "waiting"
)

// HostCertificate is the ACME certificate state of a host.
type HostCertificate struct {
	State     HostCertificateState `json:"state"`
	ExpiresAt string               `json:"expiresAt,omitempty"`
	RetryAt   string               `json:"retryAt,omitempty"`
	// Failures counts consecutive attempts that got no certificate.
	Failures int `json:"failures"`
}

type HostList struct {
	Hosts []Host `json:"hosts"`
}

type HostTrafficState string

const (
	// HostTrafficStateOff: the host's access log is off and it has no report.
	HostTrafficStateOff HostTrafficState = "off"
	// HostTrafficStateWaiting: the access log is on; the first report is
	// written at the next hourly run.
	HostTrafficStateWaiting HostTrafficState = "waiting"
	// HostTrafficStateReady: a report exists (while the access log is off it
	// gets no new data; days older than KeepDays still drop off).
	HostTrafficStateReady HostTrafficState = "ready"
)

// HostTraffic is a host's traffic report: its edge access log, page requests
// only (no static files, dotfiles or 404s, no query strings), aggregated
// hourly over the last KeepDays days.
type HostTraffic struct {
	Host            string           `json:"host"`
	AccessLog       bool             `json:"accessLog"`
	State           HostTrafficState `json:"state"`
	KeepDays        int              `json:"keepDays"`
	IntervalMinutes int              `json:"intervalMinutes"`
	// GeneratedAt is when the report was written; LastRunAt and LastError
	// describe the last run since the backend started.
	GeneratedAt string `json:"generatedAt,omitempty"`
	LastRunAt   string `json:"lastRunAt,omitempty"`
	LastError   string `json:"lastError,omitempty"`
	// PendingBytes is logged traffic not yet in the report.
	PendingBytes int64 `json:"pendingBytes"`
	// From and To are the first and last day with data (YYYY-MM-DD).
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Requests int64  `json:"requests"`
	Visitors int64  `json:"visitors"`
	Bytes    int64  `json:"bytes"`
	// Invalid counts log lines that could not be read.
	Invalid int64 `json:"invalid"`
	// Days are oldest first; Hours "00"–"23" (edge local time).
	Days  []TrafficCount `json:"days"`
	Hours []TrafficCount `json:"hours"`
	// Statuses are classes ("2xx"), Codes single codes ("502"). These and
	// the lists below are ordered by requests, most first, at most 50 each.
	Statuses  []TrafficCount `json:"statuses"`
	Codes     []TrafficCount `json:"codes"`
	Paths     []TrafficPath  `json:"paths"`
	Clients   []TrafficCount `json:"clients"`
	Agents    []TrafficCount `json:"agents"`
	Referrers []TrafficCount `json:"referrers"`
}

type TrafficCount struct {
	Label    string `json:"label"`
	Requests int64  `json:"requests"`
	Visitors int64  `json:"visitors"`
	Bytes    int64  `json:"bytes"`
}

// TrafficPath is one request path (without query string). Times are
// milliseconds.
type TrafficPath struct {
	Method   string  `json:"method"`
	Path     string  `json:"path"`
	Requests int64   `json:"requests"`
	Visitors int64   `json:"visitors"`
	Bytes    int64   `json:"bytes"`
	AvgMs    float64 `json:"avgMs"`
	MaxMs    float64 `json:"maxMs"`
}

// HostRequest creates (POST) or replaces (PUT) an Ingress host. On PUT the
// name must match the path.
type HostRequest struct {
	Name    string     `json:"name"`
	Target  HostTarget `json:"target"`
	Route   *Route     `json:"route,omitempty"`
	Enabled bool       `json:"enabled"`
}

// ---- retained data ----

type RetainedRelational struct {
	Engine    Engine   `json:"engine"`
	Service   string   `json:"service"`
	Username  string   `json:"username"`
	Databases []string `json:"databases"`
}

type RetiredApp struct {
	AppID         string               `json:"appId"`
	Slug          string               `json:"slug"`
	UID           int                  `json:"uid"`
	RetiredAt     string               `json:"retiredAt"`
	PrunedAt      string               `json:"prunedAt,omitempty"`
	Home          string               `json:"home"`
	SQLiteFileIDs []string             `json:"sqliteFileIds"`
	Relational    []RetainedRelational `json:"relational"`
}

type RetiredList struct {
	Retired []RetiredApp `json:"retired"`
}

// ---- backups ----

// RcloneRemote is a configured rclone remote. Only its name and backend type
// are exposed; every other key may be a credential.
type RcloneRemote struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// RcloneStatus summarizes the stack's rclone.conf, which is edited in the
// rclone shell. Encrypted configs cannot be used for unattended uploads.
type RcloneStatus struct {
	Present   bool           `json:"present"`
	Encrypted bool           `json:"encrypted"`
	Remotes   []RcloneRemote `json:"remotes"`
	Error     string         `json:"error,omitempty"`
}

// RcloneTestRequest lists a remote without changing it.
type RcloneTestRequest struct {
	Remote string `json:"remote"`
}

type ExportRequest struct {
	Destination string `json:"destination"`
	Confirm     string `json:"confirm"`
}

// ---- system ----

type Catalog struct {
	PHPVersions      []string            `json:"phpVersions"`
	Toolchains       map[string][]string `json:"toolchains"`
	MySQLVersions    []string            `json:"mysqlVersions"`
	PostgresVersions []string            `json:"postgresVersions"`
	PHPModes         []PHPMode           `json:"phpModes"`
	PHPSizing        PHPSizing           `json:"phpSizing"`
}

// PHPMode is one PHP performance mode and its defaults.
type PHPMode struct {
	Name                string `json:"name"`
	WorkerMultiplier    int    `json:"workerMultiplier"`
	WebMemoryLimitMB    int    `json:"webMemoryLimitMb"`
	CLIMemoryLimitMB    int    `json:"cliMemoryLimitMb"`
	MaxExecutionSeconds int    `json:"maxExecutionSeconds"`
	MaxInputVars        int    `json:"maxInputVars"`
}

// PHPSizing is the automatic worker formula:
// clamp(max(memoryMb*webSharePercent/100/workerMemoryMb, minWorkers) *
// workerMultiplier, 1, min(pids/2, maxWorkers)).
type PHPSizing struct {
	WebSharePercent int `json:"webSharePercent"`
	WorkerMemoryMB  int `json:"workerMemoryMb"`
	MinWorkers      int `json:"minWorkers"`
	MaxWorkers      int `json:"maxWorkers"`
}

type SystemStatus struct {
	StackID       string `json:"stackId"`
	StackName     string `json:"stackName"`
	Root          string `json:"root"`
	Version       string `json:"version"`
	StartedAt     string `json:"startedAt"`
	DockerVersion string `json:"dockerVersion"`
	DockerAPI     string `json:"dockerApi"`
	Arch          string `json:"arch"`
	DockerError   string `json:"dockerError,omitempty"`
	Apps          int    `json:"apps"`
	RunningApps   int    `json:"runningApps"`
	QueuedOps     int    `json:"queuedOps"`
	// DiskFreeBytes and DiskTotalBytes describe the filesystem of the stack
	// root, which holds bento.db and every app home (0 when unreadable).
	DiskFreeBytes  int64 `json:"diskFreeBytes"`
	DiskTotalBytes int64 `json:"diskTotalBytes"`
	// DiskLow is true below 2 GiB or 5% free: app homes have no quota, so an
	// app filling the disk also stops Bento from recording state.
	DiskLow bool `json:"diskLow"`
}

// DockerOwnership attributes a Docker resource to this or another Bento stack.
// Resources unrelated to Bento are never returned.
type DockerOwnership string

const (
	DockerOwnershipStack      DockerOwnership = "stack"
	DockerOwnershipOtherStack DockerOwnership = "other-stack"
)

type DockerImage struct {
	ID        string          `json:"id"`
	Tags      []string        `json:"tags"`
	SizeBytes int64           `json:"sizeBytes"`
	CreatedAt string          `json:"createdAt"`
	Built     bool            `json:"built"`
	Ownership DockerOwnership `json:"ownership"`
	UsedBy    []string        `json:"usedBy"`
	// Prunable: Bento-built and referenced by no container.
	Prunable bool `json:"prunable"`
	// AutoRemoveAt is when Bento removes this superseded runtime build on its
	// own (empty: it doesn't).
	AutoRemoveAt string `json:"autoRemoveAt,omitempty"`
}

type DockerVolume struct {
	Name      string          `json:"name"`
	Service   string          `json:"service,omitempty"`
	Ownership DockerOwnership `json:"ownership"`
	UsedBy    []string        `json:"usedBy"`
}

type DockerNetwork struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Driver    string          `json:"driver"`
	Internal  bool            `json:"internal"`
	Subnets   []string        `json:"subnets"`
	Ownership DockerOwnership `json:"ownership"`
	UsedBy    []string        `json:"usedBy"`
}

type DockerInventory struct {
	Images   []DockerImage   `json:"images"`
	Volumes  []DockerVolume  `json:"volumes"`
	Networks []DockerNetwork `json:"networks"`
}

// RuntimeBuild is one build of a managed runtime image. Manifest holds the
// tool versions it resolved and, under "base:<ref>", the image id each
// floating base resolved to.
type RuntimeBuild struct {
	ID       int64             `json:"id"`
	Tag      string            `json:"tag"`
	ImageID  string            `json:"imageId"`
	BuiltAt  string            `json:"builtAt"`
	Current  bool              `json:"current"`
	Manifest map[string]string `json:"manifest"`
}

// RuntimeImageApp is one provisioned app of a runtime.
type RuntimeImageApp struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	DesiredRuntime DesiredRuntime `json:"desiredRuntime"`
	// BuildID is the build the app runs or will run (0: none built yet).
	BuildID int64 `json:"buildId"`
	// Behind: a newer current build is available to roll out.
	Behind bool `json:"behind"`
	// Rolling: a rollout assigned the build; the instance is not yet
	// confirmed ready on it.
	Rolling bool `json:"rolling"`
	// RollbackBuildID is the earlier build the app can go back to (0: none).
	RollbackBuildID int64 `json:"rollbackBuildId"`
	// Changes lists "name from -> to" for rolling out the current build.
	Changes []string `json:"changes"`
}

// RuntimeImage is one runtime (php-8.4, node-24) and its builds.
type RuntimeImage struct {
	Key       string            `json:"key"`
	Kind      RuntimeKind       `json:"kind"`
	Toolchain string            `json:"toolchain"`
	Version   string            `json:"version"`
	Current   *RuntimeBuild     `json:"current,omitempty"`
	Builds    []RuntimeBuild    `json:"builds"`
	Apps      []RuntimeImageApp `json:"apps"`
}

type RuntimeImageList struct {
	Images []RuntimeImage `json:"images"`
}

// RolloutRequest moves apps of a runtime to one of its builds. No buildId is
// the current build; no appIds is every app on another build.
type RolloutRequest struct {
	BuildID int64    `json:"buildId,omitempty"`
	AppIDs  []string `json:"appIds,omitempty"`
}

type Session struct {
	Authenticated bool   `json:"authenticated"`
	CSRFToken     string `json:"csrfToken,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
}

type LoginRequest struct {
	Password string `json:"password"`
}

type SetPasswordRequest struct {
	Password string `json:"password"`
	// CurrentPassword is required from a browser session; root on the host
	// (the control socket) sets the password without it to recover access.
	CurrentPassword string `json:"currentPassword,omitempty"`
}

// ---- API tokens ----

// TokenScope bounds what an API token or a scoped control-socket client may
// do: read < operate < admin. Routes outside every scope (deleting, restoring,
// exec, credentials) need a browser session or the unscoped CLI.
type TokenScope string

const (
	TokenScopeRead    TokenScope = "read"
	TokenScopeOperate TokenScope = "operate"
	TokenScopeAdmin   TokenScope = "admin"
)

// APIToken describes a bearer token. The secret is never returned after
// creation.
type APIToken struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scope     TokenScope `json:"scope"`
	CreatedAt string     `json:"createdAt"`
	// ExpiresAt is empty for a token that never expires.
	ExpiresAt  string `json:"expiresAt,omitempty"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
}

type APITokenList struct {
	Tokens []APIToken `json:"tokens"`
}

type CreateAPITokenRequest struct {
	Name  string     `json:"name"`
	Scope TokenScope `json:"scope"`
	// ExpiresInDays is 1-365; 0 means the token never expires.
	ExpiresInDays int `json:"expiresInDays,omitempty"`
	// CurrentPassword is required from a browser session, so a script that
	// runs on the management origin cannot mint a lasting credential.
	CurrentPassword string `json:"currentPassword,omitempty"`
}

// CreatedAPIToken carries the secret once; only its hash is stored.
type CreatedAPIToken struct {
	APIToken `tstype:",extends"`
	Token    string `json:"token"`
}

type PermissionIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type ExecRequest struct {
	Argv    []string `json:"argv"`
	Workdir string   `json:"workdir,omitempty"`
	// Running selects exec into the running instance instead of a scoped
	// ephemeral tooling container.
	Running bool `json:"running"`
}

type ExecResult struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

type SchedulerCommandRequest struct {
	Argv []string `json:"argv"`
}

// ---- app scheduler (minicrond), as API tokens and MCP see it ----

type SchedulerJobKind string

const (
	SchedulerJobKindJob    SchedulerJobKind = "job"
	SchedulerJobKindWorker SchedulerJobKind = "worker"
)

// SchedulerJob is one minicrond job or worker. Env values are never
// included, only their keys; app secrets in the command are redacted.
type SchedulerJob struct {
	Name string           `json:"name"`
	Kind SchedulerJobKind `json:"kind"`
	// ReadOnly marks Bento's own config-owned tasks (zz-bento-*).
	ReadOnly   bool                  `json:"readOnly"`
	Enabled    bool                  `json:"enabled"`
	Command    string                `json:"command,omitempty"`
	Argv       []string              `json:"argv,omitempty"`
	Schedule   string                `json:"schedule,omitempty"`
	Timezone   string                `json:"timezone,omitempty"`
	WorkingDir string                `json:"workingDir,omitempty"`
	EnvKeys    []string              `json:"envKeys"`
	NextFireAt string                `json:"nextFireAt,omitempty"`
	Worker     *SchedulerWorkerState `json:"worker,omitempty"`
}

type SchedulerWorkerState struct {
	Status    string `json:"status"`
	Active    bool   `json:"active"`
	Held      bool   `json:"held"`
	Failures  int    `json:"failures"`
	Exhausted bool   `json:"exhausted"`
}

type SchedulerJobList struct {
	Jobs []SchedulerJob `json:"jobs"`
}

type SchedulerRun struct {
	ID           string `json:"id"`
	Job          string `json:"job"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	EndReason    string `json:"endReason,omitempty"`
	Trigger      string `json:"trigger"`
	Attempt      int    `json:"attempt"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	Signal       string `json:"signal,omitempty"`
	QueuedAt     string `json:"queuedAt"`
	StartedAt    string `json:"startedAt,omitempty"`
	EndedAt      string `json:"endedAt,omitempty"`
	LogBytes     int64  `json:"logBytes"`
	LogTruncated bool   `json:"logTruncated"`
}

type SchedulerRunList struct {
	Runs []SchedulerRun `json:"runs"`
	// NextBefore pages to older runs (pass as before); empty on the last page.
	NextBefore string `json:"nextBefore,omitempty"`
}

type SchedulerLogLine struct {
	Seq    uint64 `json:"seq"`
	Stderr bool   `json:"stderr,omitempty"`
	Line   string `json:"line"`
}

// SchedulerRunLog is one page of a run's output, oldest first. Pass Next as
// after to read on; More reports that the page was full.
type SchedulerRunLog struct {
	RunID string             `json:"runId"`
	Lines []SchedulerLogLine `json:"lines"`
	Next  uint64             `json:"next"`
	More  bool               `json:"more"`
}

// SchedulerJobRequest creates a job or worker (POST: name, kind and command
// or argv required) or edits one (PATCH: only the fields sent change).
// Setting command clears argv and the reverse. Env values are set or removed
// by key and never read back; keys not mentioned keep their values. Fields
// minicrond has that are not listed here are kept unchanged on edit.
type SchedulerJobRequest struct {
	Name       string            `json:"name,omitempty"`
	Kind       SchedulerJobKind  `json:"kind,omitempty"`
	Command    *string           `json:"command,omitempty"`
	Argv       []string          `json:"argv,omitempty"`
	Schedule   *string           `json:"schedule,omitempty"`
	Timezone   *string           `json:"timezone,omitempty"`
	WorkingDir *string           `json:"workingDir,omitempty"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Timeout    *int              `json:"timeout,omitempty"`
	Retries    *int              `json:"retries,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	RemoveEnv  []string          `json:"removeEnv,omitempty"`
}

type SchedulerEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// SchedulerResult reports a worker or run action minicrond accepted.
type SchedulerResult struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// ---- app code files (~/app only; hidden paths refused) ----

type AppFileType string

const (
	AppFileTypeFile    AppFileType = "file"
	AppFileTypeDir     AppFileType = "dir"
	AppFileTypeSymlink AppFileType = "symlink"
	AppFileTypeOther   AppFileType = "other"
)

type AppFileEntry struct {
	Name       string      `json:"name"`
	Type       AppFileType `json:"type"`
	Size       int64       `json:"size"`
	ModifiedAt string      `json:"modifiedAt"`
}

// AppFileList is one directory of the app code directory. Path is relative
// to ~/app ("" is ~/app itself).
type AppFileList struct {
	Path      string         `json:"path"`
	Entries   []AppFileEntry `json:"entries"`
	Truncated bool           `json:"truncated"`
}

// AppFileContent is a UTF-8 window of a text file, with the app's secrets
// redacted. Sha256 is of the whole file on disk; pass it to an edit. Read on
// from offset+length while eof is false.
type AppFileContent struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Sha256  string `json:"sha256"`
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
	EOF     bool   `json:"eof"`
	Content string `json:"content"`
}

// AppFileWriteRequest replaces a file's content, or creates the file when
// expectedSha256 is empty. A file that changed since it was read is refused.
type AppFileWriteRequest struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSha256 string `json:"expectedSha256,omitempty"`
}

// AppFileReplaceRequest replaces exactly one occurrence of oldText (or every
// one with all) on the real file content, so secrets shown as [redacted]
// elsewhere in the file stay intact.
type AppFileReplaceRequest struct {
	Path           string `json:"path"`
	OldText        string `json:"oldText"`
	NewText        string `json:"newText"`
	All            bool   `json:"all,omitempty"`
	ExpectedSha256 string `json:"expectedSha256,omitempty"`
}

type AppFileWritten struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Sha256       string `json:"sha256"`
	Created      bool   `json:"created"`
	Replacements int    `json:"replacements,omitempty"`
}

// ---- monitoring ----

type AppProcess struct {
	PID        string  `json:"pid"`
	PPID       string  `json:"ppid"`
	User       string  `json:"user"`
	CPUPercent float64 `json:"cpuPercent"`
	MemPercent float64 `json:"memPercent"`
	RSSBytes   int64   `json:"rssBytes"`
	Elapsed    string  `json:"elapsed"`
	Command    string  `json:"command"`
}

// AppMetrics is a point-in-time resource sample of the persistent instance.
// Running is false (and the rest zero) when no instance is running.
type AppMetrics struct {
	Running      bool         `json:"running"`
	SampledAt    string       `json:"sampledAt"`
	CPUPercent   float64      `json:"cpuPercent"`
	OnlineCPUs   int          `json:"onlineCpus"`
	MemoryBytes  int64        `json:"memoryBytes"`
	MemoryLimit  int64        `json:"memoryLimit"`
	NetworkRx    int64        `json:"networkRx"`
	NetworkTx    int64        `json:"networkTx"`
	BlockRead    int64        `json:"blockRead"`
	BlockWrite   int64        `json:"blockWrite"`
	PIDs         int          `json:"pids"`
	Processes    []AppProcess `json:"processes"`
	ProcessTotal int          `json:"processTotal"`
}

// ---- app restic backups ----

type ResticRetention struct {
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

type ResticSchedule struct {
	Enabled bool   `json:"enabled"`
	Cron    string `json:"cron"`
}

// ResticSettings is an app's restic configuration. Paths, excludes and
// sqlitePaths are relative to the app home.
type ResticSettings struct {
	// Repository is an rclone "remote:path".
	Repository      string          `json:"repository"`
	Paths           []string        `json:"paths"`
	Excludes        []string        `json:"excludes"`
	DefaultExcludes bool            `json:"defaultExcludes"`
	SQLitePaths     []string        `json:"sqlitePaths"`
	Retention       ResticRetention `json:"retention"`
	Schedule        ResticSchedule  `json:"schedule"`
	// IncludeSecrets stores env values and database passwords in snapshots
	// taken after the change.
	IncludeSecrets bool `json:"includeSecrets"`
}

// ResticSettingsRequest saves an app's backup settings. Changing the
// repository of an initialized app needs Confirm "disconnect <slug>".
type ResticSettingsRequest struct {
	ResticSettings `tstype:",extends"`
	Confirm        string `json:"confirm,omitempty"`
}

type ResticSnapshot struct {
	ID      string   `json:"id"`
	ShortID string   `json:"shortId"`
	Time    string   `json:"time"`
	Tags    []string `json:"tags"`
}

type ResticKey struct {
	ID       string `json:"id"`
	Current  bool   `json:"current"`
	UserName string `json:"userName"`
	Created  string `json:"created"`
}

type ResticRunResult struct {
	OpID    string `json:"opId"`
	Trigger string `json:"trigger"`
	At      string `json:"at"`
	OK      bool   `json:"ok"`
	// Partial is true when restic exited 3: a snapshot exists but some files
	// were unreadable.
	Partial    bool    `json:"partial"`
	SnapshotID string  `json:"snapshotId"`
	BytesAdded int64   `json:"bytesAdded"`
	FilesNew   int64   `json:"filesNew"`
	FilesTotal int64   `json:"filesTotal"`
	Seconds    float64 `json:"seconds"`
	Error      string  `json:"error"`
}

type Restic struct {
	Configured  bool             `json:"configured"`
	Initialized bool             `json:"initialized"`
	Settings    ResticSettings   `json:"settings"`
	Snapshots   []ResticSnapshot `json:"snapshots"`
	Keys        []ResticKey      `json:"keys"`
	RefreshedAt string           `json:"refreshedAt"`
	LastBackup  *ResticRunResult `json:"lastBackup"`
	LastCheck   *ResticRunResult `json:"lastCheck"`
	LastPruneAt string           `json:"lastPruneAt"`
	NextRun     string           `json:"nextRun"`
	// DefaultExcludes are applied when settings.defaultExcludes is on.
	DefaultExcludes []string `json:"defaultExcludes"`
}

// ResticConnectRequest adopts an existing repository. An empty Key means
// the stack backup password.
type ResticConnectRequest struct {
	Key string `json:"key"`
}

// ResticRestoreDBRequest replaces one database of an app with its dump in one
// of the app's snapshots (operation kind restic.restore-db). Snapshot is a
// full or short snapshot id, or "latest". Database is a relational database
// name or a SQLite file id of the app. Binding is the binding id; it is
// required when the name is in more than one binding (a MySQL and a
// PostgreSQL binding both have <slug>_main). Confirm must be exactly
// "replace <database>".
type ResticRestoreDBRequest struct {
	Snapshot string `json:"snapshot"`
	Database string `json:"database"`
	Binding  string `json:"binding,omitempty"`
	Confirm  string `json:"confirm"`
}

type ResticKeyAddRequest struct {
	Label   string `json:"label"`
	Confirm string `json:"confirm"`
}

// ResticKeyAccepted carries an added access key exactly once, in the
// response that created it.
type ResticKeyAccepted struct {
	Accepted `tstype:",extends"`
	Key      string `json:"key"`
}

// ResticInspectRequest previews a snapshot (operation kind restic.inspect).
// With a slug the preview shows the real target names. Snapshot is a full or
// short snapshot id, or "latest" for the app's newest snapshot.
type ResticInspectRequest struct {
	Snapshot     string `json:"snapshot"`
	Slug         string `json:"slug"`
	KeepUsername bool   `json:"keepUsername"`
}

// ResticCloneRequest restores a snapshot into a new app on this stack. The
// confirmation is exactly "clone <slug>". Snapshot is a full or short
// snapshot id, or "latest" for the app's newest snapshot.
type ResticCloneRequest struct {
	Snapshot     string `json:"snapshot"`
	Slug         string `json:"slug"`
	KeepUsername bool   `json:"keepUsername"`
	// BackupAfter is "none" (default) or "new-repo".
	BackupAfter string `json:"backupAfter"`
	Confirm     string `json:"confirm"`
}

// RestoreFromBackupInspectRequest previews a snapshot of an app backup made
// on another stack (operation kind restic.inspect-remote). Repository is an
// rclone remote path (name:path) and Key one of the repository's keys (empty:
// this stack's backup password); it is kept in a pending file for the
// operation and deleted at its end. An empty
// snapshot or "latest" means the newest; the result lists all snapshots.
type RestoreFromBackupInspectRequest struct {
	Repository   string `json:"repository"`
	Key          string `json:"key"`
	Snapshot     string `json:"snapshot"`
	Slug         string `json:"slug"`
	KeepUsername bool   `json:"keepUsername"`
}

// RestoreFromBackupRequest clones a snapshot of another stack's app backup
// into a new app on this stack (operation kind app.restore-from-backup). The
// confirmation is exactly "clone <slug>".
type RestoreFromBackupRequest struct {
	Repository   string `json:"repository"`
	Key          string `json:"key"`
	Snapshot     string `json:"snapshot"`
	Slug         string `json:"slug"`
	KeepUsername bool   `json:"keepUsername"`
	// BackupAfter is "none" (default), "new-repo", or "same-repo" (keep
	// backing up to this repository with this key).
	BackupAfter string `json:"backupAfter"`
	Confirm     string `json:"confirm"`
}

// The types below describe the result of the restic.inspect and
// app.clone-from-backup operations (Operation.result). They never carry
// secret values.

type ResticCloneDatabase struct {
	Engine         Engine `json:"engine"`
	Service        string `json:"service"`
	Version        string `json:"version"`
	Source         string `json:"source"`
	Target         string `json:"target"`
	SourceUsername string `json:"sourceUsername"`
	Username       string `json:"username"`
	UsernameKept   bool   `json:"usernameKept"`
	UsernameNote   string `json:"usernameNote"`
	PasswordKept   bool   `json:"passwordKept"`
}

type ResticCloneSQLite struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type ResticCloneDomain struct {
	Name  string `json:"name"`
	InUse bool   `json:"inUse"`
}

// ResticClonePreview is the result of restic.inspect.
type ResticClonePreview struct {
	Snapshot       string                `json:"snapshot"`
	SnapshotTime   string                `json:"snapshotTime"`
	FormatVersion  int                   `json:"formatVersion"`
	SizeBytes      int64                 `json:"sizeBytes"`
	SourceSlug     string                `json:"sourceSlug"`
	SourceAppID    string                `json:"sourceAppId"`
	StackID        string                `json:"stackId"`
	Slug           string                `json:"slug"`
	Secrets        bool                  `json:"secrets"`
	RuntimeKind    string                `json:"runtimeKind"`
	RuntimeVersion string                `json:"runtimeVersion"`
	Resources      Resources             `json:"resources"`
	EnvKeys        []string              `json:"envKeys"`
	EmptyEnv       []string              `json:"emptyEnv"`
	HomePath       string                `json:"homePath"`
	Databases      []ResticCloneDatabase `json:"databases"`
	SQLite         []ResticCloneSQLite   `json:"sqlite"`
	Minicron       bool                  `json:"minicron"`
	Domains        []ResticCloneDomain   `json:"domains"`
	Git            bool                  `json:"git"`
	Notes          []string              `json:"notes"`
	Blockers       []string              `json:"blockers"`
	// Snapshots lists a remote repository's snapshots, newest first (empty
	// for a same-stack preview).
	Snapshots []ResticSnapshot `json:"snapshots"`
}

// ResticCloneResult is the result of app.clone-from-backup: the checklist of
// what to check before starting the stopped clone.
type ResticCloneResult struct {
	AppID        string                `json:"appId"`
	Slug         string                `json:"slug"`
	SourceSlug   string                `json:"sourceSlug"`
	SourceAppID  string                `json:"sourceAppId"`
	Snapshot     string                `json:"snapshot"`
	SnapshotTime string                `json:"snapshotTime"`
	Stopped      bool                  `json:"stopped"`
	HomePath     string                `json:"homePath"`
	Databases    []ResticCloneDatabase `json:"databases"`
	SQLite       []ResticCloneSQLite   `json:"sqlite"`
	EmptyEnv     []string              `json:"emptyEnv"`
	Minicron     bool                  `json:"minicron"`
	// DeployKey is the new public deploy key to add to the git host.
	DeployKey string              `json:"deployKey"`
	Domains   []ResticCloneDomain `json:"domains"`
	Checklist []string            `json:"checklist"`
}

// AppBackupSummary is one app's restic backup state for the Backups page.
type AppBackupSummary struct {
	AppID           string           `json:"appId"`
	Slug            string           `json:"slug"`
	Repository      string           `json:"repository"`
	Initialized     bool             `json:"initialized"`
	ScheduleEnabled bool             `json:"scheduleEnabled"`
	Cron            string           `json:"cron"`
	NextRun         string           `json:"nextRun"`
	Paths           []string         `json:"paths"`
	SnapshotCount   int              `json:"snapshotCount"`
	LastBackup      *ResticRunResult `json:"lastBackup"`
	LastCheck       *ResticRunResult `json:"lastCheck"`
}

// AppBackupRun is one restic backup run of an app, newest first.
type AppBackupRun struct {
	AppID string          `json:"appId"`
	Slug  string          `json:"slug"`
	Run   ResticRunResult `json:"run"`
}

type AppBackupOverview struct {
	Apps     []AppBackupSummary `json:"apps"`
	Runs     []AppBackupRun     `json:"runs"`
	TimeZone string             `json:"timeZone"`
	// StackPassword reports whether the stack backup password exists. It is
	// never sent; `bento backup password` prints it on the host.
	StackPassword bool `json:"stackPassword"`
}
