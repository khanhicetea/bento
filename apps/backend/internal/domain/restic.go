package domain

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ResticSettings is the operator-owned restic configuration of one app.
// Bookkeeping written by operations lives in ResticState so an operator edit
// never races an operation.
type ResticSettings struct {
	// Repository is the rclone "remote:path" holding this app's repository.
	Repository string `json:"repository"`
	// Paths are relative to the app home; "." is the whole home.
	Paths []string `json:"paths"`
	// Excludes are restic patterns. A pattern with a "/" is anchored at the
	// home; one without matches a name anywhere. A leading "!" negates.
	Excludes        []string `json:"excludes"`
	DefaultExcludes bool     `json:"defaultExcludes"`
	// SQLitePaths are extra SQLite files in the home snapshotted with
	// .backup instead of being copied while live.
	SQLitePaths []string        `json:"sqlitePaths"`
	Retention   ResticRetention `json:"retention"`
	Schedule    ResticSchedule  `json:"schedule"`
	// IncludeSecrets stores unredacted env values and database passwords in
	// the snapshot (secrets.json). Off by default; applies to later snapshots.
	IncludeSecrets bool `json:"includeSecrets"`
}

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

// DefaultResticSettings backs up the whole home daily at 03:30.
func DefaultResticSettings() ResticSettings {
	return ResticSettings{
		Paths:           []string{"."},
		Excludes:        []string{},
		DefaultExcludes: true,
		SQLitePaths:     []string{},
		Retention:       ResticRetention{Hourly: 24, Daily: 7, Weekly: 4, Monthly: 6},
		Schedule:        ResticSchedule{Cron: "30 3 * * *"},
	}
}

// ResticState is what operations learned about the repository.
type ResticState struct {
	RepositoryID  string           `json:"repositoryId,omitempty"`
	InitializedAt time.Time        `json:"initializedAt,omitzero"`
	Snapshots     []ResticSnapshot `json:"snapshots"`
	Keys          []ResticKey      `json:"keys"`
	RefreshedAt   time.Time        `json:"refreshedAt,omitzero"`
	LastBackup    *ResticRunResult `json:"lastBackup,omitempty"`
	// History holds the most recent backup runs, newest first, failures
	// included (at most ResticHistoryLimit).
	History     []ResticRunResult `json:"history,omitempty"`
	LastPruneAt time.Time         `json:"lastPruneAt,omitzero"`
	LastCheck   *ResticRunResult  `json:"lastCheck,omitempty"`
}

type ResticSnapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"shortId"`
	Time     time.Time `json:"time"`
	Tags     []string  `json:"tags"`
	Hostname string    `json:"hostname"`
}

type ResticKey struct {
	ID       string    `json:"id"`
	Current  bool      `json:"current"`
	UserName string    `json:"userName"`
	HostName string    `json:"hostName"`
	Created  time.Time `json:"created"`
}

// ResticHistoryLimit bounds ResticState.History.
const ResticHistoryLimit = 50

type ResticRunResult struct {
	OpID    string    `json:"opId,omitempty"`
	Trigger string    `json:"trigger,omitempty"`
	At      time.Time `json:"at"`
	OK      bool      `json:"ok"`
	// Partial marks a snapshot restic created although some files were
	// unreadable (exit 3). Partial runs are not OK.
	Partial    bool    `json:"partial,omitempty"`
	SnapshotID string  `json:"snapshotId,omitempty"`
	BytesAdded int64   `json:"bytesAdded,omitempty"`
	FilesNew   int64   `json:"filesNew,omitempty"`
	FilesTotal int64   `json:"filesTotal,omitempty"`
	Seconds    float64 `json:"seconds,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// ResticSnapshotID matches a full or short restic snapshot id.
var ResticSnapshotID = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// ResticKeyID matches a restic key id (full or short).
var ResticKeyID = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// ResticDefaultExcludes are always-safe exclusions relative to the home. The
// minicrond data and SQLitePaths are excluded separately because they are
// replaced by consistent .backup copies.
var ResticDefaultExcludes = []string{".cache", ".npm/_cacache", ".composer/cache", ".cache/composer"}

