#!/usr/bin/env bash
# Bento bootstrap: check the host, install missing tools and Docker Engine,
# install the latest Bento release, create the first stack, start it as a
# systemd service, and turn on the edge.
#
#   curl -fsSL https://raw.githubusercontent.com/khanhicetea/bento/main/scripts/bootstrap.sh | sudo bash
#
# It asks every question first, shows a summary, and changes nothing on the
# host until you confirm. (The release is downloaded to a temporary directory
# before that, so its checksum and your answers can be checked against it.)
#
# Environment:
#   BENTO_VERSION   release tag to install instead of the latest (e.g. 0.3.0)
#   BENTO_REPO      GitHub owner/repo to install from (default khanhicetea/bento)
#
# The management UI stays on 127.0.0.1; reach it through an SSH tunnel.
set -Eeuo pipefail

REPO="${BENTO_REPO:-khanhicetea/bento}"
BIN=/usr/local/bin/bento
UNIT=/etc/systemd/system/bento@.service
DEFAULT_PARENT=/srv/bento
MIN_DOCKER_API=1.44

# ---------------------------------------------------------------- output

if [ -t 1 ]; then
	B=$'\e[1m' DIM=$'\e[2m' RED=$'\e[31m' GRN=$'\e[32m' YEL=$'\e[33m' RST=$'\e[0m'
else
	B='' DIM='' RED='' GRN='' YEL='' RST=''
fi
say() { printf '%s\n' "$*"; }
step() { printf '\n%s==> %s%s\n' "$B" "$*" "$RST"; }
ok() { printf '  %s✓%s %s\n' "$GRN" "$RST" "$*"; }
warn() { printf '  %s!%s %s\n' "$YEL" "$RST" "$*"; }
die() {
	printf '%serror:%s %s\n' "$RED" "$RST" "$*" >&2
	exit 1
}

TMP=''
cleanup() { if [ -n "$TMP" ]; then rm -rf -- "$TMP"; fi; }
trap cleanup EXIT
trap 'die "failed at line $LINENO: $BASH_COMMAND"' ERR

# Questions are read from the terminal, so `curl ... | sudo bash` works.
if ! { exec 3</dev/tty; } 2>/dev/null; then
	die "no terminal to ask questions on; run this script interactively"
fi

ask() { # ask VAR "question" [default]
	local __var=$1 __q=$2 __def=${3-} __ans
	if [ -n "$__def" ]; then
		printf '%s %s[%s]%s: ' "$__q" "$DIM" "$__def" "$RST"
	else
		printf '%s: ' "$__q"
	fi
	IFS= read -r -u 3 __ans || die "input closed"
	__ans=${__ans#"${__ans%%[![:space:]]*}"}
	__ans=${__ans%"${__ans##*[![:space:]]}"}
	printf -v "$__var" '%s' "${__ans:-$__def}"
}

ask_secret() { # ask_secret VAR "question"
	local __var=$1 __ans
	printf '%s: ' "$2"
	IFS= read -rs -u 3 __ans || die "input closed"
	printf '\n'
	printf -v "$__var" '%s' "$__ans"
}

yes_no() { # yes_no "question" default(y|n)
	local a
	ask a "$1 (y/n)" "$2"
	case "$a" in [Yy] | [Yy][Ee][Ss]) return 0 ;; *) return 1 ;; esac
}

# ---------------------------------------------------------------- helpers

have() { command -v "$1" >/dev/null 2>&1; }

fetch() { # fetch URL [OUT]; OUT omitted prints to stdout
	if have curl; then
		curl -fsSL --retry 3 --proto '=https' --tlsv1.2 -o "${2:--}" "$1"
	else
		wget -q --https-only -O "${2:--}" "$1"
	fi
}

latest_tag() {
	local url
	if have curl; then
		url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
	else
		url=$(wget -q --max-redirect=5 -S --spider "https://github.com/$REPO/releases/latest" 2>&1 |
			sed -n 's/^ *[Ll]ocation: *//p' | tail -n1 | tr -d '\r')
	fi
	case "$url" in */releases/tag/*) printf '%s\n' "${url##*/}" ;; *) return 1 ;; esac
}

# A TCP port is in use when something listens on it on any address.
port_in_use() {
	local hex
	hex=$(printf ':%04X' "$1")
	awk -v p="$hex" '$4 == "0A" && substr($2, length($2) - 4) == p { f = 1 } END { exit !f }' \
		/proc/net/tcp /proc/net/tcp6 2>/dev/null
}

