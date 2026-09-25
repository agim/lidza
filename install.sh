#!/bin/sh
# Līdza environment installer.
#
#   curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh -s -- --services
#   sh install.sh [--check] [--services] [--minimal] [--no-sudo] [--yes]
#
# Installs, when missing: the build prerequisites (git, curl, a C toolchain),
# Go, Rust (rustup) with the wasm targets, Node, the Go and Rust helper
# tools, and the `lidza` CLI. With --services also Postgres and Valkey (or
# Redis), started, with a database role for the current user. Safe to
# re-run; what is present is left alone.
#
# Linux (apt, dnf, pacman) and macOS (Homebrew), amd64 and arm64. On
# Windows use WSL2.
#
# --check     report what is present and missing, install nothing
# --services  also install and start Postgres and Valkey, and create the
#             database role (asked interactively when omitted)
# --minimal   skip helper tools (staticcheck, golangci-lint, sqlc, wasm-tools)
# --no-sudo   never call sudo; Go goes to ~/.local/go, services are printed
#             as commands instead of run
# --yes       no confirmation prompt (implied when stdin is not a terminal)

set -eu

GO_MIN_MINOR=24
NODE_MIN_MAJOR=20
# Node is installed from nodejs.org into ~/.local/opt when missing.
NODE_VERSION=22.23.2
# Helper tools, pinned: the versions the framework is developed and tested
# with (docs/environment.md, .github/workflows/ci.yml).
STATICCHECK_VERSION=2026.2.1
GOLANGCI_LINT_VERSION=v2.14.0
SQLC_VERSION=v1.31.1
WASM_TOOLS_VERSION=1.259.0
LIDZA_MODULE="github.com/agim/lidza"
LIDZA_ENV="$HOME/.lidza/env"

CHECK=0; MINIMAL=0; NO_SUDO=0; YES=0; SERVICES=0; ASK_SERVICES=1
for arg in "$@"; do
  case "$arg" in
    --check) CHECK=1 ;;
    --services) SERVICES=1; ASK_SERVICES=0 ;;
    --minimal) MINIMAL=1 ;;
    --no-sudo) NO_SUDO=1 ;;
    --yes|-y) YES=1 ;;
    -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done
[ -t 0 ] || YES=1

# ---------------------------------------------------------------- helpers ---

ok()   { printf '  [ok]   %s\n' "$1"; }
todo() { printf '  [todo] %s\n' "$1"; }
skip() { printf '  [--]   %s\n' "$1"; }
fail() { printf '  [fail] %s\n' "$1" >&2; }
die()  { fail "$1"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in linux|darwin) ;; *) die "unsupported OS: $OS (use WSL2 on Windows)";; esac
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

fetch() { # url dest
  if have curl; then curl -fsSL "$1" -o "$2"
  elif have wget; then wget -qO "$2" "$1"
  else die "need curl or wget"; fi
}

can_sudo() {
  [ "$NO_SUDO" -eq 0 ] || return 1
  have sudo || return 1
  sudo -n true 2>/dev/null
}

# The system package manager, for the prerequisites and the services.
PM=""
if [ "$OS" = darwin ]; then have brew && PM=brew
elif have apt-get; then PM=apt
elif have dnf; then PM=dnf
elif have pacman; then PM=pacman
fi

# as_root runs a command with sudo when allowed, else prints it as a todo.
as_root() {
  if [ "$(id -u)" = 0 ]; then "$@"; return; fi
  if can_sudo; then sudo "$@"; return; fi
  todo "run as root: $*"; return 1
}

# start_service enables and starts a system service: systemd where it runs
# (not in a container), the classic service scripts otherwise, brew on macOS.
start_service() { # name
  if [ "$PM" = brew ]; then brew services start "$1"; return; fi
  if have systemctl && systemctl is-system-running >/dev/null 2>&1; then as_root systemctl enable --now "$1"; return; fi
  if have systemctl && [ "$(systemctl is-system-running 2>/dev/null)" != "offline" ] && as_root systemctl enable --now "$1" 2>/dev/null; then return; fi
  as_root service "$1" start
}

# pm_install installs system packages; returns 1 when it could not.
pm_install() { # packages...
  case "$PM" in
    apt) as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -q "$@" ;;
    dnf) as_root dnf install -y -q "$@" ;;
    pacman) as_root pacman -S --noconfirm --needed "$@" ;;
    brew) brew install "$@" ;;
    *) todo "install with your package manager: $*"; return 1 ;;
  esac
}

