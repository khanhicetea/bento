package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// Scoped-reload scopes of generated app config.
const (
	ScopeFrontend  = "frontend"
	ScopePool      = "pool"
	ScopeScheduler = "scheduler"
)

// Scopes lists every scoped-reload scope.
var Scopes = []string{ScopeFrontend, ScopePool, ScopeScheduler}

// ScopeFiles names the generated files of one scope.
func ScopeFiles(scope string) []string {
	switch scope {
	case ScopeFrontend:
		return FrontendFiles
	case ScopePool:
		return PoolFiles
	case ScopeScheduler:
		return SchedulerFiles
	}
	return nil
}

// AppliedScope is what the running instance last loaded for one scope: the
// scope hash and the exact file bytes (nil for an absent file), so a failed
// validation can restore precisely what is live.
type AppliedScope struct {
	Hash  string            `json:"hash"`
	Files map[string][]byte `json:"files"`
}

// AppliedConfig is the root-only record of applied generated config. It lives
// outside the mounted config directory so tools and backups that rewrite the
// config never mark anything applied.
type AppliedConfig struct {
	Version int                     `json:"version"`
	Scopes  map[string]AppliedScope `json:"scopes"`
}

const appliedVersion = 1

// scopeHash hashes scope file bytes the same way RenderAppConfig does.
func scopeHash(scope string, files map[string][]byte) string {
	var buf []byte
	for _, n := range ScopeFiles(scope) {
		buf = append(buf, files[n]...)
	}
	return platform.SHA256Hex(buf)
}

// renderedScopeHash picks the Materialized hash for a scope. Non-PHP apps
// have no frontend/pool files; their rendered hash is the hash of nothing,
// matching what scopeHash computes for absent files.
func renderedScopeHash(m Materialized, scope string) string {
	var h string
	switch scope {
	case ScopeFrontend:
		h = m.FrontendHash
	case ScopePool:
		h = m.PoolHash
	case ScopeScheduler:
		h = m.SchedulerHash
	}
	if h == "" {
		return platform.SHA256Hex(nil)
	}
	return h
}

func readApplied(path string) (AppliedConfig, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return AppliedConfig{}, false, nil
	}
	if err != nil {
		return AppliedConfig{}, false, err
	}
	var a AppliedConfig
	if err := json.Unmarshal(data, &a); err != nil {
		return AppliedConfig{}, false, fmt.Errorf("applied config state %s: %w", path, err)
	}
	if a.Version != appliedVersion {
		return AppliedConfig{}, false, fmt.Errorf("applied config state %s: unsupported version %d", path, a.Version)
	}
	if a.Scopes == nil {
		a.Scopes = map[string]AppliedScope{}
	}
	return a, true, nil
}

func writeApplied(stateDir, path string, a AppliedConfig) error {
	if err := platform.EnsureDir(stateDir, 0o700, platform.RootOwner); err != nil {
		return err
	}
	a.Version = appliedVersion
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return platform.AtomicWrite(path, data, 0o600, platform.RootOwner)
}

// loadApplied reads applied state. A missing file means nothing has been
// applied yet: every scope counts as changed until the instance starts or
// reloads and records it.
func loadApplied(ctx AppContext, appID string) (AppliedConfig, error) {
	a, ok, err := readApplied(ctx.Layout.AppAppliedConfig(appID))
	if err != nil || ok {
		return a, err
	}
	return AppliedConfig{Version: appliedVersion, Scopes: map[string]AppliedScope{}}, nil
}
