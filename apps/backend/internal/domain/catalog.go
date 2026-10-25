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
//
// PHP2Shell (CVE-2026-109618, CVE-2026-109619; fixed in 8.2.35, 8.3.36, 8.4.27,
// 8.5.12): a floating tag would not change the image hash, so the fixed
// releases are pinned by digest to force a rebuild. Upstream ships no fix for
// 7.4, 8.0 and 8.1; those are built from deb.sury.org packages on Debian (see
// PHPSuryPackages), so their base is Debian itself.
var PHPVersions = map[string]string{
	"7.4": DebianBase,
	"8.0": DebianBase,
	"8.1": DebianBookwormBase,
	"8.2": "php:8.2-fpm-trixie@sha256:77900c43ba8b8f2f61c73bcf68afbfb1b72a349052472e7640d402ef998754b3",
	"8.3": "php:8.3-fpm-trixie@sha256:56e2cf0558ddfbe36e90030f763b72be2f763bcd9075890e9c68420e0dfa827e",
	"8.4": "php:8.4-fpm-trixie@sha256:af1728147713459bda545446e3fae81cea5119543c46f28b9a077c7b3ff149c2",
	"8.5": "php:8.5-fpm-trixie@sha256:f49965c6e6a237eb3b27a6fc565e167839dc8abc65ee0652af0078f6ecdf4bbd",
}

var HTTPToolchains = map[string]map[string]string{
	"node": {
		"20": "node:20-trixie-slim@sha256:abfbe12cc943141a0c9e8c0a57d710df1dadd95d35e8662cc02958b284d1f35b",
		"22": "node:22-trixie-slim@sha256:8eb619e402eaede1748a3c8173bffab391592812e85136452f781741c7242ddf",
		"24": "node:24-trixie-slim@sha256:43b9ba1142b8dcf33fa8f922ca1be85714adc444e0586c7cf7726daaeabc1bea",
	},
	"bun": {
		"1.2": "oven/bun:1.2-debian",
		"1.3": "oven/bun:1.3-debian",
		"1.4": "oven/bun:1.4-debian@sha256:93d5032fe06d163427822dc1598f420363759bb18155c9cc90eb1be05a64a19e",
	},
	"python": {
		"3.12": "python:3.12-slim-trixie@sha256:a0f57064cbdcd6285fa45f51237ab0b35ea8ebaed06a1f5973e89e19dfb86704",
		"3.13": "python:3.13-slim-trixie@sha256:d98cc29d3266e535dd9da1099688d9cb3ca4753813571e071763d17725460962",
	},
}

// PHPSuryPackages lists the PHP versions built from deb.sury.org packages and
// the oldest package version of each that is accepted. A backport keeps the
// upstream version number, so only the package revision proves the PHP2Shell
// fix. Raising a floor changes the image hash and rebuilds every app on it.
var PHPSuryPackages = map[string]string{
	"7.4": "1:7.4.33-31",
	"8.0": "1:8.0.30-25",
	"8.1": "8.1.34-9",
}

// SuryKeySHA256 pins the deb.sury.org archive signing key
// (1505 8500 A023 5D97 F5D1 0063 B188 E2B6 95BD 4743, expires 2028-02-04).
const SuryKeySHA256 = "b486fd5488185c4c46467960fa69c53d5085fec492cf76b9eaf3db33561c9d7c"

const DebianBase = "debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad"

// DebianBookwormBase is the base of PHP 8.1 only: Sury's trixie arm64 index
// still carries the unfixed 8.1.34-8, below the PHPSuryPackages floor. Move 8.1
// to DebianBase once trixie arm64 has 8.1.34-9 or newer.
const DebianBookwormBase = "debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"