# Everything this script puts on PATH is recorded in ~/.lidza/env, sourced by
# the user's shell rc. Mirrors rustup's ~/.cargo/env.
add_env() { # line
  mkdir -p "$(dirname "$LIDZA_ENV")"
  touch "$LIDZA_ENV"
  grep -qxF "$1" "$LIDZA_ENV" || printf '%s\n' "$1" >> "$LIDZA_ENV"
}
ensure_env_sourced() {
  line='[ -f "$HOME/.lidza/env" ] && . "$HOME/.lidza/env"'
  for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
    [ -f "$rc" ] || continue
    grep -qF '.lidza/env' "$rc" || printf '\n# Līdza toolchain\n%s\n' "$line" >> "$rc"
  done
  [ -f "$HOME/.profile" ] || printf '# Līdza toolchain\n%s\n' "$line" > "$HOME/.profile"
}
# Pick up what this run installed without a new shell.
reload_env() { [ -f "$LIDZA_ENV" ] && . "$LIDZA_ENV"; return 0; }

go_minor() { go version 2>/dev/null | sed -n 's/.*go1\.\([0-9]*\).*/\1/p'; }
node_major() { node --version 2>/dev/null | sed -n 's/^v\([0-9]*\).*/\1/p'; }

# ----------------------------------------------------------------- checks ---

status_go() {
  if have go && [ "$(go_minor)" -ge "$GO_MIN_MINOR" ] 2>/dev/null; then ok "go $(go version | awk '{print $3}') at $(command -v go)"; return 0; fi
  if have go; then todo "go $(go version | awk '{print $3}') is older than 1.$GO_MIN_MINOR"; else todo "go: not installed"; fi
  return 1
}
status_rust() {
  r=0
  if have cargo && have rustc; then ok "$(rustc --version) at $(command -v cargo)"; else todo "rust: not installed"; r=1; fi
  if have rustup; then
    for t in wasm32-wasip1 wasm32-unknown-unknown; do
      if rustup target list --installed 2>/dev/null | grep -qx "$t"; then ok "rust target $t"; else todo "rust target $t"; r=1; fi
    done
  else
    [ $r -eq 0 ] && { todo "rustup: not installed (needed for wasm targets)"; r=1; }
  fi
  return $r
}
status_node() {
  if have node && [ "$(node_major)" -ge "$NODE_MIN_MAJOR" ] 2>/dev/null; then ok "node $(node --version), npm $(npm --version 2>/dev/null || echo '?')"; return 0; fi
  if have node; then todo "node $(node --version) is older than $NODE_MIN_MAJOR"; else todo "node: not installed"; fi
  return 1
}
status_tool() { # name
  if have "$1"; then ok "$1"; return 0; else todo "$1"; return 1; fi
}
# port_open tests a TCP port with whatever the machine has: nc, bash's
# /dev/tcp (this script may run under dash), or the service's own client.
port_open() { # name host port
  if have nc; then nc -z "$2" "$3" 2>/dev/null; return; fi
  if have bash && bash -c "exec 3<>/dev/tcp/$2/$3" 2>/dev/null; then return 0; fi
  case "$1" in
    postgres) have pg_isready && pg_isready -q -h "$2" -p "$3" 2>/dev/null && return 0 ;;
    valkey) { have valkey-cli && valkey-cli -h "$2" -p "$3" ping 2>/dev/null | grep -q PONG; } && return 0
            { have redis-cli && redis-cli -h "$2" -p "$3" ping 2>/dev/null | grep -q PONG; } && return 0 ;;
  esac
  return 1
}
status_service() { # name host port
  if port_open "$1" "$2" "$3"; then ok "$1 on $2:$3"
  else skip "$1 not reachable on $2:$3 (sh install.sh --services, or $(service_hint "$1"))"; return 1; fi
}
# service_hint is the one-line install for a service on this machine.
service_hint() { # postgres|valkey
  case "$PM:$1" in
    apt:postgres) echo "sudo apt-get install -y postgresql && sudo -u postgres createuser -s \$USER" ;;
    dnf:postgres) echo "sudo dnf install -y postgresql-server && sudo postgresql-setup --initdb && sudo systemctl enable --now postgresql && sudo -u postgres createuser -s \$USER" ;;
    pacman:postgres) echo "sudo pacman -S postgresql && sudo -u postgres initdb -D /var/lib/postgres/data && sudo systemctl enable --now postgresql && sudo -u postgres createuser -s \$USER" ;;
    brew:postgres) echo "brew install postgresql@17 && brew services start postgresql@17" ;;
    apt:valkey) echo "sudo apt-get install -y valkey-server (or redis-server)" ;;
    dnf:valkey) echo "sudo dnf install -y valkey && sudo systemctl enable --now valkey" ;;
    pacman:valkey) echo "sudo pacman -S valkey && sudo systemctl enable --now valkey" ;;
    brew:valkey) echo "brew install valkey && brew services start valkey" ;;
    *:postgres) echo "install Postgres 15+ and create a superuser role named \$USER" ;;
    *) echo "install Valkey or Redis on 6379" ;;
  esac
}
status_prereqs() {
  r=0
  for t in git curl; do if have "$t"; then ok "$t"; else todo "$t"; r=1; fi; done
  if have cc || have gcc || have clang; then ok "C toolchain (Rust needs a linker)"; else todo "C toolchain (build-essential, Development Tools, base-devel, or Xcode command line tools)"; r=1; fi
  return $r
}
status_lidza() {
  if have lidza; then ok "lidza $(lidza version 2>/dev/null || echo '(version unknown)')"; return 0; fi
  todo "lidza CLI"; return 1
}