// MinicronDataDir is minicrond's data directory relative to the app home.
const MinicronDataDir = ".local/share/minicron"

// MinicronDBFile is minicrond's jobs, workers and settings database in
// MinicronDataDir. It is the only minicrond file an app backup keeps.
const MinicronDBFile = "minicron.db"

// ResticMinicronExcludes are minicrond files never backed up (relative to
// the home): the log database and its journals, and the control socket.
var ResticMinicronExcludes = []string{
	MinicronDataDir + "/minicron-logs.db",
	MinicronDataDir + "/minicron-logs.db-wal",
	MinicronDataDir + "/minicron-logs.db-shm",
	MinicronDataDir + "/minicron.sock",
}

// HomeSidecarName is the Bento identity record inside each home; it belongs
// to one stack incarnation and is never backed up or restored.
const HomeSidecarName = ".bento-identity.json"

// ValidateResticSettings checks operator input. The repository remote syntax
// and rclone config are checked by the caller.
func ValidateResticSettings(s ResticSettings) ValidationErrors {
	var errs ValidationErrors
	if s.Repository == "" {
		errs.Add("repository", "choose an rclone remote and path, for example b2:bento/apps/shop")
	}
	if len(s.Paths) == 0 {
		errs.Add("paths", "add at least one path (\".\" is the whole home)")
	}
	if len(s.Paths) > 200 {
		errs.Add("paths", "at most 200 paths")
	}
	if len(s.SQLitePaths) > 200 {
		errs.Add("sqlitePaths", "at most 200 files")
	}
	for _, p := range s.Paths {
		if _, err := CleanHomeRel(p, true); err != "" {
			errs.Add("paths", "%q %s", p, err)
		}
	}
	for _, p := range s.SQLitePaths {
		if _, err := CleanHomeRel(p, false); err != "" {
			errs.Add("sqlitePaths", "%q %s", p, err)
		}
	}
	if len(s.Excludes) > 200 {
		errs.Add("excludes", "at most 200 patterns")
	}
	for _, p := range s.Excludes {
		if err := validExcludePattern(p); err != "" {
			errs.Add("excludes", "%q %s", p, err)
		}
	}
	r := s.Retention
	for _, n := range []int{r.Hourly, r.Daily, r.Weekly, r.Monthly} {
		if n < 0 || n > 1000 {
			errs.Add("retention", "each count must be between 0 and 1000")
			break
		}
	}
	if r.Hourly+r.Daily+r.Weekly+r.Monthly == 0 {
		errs.Add("retention", "keep at least one snapshot")
	}
	if s.Schedule.Enabled && strings.TrimSpace(s.Schedule.Cron) == "" {
		errs.Add("schedule", "a cron expression is required when the schedule is enabled")
	}
	return errs
}

