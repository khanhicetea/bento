package domain

import (
	"fmt"
	"slices"
)

// Curated toolchains. A toolchain is a command environment inside a shared
// managed image, not a separate lifecycle engine.
//
// Base references are floating tags: building an image pulls the newest
// release under the tag, so an operator rebuild (System > Images) picks up
// PHP and Debian patch releases without a Bento release. Every build records
// the base image ids and tool versions it resolved (store.RuntimeBuild), and
// apps keep running the build they were rolled out to until the operator rolls
// them forward.
//
// PHP2Shell (CVE-2026-109618, CVE-2026-109619; fixed in 8.2.35, 8.3.36, 8.4.27,
// 8.5.12): PHPMinVersions refuses an official base older than the fixed
// release, and changing a floor changes the image inputs, so every app on that
// PHP version is rebuilt. Upstream ships no fix for 7.4, 8.0 and 8.1; those are
// built from deb.sury.org packages on Debian (see PHPSuryPackages), so their
// base is Debian itself.
var PHPVersions = map[string]string{
	"7.4": DebianBase,
	"8.0": DebianBase,
	"8.1": DebianBookwormBase,
	"8.2": "php:8.2-fpm-trixie",
	"8.3": "php:8.3-fpm-trixie",
	"8.4": "php:8.4-fpm-trixie",
	"8.5": "php:8.5-fpm-trixie",
}

// PHPMinVersions is the oldest PHP release accepted from each official base.
var PHPMinVersions = map[string]string{
	"8.2": "8.2.35",
	"8.3": "8.3.36",
	"8.4": "8.4.27",
	"8.5": "8.5.12",
}

// HTTPToolchains maps each HTTP toolchain version to the mise tool requests
// installed in its image on DebianBase. A request names a release line; the
// build resolves its newest release. Every tool is a prebuilt release (Python
// is a python-build-standalone build), so installing them compiles nothing.
var HTTPToolchains = map[string]map[string]string{
	"node": {
		"20": "node@20 pnpm@latest",
		"22": "node@22 pnpm@latest",
		"24": "node@24 pnpm@latest",
	},
	"bun": {
		"1.2": "bun@1.2",
		"1.3": "bun@1.3",
		"1.4": "bun@1.4",
	},
	"python": {
		"3.12": "python@3.12",
		"3.13": "python@3.13",
	},
}

// PHPTools are the mise tool requests installed in every PHP image, so apps
// can install node_modules and build front-end assets with the tooling exec.
const PHPTools = "node@24 pnpm@latest"

// ComposerChannel is the getcomposer.org release channel installed in PHP
// images; a rebuild picks up its newest release.
const ComposerChannel = "latest-2.x"

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

const DebianBase = "debian:trixie-slim"

// DebianBookwormBase is the base of PHP 8.1 only: Sury's trixie arm64 index
// still carries the unfixed 8.1.34-8, below the PHPSuryPackages floor. Move 8.1
// to DebianBase once trixie arm64 has 8.1.34-9 or newer.
const DebianBookwormBase = "debian:bookworm-slim"

// ValkeyBuildBase is the base of the shared Valkey image, built once and copied
// into every runtime image. The binaries link only libc and libm, and glibc runs
// programs built against an older release, so this must stay the oldest Debian
// release among the runtime bases (bookworm: PHP 8.1; everything else is
// trixie).
const ValkeyBuildBase = DebianBookwormBase

// RuntimeDebianBase returns the Debian base whose release matches a runtime
// key's base image: the Debian stages of an image (artifacts, toolchain, and
// the runtime stage itself) must share its libc.
func (k ImageKey) RuntimeDebianBase() string {
	if ref, err := k.BaseImage(); err == nil && ref == DebianBookwormBase {
		return ref
	}
	return DebianBase
}

// Supervision, scheduler and mise artifacts downloaded (and checksum-verified)
// while building managed runtime images. These are pinned: changing one is a
// Bento release.
var RuntimeArtifacts = map[string]string{
	"S6_OVERLAY_VERSION":       "3.2.3.2",
	"S6_OVERLAY_NOARCH_SHA256": "5379750ed30a84bbd2e2dd74847ba6b5bd29cd0b2e3ea2ec58049b57eb2eda12",
	"S6_OVERLAY_AMD64_SHA256":  "e6befcc96a437a3831386ecfc51808c5d3e939dc5fe3c02ae9284599e8aa2408",
	"S6_OVERLAY_ARM64_SHA256":  "b17f17a82e7a515c682a91edaf2ffdabb73f891981b6c1fd712115693a2f8b4c",
	"MINICROND_VERSION":        "0.7.2",
	"MINICROND_AMD64_SHA256":   "89a1b2ed0636841feb7d308621d2b8a023a5a4eaccc5525c3b28015f6e4760dc",
	"MINICROND_ARM64_SHA256":   "003afab579ae6611467334806c29b483d542631e825c7e1642dca739c2ec32af",
	"MISE_VERSION":             "2026.10.12",
	"MISE_AMD64_SHA256":        "72404716768b5b044cbd5170136d3d795cbd592658b6e932b66f0f8d67a54875",
	"MISE_ARM64_SHA256":        "31bbf697b289fde4b5623b223af288edd9ef86298ce9ecff705ac867f6f27e09",
}

