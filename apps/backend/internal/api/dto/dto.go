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
	AppSummary    `tstype:",extends"`
	GID           int         `json:"gid"`
	Home          string      `json:"home"`
	Runtime       RuntimeSpec `json:"runtime"`
	Route         Route       `json:"route"`
	Domains       []Domain    `json:"domains"`
	Bindings      []Binding   `json:"bindings"`
	RedisPrefix   string      `json:"redisPrefix"`
	RedisUser     string      `json:"redisUser"`
	IngressInfo   IngressInfo `json:"ingressInfo"`
	Reconcile     Reconcile   `json:"reconcile"`
	SchedulerPath string      `json:"schedulerPath"`
	CreatedAt     string      `json:"createdAt"`
	UpdatedAt     string      `json:"updatedAt"`
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

// ---- operations ----

type OperationEvent struct {
	Seq     int    `json:"seq"`
	At      string `json:"at"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type Operation struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	TargetKind   string           `json:"targetKind"`
	TargetID     string           `json:"targetId"`
	State        OperationState   `json:"state"`
	Phase        string           `json:"phase"`
	Origin       string           `json:"origin"`
	ErrorCode    string           `json:"errorCode,omitempty"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
	Guidance     string           `json:"guidance,omitempty"`
	Result       map[string]any   `json:"result,omitempty"`
	CreatedAt    string           `json:"createdAt"`
	StartedAt    string           `json:"startedAt,omitempty"`
	FinishedAt   string           `json:"finishedAt,omitempty"`
	Events       []OperationEvent `json:"events,omitempty"`
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
