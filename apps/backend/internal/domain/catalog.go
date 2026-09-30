package domain

import (
	"fmt"
	"slices"
	"strings"
)

// Curated toolchains. A toolchain is a command environment inside a shared
// managed image, not a separate lifecycle engine. Base references are pinned by
// digest where the release has been verified; the resolved image ID of every
// built managed image is additionally recorded in state.
var PHPVersions = map[string]string{
	"7.4": "php:7.4-fpm-bullseye",
	"8.0": "php:8.0-fpm-bullseye",
	"8.1": "php:8.1-fpm-bookworm",
	"8.2": "php:8.2-fpm-bookworm",
	"8.3": "php:8.3-fpm-bookworm",
	"8.4": "php:8.4-fpm-bookworm@sha256:43e1ac38217031dbbecae60e84ccf8593722031559178d199bf56adb0145d5d0",
	"8.5": "php:8.5-fpm-bookworm",
}

var HTTPToolchains = map[string]map[string]string{
	"node": {
		"20": "node:20-bookworm-slim",
		"22": "node:22-bookworm-slim",
		"24": "node:24-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6",
	},
	"bun": {
		"1.2": "oven/bun:1.2-debian",
		"1.3": "oven/bun:1.3-debian",
	},
	"python": {
		"3.12": "python:3.12-slim-bookworm",
		"3.13": "python:3.13-slim-bookworm",
	},
}

const DebianBase = "debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"

// RuntimeDebianBase returns the Debian base whose release matches a runtime
// key's base image. End-of-life bullseye releases reuse the PHP base itself:
// the current debian:bullseye-slim carries security updates that
// archive.debian.org cannot satisfy, while the PHP image's packages resolve.
func (k ImageKey) RuntimeDebianBase() string {
	if ref, err := k.BaseImage(); err == nil && strings.Contains(ref, "-bullseye") {
		return ref
	}
	return DebianBase
}

// Supervision and scheduler artifacts downloaded (and checksum-verified) while
// building managed runtime images.
var RuntimeArtifacts = map[string]string{
	"S6_OVERLAY_VERSION":       "3.2.3.2",
	"S6_OVERLAY_NOARCH_SHA256": "5379750ed30a84bbd2e2dd74847ba6b5bd29cd0b2e3ea2ec58049b57eb2eda12",
	"S6_OVERLAY_AMD64_SHA256":  "e6befcc96a437a3831386ecfc51808c5d3e939dc5fe3c02ae9284599e8aa2408",
	"S6_OVERLAY_ARM64_SHA256":  "b17f17a82e7a515c682a91edaf2ffdabb73f891981b6c1fd712115693a2f8b4c",
	"MINICROND_VERSION":        "0.3.4",
	"MINICROND_AMD64_SHA256":   "fffb8b42411f37edbcef6146fb0dc809eafd178a787b78fcd9d486c4aac3a1b0",
	"MINICROND_ARM64_SHA256":   "3eef3c4d0f03016c5faa864fd082a8d13bd920797128a12e6634b2ae797db921",
	"COMPOSER_VERSION":         "2.10.3",
	"COMPOSER_SHA256":          "7a2d379d5b8ffdaa028580ef26494c36d2feef4b178d3dd1473a4dbc5e17c8d6",
}

// Pulled infrastructure images.
const (
	EdgeImage    = "nginx:stable-trixie@sha256:b972f831f200b19ef0767938224f9711e74cd783718738cd7405d5cabf75c442"
	TunnelImage  = "cloudflare/cloudflared:2025.9.1@sha256:4604b477520dc8322af5427da68b44f0bf814938e9d2e4814f2249ee4b03ffdf"
	RcloneImage  = "rclone/rclone:1.71.1@sha256:d5971950c2b370fb04dd3292541b5bda6d9103143fd7e345aeb435a399388afc"
	RedisImage   = "redis:8.2-bookworm@sha256:164c759a0c342ee69d08fc99219382b0fd682181465c0df2e0e6911f4c85d73c"
	RedisVersion = "8.2"
	// AdminerImage serves the database browser (one shared container).
	AdminerImage = "adminer:5.5.1@sha256:6c19fd07aaf25361fbc1541a7015c86cf670b830951cfb0d47869f34ec0ef2cf"
)