// ValkeyArtifacts pins the Valkey source tarball (one checksum for every
// architecture) that the shared Valkey image compiles on ValkeyBuildBase.
var ValkeyArtifacts = map[string]string{
	"VALKEY_VERSION": "9.1.3",
	"VALKEY_SHA256":  "184e34ed2d9b51879b682b5b15205e2b9d677c60280e3cb453be8f520d04e293",
}

// ValkeyPort is the loopback port of an app's own Valkey server. HTTP apps
// cannot listen on it.
const ValkeyPort = 6379

// ResticArtifacts pin the restic binary layered onto RcloneImage to build the
// app backup image (restic's rclone backend runs the rclone next to it).
var ResticArtifacts = map[string]string{
	"RESTIC_VERSION":      "0.19.1",
	"RESTIC_AMD64_SHA256": "f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c",
	"RESTIC_ARM64_SHA256": "a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465",
}

// Pulled infrastructure images.
const (
	EdgeImage   = "nginx:stable-trixie@sha256:a7675fcee220f59b4a99f7098c06a0d5d999c2c0ee37c0211efb780536e5cb20"
	TunnelImage = "cloudflare/cloudflared:2025.9.1@sha256:4604b477520dc8322af5427da68b44f0bf814938e9d2e4814f2249ee4b03ffdf"
	RcloneImage = "rclone/rclone:1.71.1@sha256:d5971950c2b370fb04dd3292541b5bda6d9103143fd7e345aeb435a399388afc"
	// AdminerImage serves the database browser (one shared container).
	AdminerImage = "adminer:5.5.1@sha256:6c19fd07aaf25361fbc1541a7015c86cf670b830951cfb0d47869f34ec0ef2cf"
	// GoAccessImage aggregates rotated edge access logs into host traffic
	// reports (one short-lived, network-less container per host and run).
	GoAccessImage = "allinurl/goaccess:1.12@sha256:9e6dfd4abce94bd7c2907c1840b7b35f666587d56b24ffbb930b4782a87ea172"
)

// MySQL database defaults for newly created app databases and for restores
// whose dump does not declare a character set.
const (
	MySQLDefaultCharset   = "utf8mb4"
	MySQLDefaultCollation = "utf8mb4_0900_ai_ci"
)

var MySQLVersions = map[string]string{
	"8.0": "mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b", // EOL upstream. No version is upgraded in place: move a database to a newer service by dump and restore
	"8.4": "mysql:8.4@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69",
	"9.7": "mysql:9.7@sha256:8fb11c38b3551e22d50adbb688f027f8ff4ea2d2445aa277c1cc97b4af85ec54", // LTS
}

// ServiceImage is the catalog image of a data service engine and version.
// A data service only ever runs the catalog image of its version: images are
// never taken from imported state or archives.
func ServiceImage(engine Engine, version string) (string, bool) {
	switch engine {
	case EngineMySQL:
		img, ok := MySQLVersions[version]
		return img, ok
	case EnginePostgres:
		img, ok := PostgresVersions[version]
		return img, ok
	}
	return "", false
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
	OpcacheMemoryMB     int
	OpcacheMaxFiles     int
	RealpathCacheKB     int
}

var PHPModes = map[string]PHPMode{
	PHPModeStandard: {
		WorkerMultiplier: 1, WebMemoryLimitMB: 128, CLIMemoryLimitMB: 256, MaxExecutionSeconds: 60, MaxInputVars: 1000,
		OpcacheMemoryMB: 128, OpcacheMaxFiles: 20000, RealpathCacheKB: 4096,
	},
	PHPModeHighConcurrency: {
		WorkerMultiplier: 3, WebMemoryLimitMB: 48, CLIMemoryLimitMB: 256, MaxExecutionSeconds: 30, MaxInputVars: 1000,
		OpcacheMemoryMB: 128, OpcacheMaxFiles: 20000, RealpathCacheKB: 4096,
	},
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
	OpcacheMemoryMB     int
	OpcacheMaxFiles     int
	RealpathCacheKB     int
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
		OpcacheMemoryMB:     pick(p.OpcacheMemoryMB, mode.OpcacheMemoryMB),
		OpcacheMaxFiles:     pick(p.OpcacheMaxFiles, mode.OpcacheMaxFiles),
		RealpathCacheKB:     pick(p.RealpathCacheKB, mode.RealpathCacheKB),
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

// BaseImage returns the base reference (a floating tag) for a runtime image
// key. HTTP toolchains are installed by mise on DebianBase.
func (k ImageKey) BaseImage() (string, error) {
	if k.Kind == RuntimePHP {
		if ref, ok := PHPVersions[k.Version]; ok {
			return ref, nil
		}
		return "", fmt.Errorf("unsupported PHP version %q", k.Version)
	}
	if _, err := k.Tools(); err != nil {
		return "", err
	}
	return DebianBase, nil
}

// Tools returns the mise tool requests installed in the key's image.
func (k ImageKey) Tools() (string, error) {
	if k.Kind == RuntimePHP {
		if _, ok := PHPVersions[k.Version]; ok {
			return PHPTools, nil
		}
		return "", fmt.Errorf("unsupported PHP version %q", k.Version)
	}
	if versions, ok := HTTPToolchains[k.Toolchain]; ok {
		if tools, ok := versions[k.Version]; ok {
			return tools, nil
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
