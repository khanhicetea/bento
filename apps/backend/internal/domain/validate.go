package domain

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// FieldError is a validation failure tied to a request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// ValidationErrors aggregates field failures.
type ValidationErrors []FieldError

func (v ValidationErrors) Error() string {
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

func (v *ValidationErrors) Add(field, format string, args ...any) {
	*v = append(*v, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

func (v ValidationErrors) Err() error {
	if len(v) == 0 {
		return nil
	}
	return v
}

var (
	slugPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	domainLabel     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	dbNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	dbSuffixPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,29}$`)
	serviceName     = regexp.MustCompile(`^(mysql|postgres)[0-9]{1,3}$`)
	readyPathRegexp = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/%?-]{0,255}$`)
	certNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

var reservedSlugs = map[string]bool{
	"root": true, "bento": true, "admin": true, "www-data": true, "nginx": true, "edge": true,
	"mysql": true, "postgres": true, "redis": true, "daemon": true, "nobody": true, "system": true,
	"cloudflared": true, "backup": true, "tool": true,
}

func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) || strings.Contains(slug, "--") {
		return errors.New("must be 3-32 lowercase letters, digits, or single hyphens, starting with a letter")
	}
	if reservedSlugs[slug] {
		return fmt.Errorf("%q is reserved", slug)
	}
	return nil
}

// NormalizeDomain lowercases and validates a DNS host name. Wildcards and IP
// literals are not accepted.
func NormalizeDomain(name string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return "", fmt.Errorf("invalid domain %q", name)
	}
	for label := range strings.SplitSeq(d, ".") {
		if !domainLabel.MatchString(label) {
			return "", fmt.Errorf("invalid domain %q", name)
		}
	}
	if _, err := strconv.Atoi(strings.ReplaceAll(d, ".", "")); err == nil {
		return "", fmt.Errorf("IP addresses are not domains: %q", name)
	}
	return d, nil
}

// CleanRelative validates a path relative to an app-controlled base directory.
func CleanRelative(p string) (string, error) {
	if p == "" || p == "." {
		return "", nil
	}
	if strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", errors.New("must be a relative path inside the app code directory")
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", errors.New("must stay inside the app code directory")
	}
	if c == "." {
		return "", nil
	}
	for part := range strings.SplitSeq(c, "/") {
		if strings.HasPrefix(part, ".") {
			return "", errors.New("hidden path components are not allowed")
		}
	}
	return c, nil
}

// PrimaryDatabaseSuffix names a binding's first database: <slug>_main.
const PrimaryDatabaseSuffix = "main"

// ValidateDatabaseSuffix checks the app-local part of a database name. Every
// database is <slug with - as _>_<suffix>; slugs never contain "_" and suffixes
// never do either, so the last "_" splits a name unambiguously and names from
// different apps cannot collide.
func ValidateDatabaseSuffix(suffix string) error {
	if !dbSuffixPattern.MatchString(suffix) {
		return errors.New("must be 1-30 lowercase letters or digits, starting with a letter (no underscores)")
	}
	if suffix == PrimaryDatabaseSuffix {
		return fmt.Errorf("%q is reserved for the primary database", suffix)
	}
	return nil
}

func ValidateDatabaseName(name string) error {
	if !dbNamePattern.MatchString(name) {
		return fmt.Errorf("must match %s", dbNamePattern)
	}
	return nil
}

func ValidateServiceName(name string) error {
	if !serviceName.MatchString(name) {
		return errors.New("must look like mysql84 or postgres17")
	}
	return nil
}

func ValidateCertName(name string) error {
	if !certNamePattern.MatchString(name) {
		return fmt.Errorf("must match %s", certNamePattern)
	}
	return nil
}

var envKeyRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvKeys are set by Bento or the image and cannot be overridden.
var reservedEnvKeys = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true, "LANG": true, "PATH": true, "TZ": true,
	"BASE_PATH": true, "MINICRON_DATA": true, "MINICRON_CONFIG": true,
}