// MySQL database defaults for newly created app databases and for restores
// whose dump does not declare a character set.
const (
	MySQLDefaultCharset   = "utf8mb4"
	MySQLDefaultCollation = "utf8mb4_0900_ai_ci"
)

var MySQLVersions = map[string]string{
	"8.0": "mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b", // EOL upstream; kept for existing volumes, 8.0 -> 8.4 in-place upgrade is supported
	"8.4": "mysql:8.4@sha256:0744ee5ef89ce6ccfa13de3e579fe6b9e27f93dd70da9c06d2c908b1b193fb8d",
	"9.4": "mysql:9.4@sha256:135bc87cce147c3d28cecb9ad270b814cb52805af7ddeea83bfcaf157d05a6b2",
}

// PostgreSQL 17+ use the pglayers "full" profile (official postgres base plus
// prebuilt extensions such as pgvector, PostGIS, pg_cron, and timescaledb, with
// shared_preload_libraries preconfigured). pglayers does not publish 14-16, so
// those use the official image with its bundled contrib extensions.
var PostgresVersions = map[string]string{
	"14": "postgres:14-bookworm@sha256:dcc2ca942d8518144f387a0c2630188427835f984caaed0418b986928e761809",
	"15": "postgres:15-bookworm@sha256:539ceaaae49b3a7c8a04467cf00cc6788d8e3f1675df41860d86eebc4c40524f",
	"16": "postgres:16-bookworm@sha256:efedf3595f1d6f415c08568ba171029bf54052e754cc9f030e3f2412b21f3d67",
	"17": "ghcr.io/pglayers/pglayers-full:17@sha256:b7969f13473358d72a34c23391449f7065090a1ecc455cba75913287e975044c",
	"18": "ghcr.io/pglayers/pglayers-full:18@sha256:c71d1b7bd757dfb6049a85a1cdaaf5610675c215549ccfa31351133189f335e0",
}

// PHP performance modes. Both run one ondemand FPM pool: workers are forked
// per request (about 1ms) and reaped when idle, so an idle app holds no
// workers. Modes differ only in worker count and per-request PHP limits.
// Measurements behind these numbers: apps/backend/docs/evidence.md.
const (
	PHPModeStandard        = "standard"
	PHPModeHighConcurrency = "high-concurrency"
)

// PHPMode holds a mode's defaults. Worker count is derived from the app's
// memory limit, because FPM shares it with nginx, cron jobs and workers.
type PHPMode struct {
	WorkerMultiplier    int
	WebMemoryLimitMB    int
	CLIMemoryLimitMB    int
	MaxExecutionSeconds int
	MaxInputVars        int
}

var PHPModes = map[string]PHPMode{
	PHPModeStandard:        {WorkerMultiplier: 1, WebMemoryLimitMB: 128, CLIMemoryLimitMB: 256, MaxExecutionSeconds: 60, MaxInputVars: 1000},
	PHPModeHighConcurrency: {WorkerMultiplier: 3, WebMemoryLimitMB: 48, CLIMemoryLimitMB: 256, MaxExecutionSeconds: 30, MaxInputVars: 1000},
}

// Auto worker sizing: FPM gets PHPWebSharePercent of the app memory limit at
// PHPWorkerMemoryMB per typical worker; the rest stays with nginx, jobs and
// workers. The web UI mirrors this formula for its estimate.
const (
	PHPWebSharePercent = 60
	PHPWorkerMemoryMB  = 48
	PHPMinWorkers      = 2
	PHPMaxWorkers      = 200
	PHPIdleTimeout     = "10s"
)

// PHPSettings is the effective FPM pool and PHP configuration of one app.
type PHPSettings struct {
	Workers             int
	IdleTimeout         string
	WebMemoryLimitMB    int
	CLIMemoryLimitMB    int
	MaxExecutionSeconds int
	MaxInputVars        int
}