// RuntimeDebianBase returns the Debian base whose release matches a runtime
// key's base image. End-of-life bullseye releases reuse the PHP base itself:
// the current debian:bullseye-slim carries security updates that
// archive.debian.org cannot satisfy, while the PHP image's packages resolve.
func (k ImageKey) RuntimeDebianBase() string {
	if ref, err := k.BaseImage(); err == nil && (strings.Contains(ref, "-bullseye") || ref == DebianBookwormBase) {
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
	"MINICROND_VERSION":        "0.5.2",
	"MINICROND_AMD64_SHA256":   "4a1573e5d3579c81da3118bbca532b2e1aaee916a55ce41f7981a863946d70c1",
	"MINICROND_ARM64_SHA256":   "5140db71bea17128851520878cec48541f1649d6ba2fb3def2f80bd13d5da755",
	"COMPOSER_VERSION":         "2.10.3",
	"COMPOSER_SHA256":          "7a2d379d5b8ffdaa028580ef26494c36d2feef4b178d3dd1473a4dbc5e17c8d6",
}

// PHPNodeArtifacts pin the Node.js LTS toolchain (with npm) and pnpm layered
// into every managed PHP image for installing and building front-end assets.
var PHPNodeArtifacts = map[string]string{
	"NODE_BASE":    HTTPToolchains["node"]["24"],
	"PNPM_VERSION": "12.7.0",
	"PNPM_SHA256":  "1a7fe4b36abead79ee13e2f2c2089ddd37fee016179b9fe56a85a44ef72aaf7f",
}

// ResticArtifacts pin the restic binary layered onto RcloneImage to build the
// app backup image (restic's rclone backend runs the rclone next to it).
var ResticArtifacts = map[string]string{
	"RESTIC_VERSION":      "0.19.1",
	"RESTIC_AMD64_SHA256": "f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c",
	"RESTIC_ARM64_SHA256": "a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465",
}

// Pulled infrastructure images.
const (
	EdgeImage    = "nginx:stable-trixie@sha256:a7675fcee220f59b4a99f7098c06a0d5d999c2c0ee37c0211efb780536e5cb20"
	TunnelImage  = "cloudflare/cloudflared:2025.9.1@sha256:4604b477520dc8322af5427da68b44f0bf814938e9d2e4814f2249ee4b03ffdf"
	RcloneImage  = "rclone/rclone:1.71.1@sha256:d5971950c2b370fb04dd3292541b5bda6d9103143fd7e345aeb435a399388afc"
	RedisImage   = "redis:8.4-trixie@sha256:6a12a187547eb2eac4da4528f9d9787d69f3848e513480baedfd75e3b2ac393e"
	RedisVersion = "8.4"
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
	"8.4": "mysql:8.4@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69",
	"9.7": "mysql:9.7@sha256:8fb11c38b3551e22d50adbb688f027f8ff4ea2d2445aa277c1cc97b4af85ec54", // LTS
}

// PostgreSQL 17+ use the pglayers "full" profile (official postgres base plus
// prebuilt extensions such as pgvector, PostGIS, pg_cron, and timescaledb, with
// shared_preload_libraries preconfigured). pglayers does not publish 14-16, so
// those use the official image with its bundled contrib extensions.
var PostgresVersions = map[string]string{
	"14": "postgres:14-trixie@sha256:8ab94f61668dc73bbab4dc13273ff2fc37e635536af62aef5306a49a9682a446",
	"15": "postgres:15-trixie@sha256:baa22e6286bf832cb147b8bb5aa9d8bc999846d69c21d11bf63f58fbeed1fa48",
	"16": "postgres:16-trixie@sha256:9e2cf6287f25c3151f4c9421ccce2b17f3e60061c111a29165e0d2cdd9ff0a97",
	"17": "ghcr.io/pglayers/pglayers-full:17@sha256:f64f47233cf3dcb69121e3785f0acbc39af21c13f8f909538f71b7b21a934768",
	"18": "ghcr.io/pglayers/pglayers-full:18@sha256:d22f47895e8c2be156e048a5644c79f534090281fb0e9954222960fa008296de",
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

// SuryMinVersion is the oldest accepted deb.sury.org package version when the
// key's PHP is built from packages rather than the official image.
func (k ImageKey) SuryMinVersion() (string, bool) {
	if k.Kind != RuntimePHP {
		return "", false
	}
	v, ok := PHPSuryPackages[k.Version]
	return v, ok
}

func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