// ValidateEnv checks operator-defined app environment variables.
func ValidateEnv(env []EnvVar, errs *ValidationErrors) {
	if len(env) > 256 {
		errs.Add("env", "at most 256 variables")
	}
	seen := map[string]bool{}
	for i, e := range env {
		field := fmt.Sprintf("env[%d]", i)
		switch {
		case !envKeyRegexp.MatchString(e.Key) || len(e.Key) > 128:
			errs.Add(field, "key %q must match [A-Za-z_][A-Za-z0-9_]* (max 128)", e.Key)
		case strings.HasPrefix(e.Key, "BENTO_") || reservedEnvKeys[e.Key]:
			errs.Add(field, "key %s is reserved", e.Key)
		case seen[e.Key]:
			errs.Add(field, "duplicate key %s", e.Key)
		}
		seen[e.Key] = true
		if len(e.Value) > 8192 || strings.ContainsAny(e.Value, "\x00\r\n") {
			errs.Add(field, "value of %s must be a single line (max 8192 bytes)", e.Key)
		}
	}
}

// ValidateRuntime checks the runtime union and variant-specific constraints.
func ValidateRuntime(r *Runtime, errs *ValidationErrors) {
	switch r.Kind {
	case RuntimePHP:
		if r.HTTP != nil {
			errs.Add("runtime.http", "must be omitted for php-fpm")
		}
		if r.PHP == nil {
			errs.Add("runtime.php", "is required for php-fpm")
			return
		}
		p := r.PHP
		if _, ok := PHPVersions[p.Version]; !ok {
			errs.Add("runtime.php.version", "unsupported; choose one of %s", strings.Join(SortedKeys(PHPVersions), ", "))
		}
		if dr, err := CleanRelative(p.DocumentRoot); err != nil {
			errs.Add("runtime.php.documentRoot", "%s", err)
		} else {
			p.DocumentRoot = dr
		}
		if p.Routing == "" {
			p.Routing = RoutingFrontController
		}
		if p.Routing != RoutingFrontController && p.Routing != RoutingLegacy {
			errs.Add("runtime.php.routing", "must be front-controller or legacy")
		}
		if p.Mode == "" {
			p.Mode = PHPModeStandard
		}
		if _, ok := PHPModes[p.Mode]; !ok {
			errs.Add("runtime.php.mode", "must be one of %s", strings.Join(SortedKeys(PHPModes), ", "))
		}
		optionalRange(errs, "runtime.php.maxWorkers", p.MaxWorkers, 1, PHPMaxWorkers)
		optionalRange(errs, "runtime.php.webMemoryLimitMb", p.WebMemoryLimitMB, 16, 4096)
		optionalRange(errs, "runtime.php.cliMemoryLimitMb", p.CLIMemoryLimitMB, 16, 8192)
		// Must end before the edge proxy_read_timeout (300s).
		optionalRange(errs, "runtime.php.maxExecutionSeconds", p.MaxExecutionSeconds, 1, 280)
		optionalRange(errs, "runtime.php.maxInputVars", p.MaxInputVars, 100, 100000)
		if p.ReadyPath != "" && !readyPathRegexp.MatchString(p.ReadyPath) {
			errs.Add("runtime.php.readyPath", "must be an absolute URL path")
		}
		if p.ReleaseSymlink != "" {
			rs, err := CleanRelative(p.ReleaseSymlink)
			if err != nil || rs == "" || strings.Contains(rs, "/") {
				errs.Add("runtime.php.releaseSymlink", "must be a single path component inside the app code directory")
			}
		}
		if p.UploadLimitMB == 0 {
			p.UploadLimitMB = 64
		}
		if p.UploadLimitMB < 1 || p.UploadLimitMB > 4096 {
			errs.Add("runtime.php.uploadLimitMb", "must be between 1 and 4096")
		}
	case RuntimeHTTP:
		if r.PHP != nil {
			errs.Add("runtime.php", "must be omitted for http-process")
		}
		if r.HTTP == nil {
			errs.Add("runtime.http", "is required for http-process")
			return
		}
		h := r.HTTP
		versions, ok := HTTPToolchains[h.Toolchain]
		if !ok {
			errs.Add("runtime.http.toolchain", "must be one of %s", strings.Join(SortedKeys(HTTPToolchains), ", "))
		} else if _, ok := versions[h.Version]; !ok {
			errs.Add("runtime.http.version", "unsupported; choose one of %s", strings.Join(SortedKeys(versions), ", "))
		}
		if len(h.Argv) == 0 || len(h.Argv) > 64 {
			errs.Add("runtime.http.argv", "must contain 1-64 arguments")
		}
		for i, a := range h.Argv {
			if a == "" && i == 0 {
				errs.Add("runtime.http.argv", "command must not be empty")
			}
			if len(a) > 4096 || strings.ContainsRune(a, 0) {
				errs.Add("runtime.http.argv", "argument %d is invalid", i)
			}
		}
		if wd, err := CleanRelative(h.Workdir); err != nil {
			errs.Add("runtime.http.workdir", "%s", err)
		} else {
			h.Workdir = wd
		}
		if h.Port == 0 {
			h.Port = 3000
		}
		if h.Port < 1024 || h.Port > 65535 {
			errs.Add("runtime.http.port", "must be between 1024 and 65535")
		}
		if h.ReadyPath != "" && !readyPathRegexp.MatchString(h.ReadyPath) {
			errs.Add("runtime.http.readyPath", "must be an absolute URL path")
		}
	default:
		errs.Add("runtime.kind", "must be php-fpm or http-process")
	}
}

