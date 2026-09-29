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
	EngineRedis    Engine = "redis"
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
	Version        string `json:"version"`
	DocumentRoot   string `json:"documentRoot"`
	Routing        string `json:"routing"`
	Pool           string `json:"pool"`
	ReadyPath      string `json:"readyPath,omitempty"`
	ReleaseSymlink string `json:"releaseSymlink,omitempty"`
	UploadLimitMB  int    `json:"uploadLimitMb"`
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
}

type Domain struct {
	Name    string `json:"name"`
	Primary bool   `json:"primary"`
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
	ID               string         `json:"id"`
	Slug             string         `json:"slug"`
	UID              int            `json:"uid"`
	Kind             RuntimeKind    `json:"kind"`
	Toolchain        string         `json:"toolchain"`
	Version          string         `json:"version"`
	DesiredRuntime   DesiredRuntime `json:"desiredRuntime"`
	Ingress          IngressMode    `json:"ingress"`
	Publication      Publication    `json:"publication"`
	PrimaryDomain    string         `json:"primaryDomain"`
	Provisioned      bool           `json:"provisioned"`
	ConfigGeneration int            `json:"configGeneration"`
	Observed         Observed       `json:"observed"`
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
	AppSummary  `tstype:",extends"`
	GID         int         `json:"gid"`
	Home        string      `json:"home"`
	Runtime     RuntimeSpec `json:"runtime"`
	Route       Route       `json:"route"`
	Env         []EnvVar    `json:"env"`
	Domains     []Domain    `json:"domains"`
	Bindings    []Binding   `json:"bindings"`
	RedisPrefix string      `json:"redisPrefix"`
	RedisUser   string      `json:"redisUser"`
	IngressInfo IngressInfo `json:"ingressInfo"`
	Reconcile   Reconcile   `json:"reconcile"`
	CreatedAt   string      `json:"createdAt"`
	UpdatedAt   string      `json:"updatedAt"`
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
	Slug      string           `json:"slug"`
	Runtime   RuntimeSpec      `json:"runtime"`
	Resources *Resources       `json:"resources,omitempty"`
	Ingress   IngressMode      `json:"ingress,omitempty"`
	Domains   []string         `json:"domains"`
	Route     *Route           `json:"route,omitempty"`
	Bindings  []BindingRequest `json:"bindings"`
}

type UpdateAppRequest struct {
	ExpectedGeneration int          `json:"expectedGeneration,omitempty"`
	Runtime            *RuntimeSpec `json:"runtime,omitempty"`
	Resources          *Resources   `json:"resources,omitempty"`
	Ingress            *IngressMode `json:"ingress,omitempty"`
	Domains            *[]string    `json:"domains,omitempty"`
	Route              *Route       `json:"route,omitempty"`
	// Env, when present, replaces the app's environment variables.
	Env *[]EnvVar `json:"env,omitempty"`
}

// EnvVar is one operator-defined environment variable exposed to the app
// process. Values are operator configuration and are shown back verbatim.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// BackupDeleteRequest removes one backup artifact; Confirm must be "delete".
type BackupDeleteRequest struct {
	Artifact string `json:"artifact"`
	Confirm  string `json:"confirm"`
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

// SchedulerTicket opens one app's scheduler UI on the utils listener. The
// fields mean the same as in DBAdminTicket.
type SchedulerTicket struct {
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
	// edge forwards webhooks to Bento, else "".
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

type Proxy struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Upstreams []string `json:"upstreams"`
	Domains   []Domain `json:"domains"`
	Route     Route    `json:"route"`
	Enabled   bool     `json:"enabled"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

type ProxyList struct {
	Proxies []Proxy `json:"proxies"`
}

type ProxyRequest struct {
	Name      string   `json:"name"`
	Upstreams []string `json:"upstreams"`
	Domains   []string `json:"domains"`
	Route     *Route   `json:"route,omitempty"`
	Enabled   bool     `json:"enabled"`
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

type BackupRequest struct {
	// Scope is "all", "app", or "binding".
	Scope       string `json:"scope"`
	AppID       string `json:"appId,omitempty"`
	BindingID   string `json:"bindingId,omitempty"`
	Compression string `json:"compression,omitempty"`
	Upload      bool   `json:"upload"`
}

type BackupArtifact struct {
	Path      string `json:"path"`
	AppSlug   string `json:"appSlug"`
	Engine    Engine `json:"engine"`
	Database  string `json:"database"`
	SizeBytes int    `json:"sizeBytes"`
	CreatedAt string `json:"createdAt"`
}

type BackupArtifactList struct {
	Artifacts []BackupArtifact `json:"artifacts"`
}

type BackupRun struct {
	ID          string   `json:"id"`
	Trigger     string   `json:"trigger"`
	State       string   `json:"state"`
	StartedAt   string   `json:"startedAt"`
	FinishedAt  string   `json:"finishedAt,omitempty"`
	Artifacts   []string `json:"artifacts"`
	UploadState string   `json:"uploadState,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type BackupRunList struct {
	Runs []BackupRun `json:"runs"`
}

type BackupSchedule struct {
	Enabled      bool   `json:"enabled"`
	Cron         string `json:"cron"`
	Compression  string `json:"compression"`
	Retain       int    `json:"retain"`
	RcloneRemote string `json:"rcloneRemote"`
	NextRun      string `json:"nextRun,omitempty"`
	LastRun      string `json:"lastRun,omitempty"`
	LastState    string `json:"lastState,omitempty"`
	// TimeZone is the server's zone that cron fields are read in, for
	// example "UTC+07:00". Ignored on write.
	TimeZone string `json:"timeZone,omitempty"`
}

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

// RcloneTestRequest lists a remote without changing it; an empty remote
// tests the schedule's remote.
type RcloneTestRequest struct {
	Remote string `json:"remote"`
}

// RestoreRequest replaces an app database from an artifact. Confirm must be
// exactly "replace <database>".
type RestoreRequest struct {
	Artifact string `json:"artifact"`
	AppID    string `json:"appId"`
	Database string `json:"database"`
	Confirm  string `json:"confirm"`
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
	PoolProfiles     []string            `json:"poolProfiles"`
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