report() {
  echo "Līdza environment check ($OS/$ARCH, packages via ${PM:-none})"
  echo "Prerequisites:"
  status_prereqs || true
  echo "Toolchain:"
  status_go || true
  status_rust || true
  status_node || true
  status_lidza || true
  echo "Helper tools:"
  for t in staticcheck golangci-lint sqlc wasm-tools; do status_tool "$t" || true; done
  echo "Services (Postgres for the db, auth, jobs, mail and analytics packs; Valkey for cache and realtime):"
  status_service postgres 127.0.0.1 5432 || true
  status_service valkey 127.0.0.1 6379 || true
  echo "Agent CLIs (any one is enough):"
  for t in claude codex gemini; do
    if have "$t"; then ok "$t"; else skip "$t not installed"; fi
  done
}

# --------------------------------------------------------------- installs ---

install_prereqs() {
  status_prereqs && return 0
  case "$PM" in
    apt) pm_install git curl ca-certificates build-essential ;;
    dnf) pm_install git curl gcc make ;;
    pacman) pm_install git curl base-devel ;;
    brew) have cc || xcode-select --install 2>/dev/null || true; have git || pm_install git ;;
    *) todo "install git, curl and a C toolchain with your package manager" ;;
  esac
  status_prereqs || true
}

install_node() {
  status_node && return 0
  case "$OS-$ARCH" in
    linux-amd64) tarball="node-v$NODE_VERSION-linux-x64" ;;
    linux-arm64) tarball="node-v$NODE_VERSION-linux-arm64" ;;
    darwin-amd64) tarball="node-v$NODE_VERSION-darwin-x64" ;;
    darwin-arm64) tarball="node-v$NODE_VERSION-darwin-arm64" ;;
  esac
  dest="$HOME/.local/opt/$tarball"
  if [ ! -x "$dest/bin/node" ]; then
    echo "  downloading Node $NODE_VERSION"
    mkdir -p "$HOME/.local/opt"
    fetch "https://nodejs.org/dist/v$NODE_VERSION/$tarball.tar.gz" "/tmp/$tarball.tar.gz"
    tar -xzf "/tmp/$tarball.tar.gz" -C "$HOME/.local/opt"
    rm -f "/tmp/$tarball.tar.gz"
  fi
  add_env "export PATH=\"$dest/bin:\$PATH\""
  reload_env
  status_node || die "node install did not take effect"
}

# install_services installs and starts Postgres and Valkey (Redis where
# Valkey is not packaged) and creates a superuser role named after the
# current user, so the templates' socket DSN works without a password.
install_services() {
  echo "Postgres:"
  if ! status_service postgres 127.0.0.1 5432; then
    case "$PM" in
      apt) pm_install postgresql && start_service postgresql ;;
      dnf) pm_install postgresql-server && { [ -d /var/lib/pgsql/data/base ] || as_root postgresql-setup --initdb; } && start_service postgresql ;;
      pacman) pm_install postgresql && { [ -d /var/lib/postgres/data/base ] || as_root su - postgres -c "initdb -D /var/lib/postgres/data"; } && start_service postgresql ;;
      brew) pm_install postgresql@17 && start_service postgresql@17 ;;
      *) todo "$(service_hint postgres)" ;;
    esac
    sleep 2
    status_service postgres 127.0.0.1 5432 || true
  fi
  # The role: the current user as superuser, for local development only.
  if have psql; then
    if psql -d postgres -tAc "select 1" >/dev/null 2>&1; then ok "postgres role $(id -un) can connect"
    elif [ "$OS" = darwin ]; then createuser -s "$(id -un)" 2>/dev/null && ok "postgres role $(id -un) created" || todo "createuser -s $(id -un)"
    elif as_root su - postgres -c "psql -tAc \"select 1 from pg_roles where rolname='$(id -un)'\"" 2>/dev/null | grep -q 1; then ok "postgres role $(id -un) exists"
    elif as_root su - postgres -c "createuser -s $(id -un)" 2>/dev/null; then ok "postgres role $(id -un) created (superuser, for local development)"
    else todo "sudo -u postgres createuser -s $(id -un)"; fi
  fi
  echo "Valkey:"
  if ! status_service valkey 127.0.0.1 6379; then
    case "$PM" in
      apt) if pm_install valkey-server 2>/dev/null; then start_service valkey-server; else pm_install redis-server && start_service redis-server; fi ;;
      dnf) pm_install valkey && start_service valkey ;;
      pacman) pm_install valkey && start_service valkey ;;
      brew) pm_install valkey && start_service valkey ;;
      *) todo "$(service_hint valkey)" ;;
    esac
    sleep 1
    status_service valkey 127.0.0.1 6379 || true
  fi
}