port_owner() {
	have ss || return 0
	ss -Hltnp "sport = :$1" 2>/dev/null | sed -n 's/.*users:(("\([^"]*\)".*/\1/p' | sort -u | paste -sd, -
}

valid_port() { [[ $1 =~ ^[0-9]{1,5}$ ]] && [ "$1" -ge 1 ] && [ "$1" -le 65535 ]; }

ask_port() { # ask_port VAR label default other
	local __var=$1 label=$2 def=$3 other=${4-} p owner
	p=$def
	if port_in_use "$p"; then
		owner=$(port_owner "$p")
		warn "port $p ($label) is in use${owner:+ by $owner}"
		p=''
	else
		ok "port $p ($label) is free"
		printf -v "$__var" '%s' "$p"
		return
	fi
	while :; do
		ask p "  Alternative $label port"
		if ! valid_port "$p"; then
			warn "enter a number from 1 to 65535"
		elif [ "$p" = "$other" ]; then
			warn "HTTP and HTTPS need different ports"
		elif port_in_use "$p"; then
			owner=$(port_owner "$p")
			warn "port $p is in use${owner:+ by $owner}"
		else
			break
		fi
	done
	printf -v "$__var" '%s' "$p"
}

version_ge() { [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]; }

pkg_install() {
	if have apt-get; then
		DEBIAN_FRONTEND=noninteractive apt-get update -qq
		DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@"
	elif have dnf; then
		dnf install -y -q "$@"
	elif have yum; then
		yum install -y -q "$@"
	elif have zypper; then
		zypper --non-interactive install "$@"
	else
		die "no supported package manager; install $* yourself and run again"
	fi
}

primary_ip() {
	local ip=''
	have ip && ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n1)
	[ -n "$ip" ] || ip=$(hostname -I 2>/dev/null | awk '{print $1}')
	printf '%s\n' "${ip:-your-server}"
}

# ---------------------------------------------------------------- 1. system requirements

say "${B}Bento bootstrap${RST}"
step "Checking system requirements"

[ "$(id -u)" -eq 0 ] || die "run as root (sudo -i, or pipe into sudo bash)"
[ "$(uname -s)" = Linux ] || die "Bento runs on Linux only"
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported CPU architecture $(uname -m); Bento ships amd64 and arm64" ;;
esac
ok "Linux $ARCH"

OS_NAME=unknown
if [ -r /etc/os-release ]; then
	# shellcheck disable=SC1091
	OS_NAME=$(. /etc/os-release && printf '%s' "${PRETTY_NAME:-$ID}")
fi
ok "$OS_NAME"

{ [ -d /run/systemd/system ] && have systemctl; } || die "systemd is required (Bento runs as the bento@<stack> service)"
ok "systemd"

MEM_MB=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
if [ "$MEM_MB" -lt 1000 ]; then
	warn "${MEM_MB} MiB RAM; 2 GiB or more is recommended (MySQL alone wants about 400 MiB)"
else
	ok "${MEM_MB} MiB RAM"
fi

TOOLS=()
have curl || TOOLS+=(curl)
[ -e /etc/ssl/certs/ca-certificates.crt ] || [ -e /etc/pki/tls/certs/ca-bundle.crt ] || TOOLS+=(ca-certificates)
if ! have curl && ! have wget; then
	die "neither curl nor wget is installed; install curl and run again"