// AutoPHPWorkers is the worker count a mode gets for a memory and PID limit.
// Workers never take more than half of the PID limit, which jobs share.
func AutoPHPWorkers(mode PHPMode, res Resources) int {
	n := res.MemoryMB * PHPWebSharePercent / 100 / PHPWorkerMemoryMB
	n = max(n, PHPMinWorkers) * mode.WorkerMultiplier
	return max(min(n, res.PIDs/2, PHPMaxWorkers), 1)
}

// ResolvePHP applies mode defaults to unset fields. An unknown mode is
// refused, never guessed.
func ResolvePHP(p PHPRuntime, res Resources) (PHPSettings, error) {
	mode, ok := PHPModes[p.Mode]
	if !ok {
		return PHPSettings{}, fmt.Errorf("unknown PHP mode %q", p.Mode)
	}
	pick := func(v, def int) int {
		if v > 0 {
			return v
		}
		return def
	}
	return PHPSettings{
		Workers:             pick(p.MaxWorkers, AutoPHPWorkers(mode, res)),
		IdleTimeout:         PHPIdleTimeout,
		WebMemoryLimitMB:    pick(p.WebMemoryLimitMB, mode.WebMemoryLimitMB),
		CLIMemoryLimitMB:    pick(p.CLIMemoryLimitMB, mode.CLIMemoryLimitMB),
		MaxExecutionSeconds: pick(p.MaxExecutionSeconds, mode.MaxExecutionSeconds),
		MaxInputVars:        pick(p.MaxInputVars, mode.MaxInputVars),
	}, nil
}

// Default resource profiles, selected from the phase-1 measurements recorded
// in apps/backend/docs/evidence.md.
func DefaultResources(kind RuntimeKind) Resources {
	if kind == RuntimePHP {
		return Resources{MemoryMB: 512, CPUMillis: 1000, PIDs: 256}
	}
	return Resources{MemoryMB: 512, CPUMillis: 1000, PIDs: 256}
}

// ImageKey identifies one shared managed runtime image.
type ImageKey struct {
	Kind      RuntimeKind
	Toolchain string // "php" for PHP
	Version   string
}

func (k ImageKey) String() string { return fmt.Sprintf("%s-%s", k.Toolchain, k.Version) }

// ParseImageKey resolves the String form of a supported runtime image key.
// Only catalog entries match, so an unknown or retired key is refused.
func ParseImageKey(s string) (ImageKey, bool) {
	for v := range PHPVersions {
		if k := (ImageKey{Kind: RuntimePHP, Toolchain: "php", Version: v}); k.String() == s {
			return k, true
		}
	}
	for t, versions := range HTTPToolchains {
		for v := range versions {
			if k := (ImageKey{Kind: RuntimeHTTP, Toolchain: t, Version: v}); k.String() == s {
				return k, true
			}
		}
	}
	return ImageKey{}, false
}

func (r Runtime) ImageKey() ImageKey {
	if r.Kind == RuntimePHP && r.PHP != nil {
		return ImageKey{Kind: RuntimePHP, Toolchain: "php", Version: r.PHP.Version}
	}
	if r.HTTP != nil {
		return ImageKey{Kind: RuntimeHTTP, Toolchain: r.HTTP.Toolchain, Version: r.HTTP.Version}
	}
	return ImageKey{}
}

// BaseImage returns the pinned base reference for a runtime image key.
func (k ImageKey) BaseImage() (string, error) {
	if k.Kind == RuntimePHP {
		if ref, ok := PHPVersions[k.Version]; ok {
			return ref, nil
		}
		return "", fmt.Errorf("unsupported PHP version %q", k.Version)
	}
	if versions, ok := HTTPToolchains[k.Toolchain]; ok {
		if ref, ok := versions[k.Version]; ok {
			return ref, nil
		}
	}
	return "", fmt.Errorf("unsupported toolchain %s %s", k.Toolchain, k.Version)
}

func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
