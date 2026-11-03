package runtime

import (
	"regexp"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// This file renders the Nginx location blocks behind domain.Protection. The
// same text is included in the app-local Nginx (PHP apps) and in every edge
// route that targets the app, so the two layers never disagree.
//
// Every block answers 404, never 403: a refusal that differs from "not found"
// tells a scanner the path exists. Blocks are regex locations placed before
// the static-file and front-controller locations (the first matching regex in
// file order wins), and an operator drop-in included earlier can still
// override them. Patterns use PCRE lookarounds, so they are checked against
// real Nginx, not Go's regexp (see TestIntegrationProtectionNginx).

// packageMetadataFiles are dependency manifests and lock files. They list
// exactly which library versions an app runs.
var packageMetadataFiles = []string{
	"composer.json", "composer.lock",
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock",
	"pnpm-lock.yaml", "pnpm-workspace.yaml", "bun.lock", "bun.lockb", "bower.json",
	"Gemfile", "Gemfile.lock",
	"requirements.txt", "Pipfile", "Pipfile.lock", "poetry.lock", "pyproject.toml",
	"go.mod", "go.sum",
	"Cargo.toml", "Cargo.lock",
	"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "gradle.properties",
	"mix.exs", "mix.lock", "pubspec.yaml", "pubspec.lock",
}

// protectionBlock is one location block. Pattern is an unquoted Nginx regex:
// it must not contain whitespace, braces or semicolons.
type protectionBlock struct {
	comment string
	pattern string
	// caseSensitive selects "~" instead of "~*".
	caseSensitive bool
}

const (
	// dotFilesPattern matches any dot-prefixed segment except ACME's
	// conventional /.well-known/.
	dotFilesPattern = `/\.(?!well-known(?:/|$))`

	// wordpressPattern matches WordPress core paths and its XML-RPC and
	// Live Writer endpoints at any depth (/blog/wp-login.php, /wp/wp-admin/).
	wordpressPattern = `(?:^|/)(?:wp-(?:admin|includes|content|json|login|signup|activate|cron|config|config-sample|load|settings|blog-header|mail|links-opml|trackback|comments-post)(?:/|\.php|$)|xmlrpc\.php|wlwmanifest\.xml)`

	// dbToolsPattern matches database admin tools scanners look for.
	dbToolsPattern = `(?:^|/)(?:phpmyadmin[0-9.-]*|php-?my-?admin|pma|myadmin|mysqladmin|sqlite-?manager|adminer(?:-[0-9.]+)?)(?:/|\.php|$)`

	// webshellPattern matches file names used by common webshells, info
	// pages and exploit chains (the PHPUnit eval-stdin.php RCE).
	webshellPattern = `(?:^|/)(?:phpinfo|php-?info|pinfo|infophp|shell|c99|c100|r57|wso|b374k|alfa|alfashell|webshell|backdoor|eval-stdin|filemanager|tinyfilemanager)\.php(?:/|$)`

	// phpSourcePattern matches PHP extensions this Nginx does not execute: they
	// would be served as plain source text.
	phpSourcePattern = `\.(?:phps|phtml|pht|php[0-9]|phar|inc)(?:/|$)`

	// phpInContentPattern matches PHP under directories that hold user
	// uploads, caches, logs or third-party code, where a .php file is
	// planted, not deployed.
	phpInContentPattern = `(?:^|/)(?:uploads?|files|images?|img|media|assets|static|storage|cache|tmp|temp|logs?|backups?|vendor|node_modules|bower_components|cgi-bin)/.*\.php(?:/|$)`

	// backupPattern matches backup, editor-leftover, swap, dump and log files
	// (config.php.bak, config.php~, .index.php.swp, site.sql, debug.log).
	backupPattern = `(?:~|\.(?:bak|bkp|backup|old|orig|save|sav|rej|tmp|temp|sw[a-p]|sql|dump|sqlite3?|db|log|dist|sample))$`

	// archivePattern matches compressed archives. A .xml.gz is a sitemap.
	archivePattern = `(?:\.(?:zip|rar|7z|tar|tgz|tbz2?|txz|bz2|xz|zst|lz4|war|jar)|(?<!\.xml)\.gz)$`

	// sourceMapPattern matches JavaScript and CSS source maps.
	sourceMapPattern = `\.map$`

	// composerDirPattern matches Composer's installed-package metadata.
	composerDirPattern = `(?:^|/)vendor/composer/`
)

func packageMetadataPattern() string {
	names := make([]string, len(packageMetadataFiles))
	for i, n := range packageMetadataFiles {
		names[i] = regexp.QuoteMeta(n)
	}
	return `(?:^|/)(?:` + strings.Join(names, "|") + `)$`
}

// protectionBlocks returns the blocks p enables, in the order they are
// rendered.
func protectionBlocks(p domain.Protection) []protectionBlock {
	var out []protectionBlock
	if p.BlockDotFiles {
		out = append(out, protectionBlock{
			comment: "Dot files and directories (.env, .git, .htaccess); /.well-known/ stays reachable.",
			pattern: dotFilesPattern, caseSensitive: true,
		})
	}
	if p.BlockPHPProbes {
		out = append(out,
			protectionBlock{comment: "WordPress paths scanners probe on every PHP host.", pattern: wordpressPattern},
			protectionBlock{comment: "Database admin tools.", pattern: dbToolsPattern},
			protectionBlock{comment: "Webshell, info page and exploit file names.", pattern: webshellPattern},
			protectionBlock{comment: "PHP source extensions that would be served as text.", pattern: phpSourcePattern},
			protectionBlock{comment: "PHP planted in upload, cache and dependency directories.", pattern: phpInContentPattern},
		)
	}
	if p.BlockSensitiveFiles {
		out = append(out,
			protectionBlock{comment: "Backup, swap, dump and log files.", pattern: backupPattern},
			protectionBlock{comment: "Compressed archives.", pattern: archivePattern},
			protectionBlock{comment: "Source maps.", pattern: sourceMapPattern},
			protectionBlock{comment: "Package manager metadata.", pattern: packageMetadataPattern()},
			protectionBlock{comment: "Composer's installed-package metadata.", pattern: composerDirPattern},
		)
	}
	return out
}

// ProtectionConfig renders the location blocks p enables, each line prefixed
// with indent, or "" when p enables nothing. The result ends without a
// newline so a template decides the spacing.
func ProtectionConfig(p domain.Protection, indent string) string {
	blocks := protectionBlocks(p)
	if len(blocks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(indent + "# Bento protection: hostile request classes answer 404, never 403.\n")
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		op := "~*"
		if blk.caseSensitive {
			op = "~"
		}
		b.WriteString(indent + "# " + blk.comment + "\n")
		b.WriteString(indent + "location " + op + " " + blk.pattern + " {\n")
		b.WriteString(indent + "  access_log off;\n")
		b.WriteString(indent + "  log_not_found off;\n")
		b.WriteString(indent + "  return 404;\n")
		b.WriteString(indent + "}\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