func ValidateResources(r *Resources, kind RuntimeKind, errs *ValidationErrors) {
	def := DefaultResources(kind)
	if r.MemoryMB == 0 {
		r.MemoryMB = def.MemoryMB
	}
	if r.CPUMillis == 0 {
		r.CPUMillis = def.CPUMillis
	}
	if r.PIDs == 0 {
		r.PIDs = def.PIDs
	}
	if r.MemoryMB < 64 || r.MemoryMB > 262144 {
		errs.Add("resources.memoryMb", "must be between 64 and 262144")
	}
	if r.CPUMillis < 50 || r.CPUMillis > 256000 {
		errs.Add("resources.cpuMillis", "must be between 50 and 256000")
	}
	if r.PIDs < 32 || r.PIDs > 65536 {
		errs.Add("resources.pids", "must be between 32 and 65536")
	}
}

func ValidateRoute(r *Route, field string, errs *ValidationErrors) {
	switch r.TLS {
	case "":
		r.TLS = TLSNone
	case TLSNone, TLSSelfSigned, TLSACME:
	case TLSExternal:
		if err := ValidateCertName(r.CertName); err != nil {
			errs.Add(field+".certName", "%s", err)
		}
	default:
		errs.Add(field+".tls", "must be none, self-signed, acme, or external")
	}
	if r.TLS != TLSExternal && r.CertName != "" {
		errs.Add(field+".certName", "only valid with external TLS")
	}
	if r.TLS == TLSNone && r.RedirectHTTPS {
		errs.Add(field+".redirectHttps", "requires TLS")
	}
}

// ValidateUpstream checks a reverse-proxy upstream URL.
func ValidateUpstream(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an http(s) URL with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must not include credentials, query, or fragment")
	}
	if strings.ContainsAny(raw, " ;{}'\"\\\n\r\t$") {
		return errors.New("contains characters not allowed in generated configuration")
	}
	return nil
}

func ValidateIngress(mode IngressMode) error {
	switch mode {
	case IngressManaged, IngressExternal, IngressNone:
		return nil
	}
	return errors.New("must be managed, external, or none")
}

// optionalRange checks an optional integer: zero means unset.
func optionalRange(errs *ValidationErrors, field string, v, lo, hi int) {
	if v != 0 && (v < lo || v > hi) {
		errs.Add(field, "must be between %d and %d, or empty for the default", lo, hi)
	}
}