// TopmostRels drops every (already cleaned) home-relative path that lies inside
// another one of the list ("app/storage" next to "app"), so restoring moves
// each tree once. "." covers everything.
func TopmostRels(rels []string) []string {
	if slices.Contains(rels, ".") {
		return []string{"."}
	}
	sorted := slices.Clone(rels)
	slices.SortFunc(sorted, func(a, b string) int { return len(a) - len(b) })
	out := []string{}
	for _, p := range sorted {
		covered := slices.ContainsFunc(out, func(q string) bool { return p == q || strings.HasPrefix(p, q+"/") })
		if !covered {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// CleanHomeRel normalizes a home-relative path. It returns a non-empty
// reason when the path is unusable.
func CleanHomeRel(p string, allowRoot bool) (string, string) {
	if p == "" || strings.ContainsAny(p, "\x00\n\r") || len(p) > 1024 {
		return "", "is not a valid path"
	}
	if strings.HasPrefix(p, "/") {
		return "", "must be relative to the app home"
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", "must stay inside the app home"
	}
	if c == "." && !allowRoot {
		return "", "must name a file"
	}
	if c == HomeSidecarName {
		return "", "is managed by Bento"
	}
	return c, ""
}

func validExcludePattern(p string) string {
	body := strings.TrimPrefix(p, "!")
	switch {
	case body == "" || len(p) > 1024 || strings.ContainsAny(p, "\x00\n\r"):
		return "is not a valid pattern"
	case strings.HasPrefix(body, "/"):
		return "must be relative to the app home"
	case body == ".." || strings.HasPrefix(body, "../") || strings.Contains(body, "/../"):
		return "must stay inside the app home"
	}
	return ""
}

var globEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`)

// ResticExcludeLines renders restic exclude-file lines for a home mounted at
// homeMount. Patterns containing "/" are anchored at the home; others match a
// name at any depth, as in restic.
func ResticExcludeLines(s ResticSettings, homeMount string, sqliteFiles []string) []string {
	anchor := func(p string) string {
		neg := ""
		if strings.HasPrefix(p, "!") {
			neg, p = "!", p[1:]
		}
		p = strings.TrimPrefix(p, "./")
		if strings.Contains(p, "/") || slices.Contains(ResticDefaultExcludes, p) {
			return neg + homeMount + "/" + strings.TrimSuffix(p, "/")
		}
		return neg + p
	}
	lines := []string{homeMount + "/" + HomeSidecarName}
	for _, p := range ResticMinicronExcludes {
		lines = append(lines, homeMount+"/"+p)
	}
	if s.DefaultExcludes {
		for _, p := range ResticDefaultExcludes {
			lines = append(lines, homeMount+"/"+p)
		}
	}
	for _, f := range sqliteFiles {
		// File names are literal; restic would read *, ? and [ as globs.
		f = globEscaper.Replace(f)
		for _, sfx := range []string{"", "-wal", "-shm", "-journal"} {
			lines = append(lines, homeMount+"/"+f+sfx)
		}
	}
	for _, p := range s.Excludes {
		lines = append(lines, anchor(p))
	}
	return lines
}

// sensitiveEnvParts mark a key as secret when they appear anywhere in it
// ("DBPASSWORD", "MY_SECRET_X"). They are long enough not to occur by chance.
var sensitiveEnvParts = []string{"PASSWORD", "PASSWD", "PASSPHRASE", "SECRET", "TOKEN", "CREDENTIAL"}

// sensitiveEnvWords mark a key as secret when they are a whole word of it
// (words are separated by "_", "-" or "."), so "PASS" matches "SMTP_PASS"
// but not "COMPASS".
var sensitiveEnvWords = []string{"PASS", "PWD", "PW", "AUTH", "CREDS", "PRIVATE", "SALT", "COOKIE", "COOKIES", "CERT",
	"DSN", "APIKEY"}

// sensitiveEnvLast mark a key as secret when they are its last word: "APP_KEY"
// and "STRIPE_KEYS" but not "CACHE_KEY_PREFIX".
var sensitiveEnvLast = []string{"KEY", "KEYS", "SECRETKEY", "ACCESSKEY", "PRIVATEKEY"}

// urlUserinfoPassword matches a URL that carries a password in its userinfo,
// such as "mysql://user:pw@host/db" or "redis://:pw@host".
var urlUserinfoPassword = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://[^/?#@\s]*:[^/?#@\s]+@`)

// SensitiveEnvKey reports whether an env var's name marks its value as a
// secret.
func SensitiveEnvKey(key string) bool {
	k := strings.ToUpper(key)
	for _, s := range sensitiveEnvParts {
		if strings.Contains(k, s) {
			return true
		}
	}
	words := strings.FieldsFunc(k, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if slices.Contains(sensitiveEnvWords, w) {
			return true
		}
	}
	return slices.Contains(sensitiveEnvLast, words[len(words)-1])
}

// SensitiveEnv reports whether an env var's value is treated as a secret: by
// its name, or because the value is a URL with an embedded password (for
// example DATABASE_URL or REDIS_URL).
func SensitiveEnv(e EnvVar) bool {
	return SensitiveEnvKey(e.Key) || urlUserinfoPassword.MatchString(strings.TrimSpace(e.Value))
}

// RedactedEnvValue replaces sensitive env values in app.json.
const RedactedEnvValue = "[redacted]"