install_go() {
  status_go && return 0
  ver=$(curl -fsSL 'https://go.dev/VERSION?m=text' 2>/dev/null | head -1) || die "cannot look up the Go version"
  tarball="${ver}.${OS}-${ARCH}.tar.gz"
  if can_sudo; then prefix=/usr/local; sudo_cmd="sudo"; else prefix="$HOME/.local"; sudo_cmd=""; fi
  echo "  installing $ver to $prefix/go"
  tmp=$(mktemp -d)
  fetch "https://go.dev/dl/$tarball" "$tmp/$tarball"
  $sudo_cmd rm -rf "$prefix/go"
  $sudo_cmd mkdir -p "$prefix"
  $sudo_cmd tar -C "$prefix" -xzf "$tmp/$tarball"
  rm -rf "$tmp"
  add_env "export PATH=\"$prefix/go/bin:\$HOME/go/bin:\$PATH\""
  reload_env
  status_go || die "go install did not take effect"
}

install_rust() {
  if ! have rustup; then
    echo "  installing rustup (stable, default profile)"
    fetch https://sh.rustup.rs /tmp/rustup-init.sh
    sh /tmp/rustup-init.sh -y --profile default --no-modify-path >/dev/null
    rm -f /tmp/rustup-init.sh
    add_env 'export PATH="$HOME/.cargo/bin:$PATH"'
    reload_env
  fi
  for t in wasm32-wasip1 wasm32-unknown-unknown; do
    rustup target list --installed | grep -qx "$t" || rustup target add "$t"
  done
  status_rust || die "rust install did not take effect"
}

install_tools() {
  [ "$MINIMAL" -eq 0 ] || { skip "helper tools (--minimal)"; return 0; }
  have staticcheck   || go install honnef.co/go/tools/cmd/staticcheck@$STATICCHECK_VERSION
  have golangci-lint || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_LINT_VERSION
  have sqlc          || go install github.com/sqlc-dev/sqlc/cmd/sqlc@$SQLC_VERSION
  have wasm-tools    || cargo install wasm-tools --version "$WASM_TOOLS_VERSION" --locked
  for t in staticcheck golangci-lint sqlc wasm-tools; do status_tool "$t" || true; done
}

install_lidza() {
  status_lidza && return 0
  echo "  go install $LIDZA_MODULE/cmd/lidza@latest"
  if go install "$LIDZA_MODULE/cmd/lidza@latest" 2>/tmp/lidza-install.err; then
    status_lidza
  else
    fail "lidza CLI: go install failed ($(tail -1 /tmp/lidza-install.err))"
    echo "         the CLI ships in roadmap Phase 1; until then the toolchain above is what you need"
  fi
  rm -f /tmp/lidza-install.err
}

# ------------------------------------------------------------------- main ---

if [ "$CHECK" -eq 1 ]; then report; exit 0; fi

echo "Līdza installer ($OS/$ARCH, packages via ${PM:-none})"
echo "Will install what is missing from: git, curl, C toolchain, Go, Rust + wasm targets, Node $NODE_VERSION, helper tools, lidza CLI."
if [ "$SERVICES" -eq 1 ]; then echo "And the services: Postgres and Valkey, started, with a database role for $(id -un)."; fi
if can_sudo; then echo "Go goes to /usr/local/go (sudo available)."; else echo "Go goes to ~/.local/go (no sudo)."; fi
echo "PATH additions are written to $LIDZA_ENV and sourced from your shell rc."
if [ "$YES" -eq 0 ]; then
  printf 'Continue? [Y/n] '; read -r ans
  case "${ans:-Y}" in [Yy]*) ;; *) echo "aborted"; exit 1;; esac
  if [ "$ASK_SERVICES" -eq 1 ]; then
    printf 'Also install and start Postgres and Valkey for the packs? [y/N] '; read -r ans
    case "${ans:-N}" in [Yy]*) SERVICES=1 ;; esac
  fi
fi

reload_env
echo "Prerequisites:"; install_prereqs
echo "Go:";    install_go
echo "Rust:";  install_rust
echo "Node:";  install_node
echo "Tools:"; install_tools
echo "Lidza:"; install_lidza
if [ "$SERVICES" -eq 1 ]; then install_services; fi
ensure_env_sourced

echo
report
echo
echo "Open a new shell (or run: . $LIDZA_ENV), then: lidza new myapp && cd myapp && lidza dev"
echo "Next: docs/getting-started.md"