fi
if [ ${#TOOLS[@]} -gt 0 ]; then
	warn "missing tools, will install: ${TOOLS[*]}"
else
	ok "curl, ca-certificates"
fi

# Docker: install, start, or accept as is. An old Docker is refused rather than replaced.
DOCKER_PLAN=ok
if ! have docker; then
	DOCKER_PLAN=install
	warn "Docker Engine is not installed; will install it with get.docker.com"
elif ! DOCKER_API=$(docker version --format '{{.Server.APIVersion}}' 2>/dev/null); then
	DOCKER_PLAN=start
	warn "Docker is installed but not running; will start it"
elif ! version_ge "$DOCKER_API" "$MIN_DOCKER_API"; then
	die "Docker Engine API $DOCKER_API is too old; Bento needs $MIN_DOCKER_API+ (Docker 25 or newer). Upgrade Docker and run again"
else
	ok "Docker Engine (API $DOCKER_API)"
fi

# One stack per run. A host that already serves a Bento stack is left alone.
for p in 7780 7781; do
	if port_in_use "$p"; then
		owner=$(port_owner "$p")
		die "127.0.0.1:$p is in use${owner:+ by $owner}. Bento's management listener needs it; is Bento already running here? (systemctl list-units 'bento@*')"
	fi
done
ok "management ports 7780, 7781 are free"

# ---------------------------------------------------------------- 2. release

step "Finding the Bento release"
if [ -n "${BENTO_VERSION:-}" ]; then
	TAG=$BENTO_VERSION
	ok "pinned by BENTO_VERSION: $TAG"
else
	TAG=$(latest_tag) || die "could not find the latest release of $REPO"
	ok "latest release: $TAG"
fi

TMP=$(mktemp -d)
ASSET="bento-linux-$ARCH"
fetch "https://github.com/$REPO/releases/download/$TAG/$ASSET" "$TMP/$ASSET" ||
	die "could not download $ASSET for $TAG"
fetch "https://github.com/$REPO/releases/download/$TAG/SHA256SUMS" "$TMP/SHA256SUMS" ||
	die "could not download SHA256SUMS for $TAG"
(cd "$TMP" && grep -E "  $ASSET\$" SHA256SUMS | sha256sum -c --status) ||
	die "checksum mismatch for $ASSET; refusing to install it"
chmod 0755 "$TMP/$ASSET"
ok "downloaded $ASSET and verified its SHA-256"
fetch "https://raw.githubusercontent.com/$REPO/$TAG/deploy/systemd/bento@.service" "$TMP/bento@.service" ||
	die "could not download deploy/systemd/bento@.service for $TAG"
ok "downloaded the systemd unit"

NEW_VERSION=$("$TMP/$ASSET" version | awk '{print $2}')
OLD_VERSION=''
if [ -x "$BIN" ]; then
	OLD_VERSION=$("$BIN" version 2>/dev/null | awk '{print $2}') || OLD_VERSION=unknown
fi

# ---------------------------------------------------------------- 3. questions

step "Edge ports (public HTTP and HTTPS)"
ask_port HTTP_PORT HTTP 80 ''
ask_port HTTPS_PORT HTTPS 443 "$HTTP_PORT"
if [ "$HTTP_PORT" = "$HTTPS_PORT" ]; then
	die "HTTP and HTTPS need different ports"
fi

step "Certificates"
say "  Let's Encrypt uses this address for account and expiry notices."
while :; do
	ask ACME_EMAIL "  ACME email"
	if [[ $ACME_EMAIL =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]]; then
		break
	fi
	warn "enter an email address like you@example.com"
done

step "Stack"
while :; do
	ask STACK_NAME "  Stack name" prod
	if ! [[ $STACK_NAME =~ ^[a-z][a-z0-9-]{0,22}[a-z0-9]$ ]]; then
		warn "2-24 lowercase letters, digits, or hyphens, starting with a letter"
	elif systemctl is-enabled "bento@$STACK_NAME" >/dev/null 2>&1; then
		warn "bento@$STACK_NAME is already enabled on this host; pick another name"
	elif [ -s "/etc/bento/$STACK_NAME.env" ]; then
		warn "/etc/bento/$STACK_NAME.env already exists; pick another name or remove it"
	else
		break
	fi
done
while :; do
	ask STACK_ROOT "  Install the stack in" "$DEFAULT_PARENT/$STACK_NAME"
	STACK_ROOT=${STACK_ROOT%/}
	if ! [[ $STACK_ROOT =~ ^/[A-Za-z0-9._/-]+$ ]] || [[ $STACK_ROOT =~ (^|/)\.\.?(/|$) ]]; then
		warn "use an absolute path of letters, digits, '.', '_', '-' and '/'"
	elif [ -e "$STACK_ROOT" ] && [ ! -d "$STACK_ROOT" ]; then
		warn "$STACK_ROOT exists and is not a directory"
	elif [ -d "$STACK_ROOT" ] && [ -n "$(ls -A "$STACK_ROOT")" ]; then
		warn "$STACK_ROOT is not empty; Bento only initializes an empty or new directory"
	else
		break
	fi
done
FREE_PARENT=$STACK_ROOT
while [ ! -d "$FREE_PARENT" ]; do FREE_PARENT=$(dirname "$FREE_PARENT"); done
FREE_GB=$(df -Pk "$FREE_PARENT" | awk 'NR == 2 {print int($4 / 1048576)}')
if [ "$FREE_GB" -lt 10 ]; then
	warn "only ${FREE_GB} GiB free under $FREE_PARENT; runtime images and app data want 10 GiB or more"
fi

step "Database servers (more can be added later in the UI)"
while :; do
	ask MYSQL "  MySQL version, or none" 8.4
	ask POSTGRES "  PostgreSQL version, or none" none
	[ "$MYSQL" = none ] && MYSQL=''
	[ "$POSTGRES" = none ] && POSTGRES=''
	# Dry-run init in a scratch directory: the release itself decides which
	# versions it supports, so nothing here goes stale.
	probe_args=(init --name "$STACK_NAME")
	[ -n "$MYSQL" ] && probe_args+=(--mysql "$MYSQL")
	[ -n "$POSTGRES" ] && probe_args+=(--postgres "$POSTGRES")
	if probe_err=$("$TMP/$ASSET" --stack "$TMP/probe" "${probe_args[@]}" 2>&1 >/dev/null); then
		rm -rf -- "$TMP/probe"
		break
	fi
	rm -rf -- "$TMP/probe"
	warn "${probe_err#error: }"
done

step "Web UI password"
say "  At least 12 characters. Press Enter to generate one."
GENERATED_PASSWORD='' PASSWORD2=''
while :; do
	ask_secret PASSWORD "  Password"
	if [ -z "$PASSWORD" ]; then
		PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n' | cut -c1-24)
		GENERATED_PASSWORD=$PASSWORD
		ok "generated; it is shown once at the end"
		break
	fi
	if [ "${#PASSWORD}" -lt 12 ]; then
		warn "too short"
		continue
	fi
	ask_secret PASSWORD2 "  Repeat password"
	[ "$PASSWORD" = "$PASSWORD2" ] && break
	warn "passwords differ"
done
unset PASSWORD2

ADD_BASHRC=n
grep -qs 'BENTO_STACK_ROOT' /root/.bashrc || ADD_BASHRC=y

# ---------------------------------------------------------------- 4. confirm

step "Summary"
row() { printf '  %-14s %s\n' "$1" "$2"; }
[ ${#TOOLS[@]} -gt 0 ] && row "Tools" "install ${TOOLS[*]}"
case $DOCKER_PLAN in
install) row "Docker" "install Docker Engine (get.docker.com), enable docker.service" ;;
start) row "Docker" "enable and start docker.service" ;;
ok) row "Docker" "already running (API $DOCKER_API)" ;;
esac
if [ -z "$OLD_VERSION" ]; then
	row "Bento" "install $NEW_VERSION to $BIN"
elif [ "$OLD_VERSION" = "$NEW_VERSION" ]; then
	row "Bento" "$NEW_VERSION already in $BIN"
else
	row "Bento" "replace $OLD_VERSION with $NEW_VERSION in $BIN"
fi
row "Service" "$UNIT, bento@$STACK_NAME (enabled at boot)"
row "Stack" "$STACK_NAME in $STACK_ROOT"
[ "$STACK_ROOT" != "$DEFAULT_PARENT/$STACK_NAME" ] && row "" "BENTO_STACK_ROOT in /etc/bento/$STACK_NAME.env"
row "MySQL" "${MYSQL:-none}"
row "PostgreSQL" "${POSTGRES:-none}"
row "Edge" "enabled on 0.0.0.0, HTTP $HTTP_PORT, HTTPS $HTTPS_PORT"
row "ACME email" "$ACME_EMAIL"
row "UI password" "$([ -n "$GENERATED_PASSWORD" ] && echo generated || echo 'set (hidden)')"
row "Management" "127.0.0.1:7780 (SSH tunnel only)"
[ "$ADD_BASHRC" = y ] && row "Shell" "export BENTO_STACK_ROOT in /root/.bashrc"
say ""
yes_no "Proceed?" n || {
	say "Nothing was changed."
	exit 0
}

# ---------------------------------------------------------------- 5. run

if [ ${#TOOLS[@]} -gt 0 ]; then
	step "Installing tools"
	pkg_install "${TOOLS[@]}"
	ok "installed ${TOOLS[*]}"
fi

if [ "$DOCKER_PLAN" = install ]; then
	step "Installing Docker Engine"
	fetch https://get.docker.com "$TMP/get-docker.sh"
	sh "$TMP/get-docker.sh"
fi
if [ "$DOCKER_PLAN" != ok ]; then
	systemctl enable --now docker
	for _ in $(seq 1 30); do
		DOCKER_API=$(docker version --format '{{.Server.APIVersion}}' 2>/dev/null) && break
		sleep 1
	done
	[ -n "${DOCKER_API:-}" ] || die "Docker did not start; see: journalctl -u docker"
	version_ge "$DOCKER_API" "$MIN_DOCKER_API" ||
		die "Docker Engine API $DOCKER_API is too old; Bento needs $MIN_DOCKER_API+"
	ok "Docker Engine running (API $DOCKER_API)"
fi

step "Installing Bento $NEW_VERSION"
if [ "$OLD_VERSION" != "$NEW_VERSION" ]; then
	install -m 0755 "$TMP/$ASSET" "$BIN.new"
	mv -f "$BIN.new" "$BIN"
fi
ok "$("$BIN" version)"
install -m 0644 "$TMP/bento@.service" "$UNIT"
if [ "$STACK_ROOT" != "$DEFAULT_PARENT/$STACK_NAME" ]; then
	install -d -m 0755 /etc/bento
	printf 'BENTO_STACK_ROOT=%s\n' "$STACK_ROOT" >"/etc/bento/$STACK_NAME.env"
	chmod 0644 "/etc/bento/$STACK_NAME.env"
	ok "/etc/bento/$STACK_NAME.env"
fi
systemctl daemon-reload
ok "$UNIT"

step "Creating stack $STACK_NAME"
install -d -m 0755 "$(dirname "$STACK_ROOT")"
init_args=(init --name "$STACK_NAME" --password-stdin)
[ -n "$MYSQL" ] && init_args+=(--mysql "$MYSQL")
[ -n "$POSTGRES" ] && init_args+=(--postgres "$POSTGRES")
printf '%s\n' "$PASSWORD" | "$BIN" --stack "$STACK_ROOT" "${init_args[@]}" >/dev/null
unset PASSWORD
ok "initialized $STACK_ROOT"

step "Starting bento@$STACK_NAME"
systemctl enable --now "bento@$STACK_NAME"
export BENTO_STACK_ROOT=$STACK_ROOT
up=''
for _ in $(seq 1 60); do
	if "$BIN" status >/dev/null 2>&1; then
		up=y
		break
	fi
	sleep 2
done
[ -n "$up" ] || die "bento@$STACK_NAME did not answer within 2 minutes; see: journalctl -u bento@$STACK_NAME"
ok "backend is up"

step "Turning on the edge"
printf '{"enabled":true,"bind":"0.0.0.0","httpPort":%d,"httpsPort":%d,"http3":false,"acmeEmail":"%s","acmeUrl":""}\n' \
	"$HTTP_PORT" "$HTTPS_PORT" "$ACME_EMAIL" | "$BIN" edge set --json - >/dev/null
ok "edge listening on $HTTP_PORT and $HTTPS_PORT"

if [ "$ADD_BASHRC" = y ]; then
	printf '\nexport BENTO_STACK_ROOT=%s\n' "$STACK_ROOT" >>/root/.bashrc
	ok "added BENTO_STACK_ROOT to /root/.bashrc"
fi

# ---------------------------------------------------------------- 6. done

step "Done"
"$BIN" status || true
IP=$(primary_ip)
say ""
say "${B}Open the UI${RST} from your computer:"
say "  ssh -L 7780:127.0.0.1:7780 root@$IP"
say "  then browse to http://127.0.0.1:7780"
if [ -n "$GENERATED_PASSWORD" ]; then
	say ""
	say "${B}UI password${RST} (shown once, store it now): $GENERATED_PASSWORD"
	say "  change it any time: bento auth set-password"
fi
say ""
say "${B}Back up the stack backup password${RST} somewhere off this host:"
say "  bento --stack $STACK_ROOT backup password"
if have ufw && ufw status 2>/dev/null | grep -q '^Status: active'; then
	say ""
	warn "ufw is active; allow the edge ports: ufw allow $HTTP_PORT/tcp && ufw allow $HTTPS_PORT/tcp"
elif have firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
	say ""
	warn "firewalld is running; allow the edge ports: firewall-cmd --permanent --add-port={$HTTP_PORT,$HTTPS_PORT}/tcp && firewall-cmd --reload"
fi
if [ "$HTTP_PORT" != 80 ]; then
	say ""
	warn "HTTP is on $HTTP_PORT, not 80: Let's Encrypt HTTP-01 validation needs port 80 to reach the edge (forward 80 to $HTTP_PORT, or use a Cloudflare Tunnel)"
fi
if [ "$OLD_VERSION" != "" ] && [ "$OLD_VERSION" != "$NEW_VERSION" ]; then
	others=$(systemctl list-units --type=service --state=running --plain --no-legend 'bento@*' 2>/dev/null |
		awk -v me="bento@$STACK_NAME.service" '$1 != me {printf "%s ", $1}')
	if [ -n "$others" ]; then
		say ""
		warn "other stacks run the old binary until restarted: systemctl restart $others"
	fi
fi
say ""
say "Next: create your first app — https://github.com/$REPO/tree/$TAG/apps/docs/docs/get-started"
