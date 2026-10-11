package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ServiceSettings are operator-tuned server parameters of a relational data
// service, keyed by ServiceTunable.Key. Values are canonical: a size is a
// positive integer with a K, M or G suffix ("512M"); an int is plain digits.
// Only allowlisted keys are accepted, and the backend renders them itself,
// so nothing an operator types reaches the server command line verbatim.
type ServiceSettings map[string]string

// TunableKind is how a tunable value is written and validated.
type TunableKind string

const (
	TunableSize TunableKind = "size"
	TunableInt  TunableKind = "int"
)

// ServiceTunable is one allowlisted server parameter.
type ServiceTunable struct {
	Key string
	// Label is the short CAPS field label (at most two words).
	Label string
	Kind  TunableKind
	// Min and Max bound the value (bytes for sizes).
	Min, Max int64
	// Memory marks parameters that size RAM; they may not exceed host memory.
	Memory bool
	// Default is the engine's built-in value, shown when unset.
	Default string
	Help    string
}

// ServiceTunables lists the tunable parameters per engine. All of them take
// effect when the service restarts.
var ServiceTunables = map[Engine][]ServiceTunable{
	EngineMySQL: {
		{Key: "innodb_buffer_pool_size", Label: "Buffer pool", Kind: TunableSize, Min: 32 << 20, Max: 1 << 40, Memory: true, Default: "128M",
			Help: "InnoDB data and index cache; the main memory knob"},
		{Key: "innodb_redo_log_capacity", Label: "Redo log", Kind: TunableSize, Min: 8 << 20, Max: 128 << 30, Default: "100M",
			Help: "Redo log disk space; larger smooths heavy writes"},
		{Key: "max_connections", Label: "Connections", Kind: TunableInt, Min: 10, Max: 100000, Default: "151",
			Help: "Concurrent client connections"},
		{Key: "max_allowed_packet", Label: "Max packet", Kind: TunableSize, Min: 1 << 20, Max: 1 << 30, Default: "64M",
			Help: "Largest single query or row"},
		{Key: "tmp_table_size", Label: "Temp tables", Kind: TunableSize, Min: 1 << 20, Max: 1 << 40, Memory: true, Default: "16M",
			Help: "In-memory temporary table limit per query"},
	},
	EnginePostgres: {
		{Key: "shared_buffers", Label: "Shared buffers", Kind: TunableSize, Min: 16 << 20, Max: 1 << 40, Memory: true, Default: "128M",
			Help: "Shared page cache; commonly 25% of memory"},
		{Key: "effective_cache_size", Label: "Cache size", Kind: TunableSize, Min: 16 << 20, Max: 1 << 40, Memory: true, Default: "4G",
			Help: "Planner hint for OS + database cache; allocates nothing"},
		{Key: "work_mem", Label: "Work mem", Kind: TunableSize, Min: 64 << 10, Max: 2 << 30, Memory: true, Default: "4M",
			Help: "Per sort or hash operation, per query"},
		{Key: "maintenance_work_mem", Label: "Maint mem", Kind: TunableSize, Min: 1 << 20, Max: 2 << 30, Memory: true, Default: "64M",
			Help: "VACUUM, CREATE INDEX and restore memory"},
		{Key: "max_connections", Label: "Connections", Kind: TunableInt, Min: 10, Max: 10000, Default: "100",
			Help: "Concurrent client connections"},
	},
}

// Tunable finds an allowlisted parameter of an engine.
func Tunable(e Engine, key string) (ServiceTunable, bool) {
	i := slices.IndexFunc(ServiceTunables[e], func(t ServiceTunable) bool { return t.Key == key })
	if i < 0 {
		return ServiceTunable{}, false
	}
	return ServiceTunables[e][i], true
}

var sizePattern = regexp.MustCompile(`^([1-9][0-9]{0,9})\s*([KMGkmg])(?:i?[Bb])?$`)

// ParseSize reads "512M", "2G", "64k" or "512MB" and returns bytes and the
// canonical form ("512M").
func ParseSize(s string) (int64, string, error) {
	m := sizePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, "", fmt.Errorf("must be a number with a K, M or G unit, like 512M")
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	unit := strings.ToUpper(m[2])
	shift := map[string]uint{"K": 10, "M": 20, "G": 30}[unit]
	return n << shift, m[1] + unit, nil
}

// NormalizeServiceSettings validates operator input for an engine and
// returns canonical settings. Empty values unset a parameter (the engine
// default applies). hostMemory bounds memory parameters when it is known
// (> 0).
func NormalizeServiceSettings(e Engine, in map[string]string, hostMemory int64) (ServiceSettings, error) {
	var errs ValidationErrors
	if len(ServiceTunables[e]) == 0 && len(in) > 0 {
		errs.Add("settings", "%s has no tunable settings", e)
		return nil, errs
	}
	out := ServiceSettings{}
	for key, raw := range in {
		field := "settings." + key
		t, ok := Tunable(e, key)
		if !ok {
			errs.Add(field, "is not a tunable %s setting", e)
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		switch t.Kind {
		case TunableSize:
			n, canon, err := ParseSize(raw)
			if err != nil {
				errs.Add(field, "%v", err)
				continue
			}
			if n < t.Min || n > t.Max {
				errs.Add(field, "must be between %s and %s", FormatSize(t.Min), FormatSize(t.Max))
				continue
			}
			if t.Memory && hostMemory > 0 && n > hostMemory {
				errs.Add(field, "exceeds host memory (%s)", FormatSize(hostMemory))
				continue
			}
			out[key] = canon
		case TunableInt:
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < t.Min || n > t.Max {
				errs.Add(field, "must be a whole number between %d and %d", t.Min, t.Max)
				continue
			}
			out[key] = strconv.FormatInt(n, 10)
		}
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// FormatSize renders bytes in the largest whole K/M/G unit.
func FormatSize(n int64) string {
	switch {
	case n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + "G"
	case n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "M"
	case n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + "K"
	}
	return strconv.FormatInt(n, 10)
}
