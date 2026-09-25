#!/bin/sh
# Līdza environment installer.
#
#   curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh
#   sh install.sh [--check] [--minimal] [--no-sudo] [--yes]
#
# Installs, when missing: Go, Rust (rustup) with the wasm targets, the Go and
# Rust helper tools, and the `lidza` CLI. Checks Node, Postgres and Redis and
# tells you what to do if they are missing. Safe to re-run; already-present
# tools are left alone.
#
# Linux and macOS, amd64 and arm64. On Windows use WSL2.
#
# --check    report what is present and missing, install nothing
# --minimal  skip helper tools (staticcheck, golangci-lint, sqlc, wasm-tools)
# --no-sudo  never call sudo; Go goes to ~/.local/go
# --yes      no confirmation prompt (implied when stdin is not a terminal)

set -eu

GO_MIN_MINOR=24
NODE_MIN_MAJOR=20
LIDZA_MODULE="github.com/agim/lidza"
LIDZA_ENV="$HOME/.lidza/env"

CHECK=0; MINIMAL=0; NO_SUDO=0; YES=0
for arg in "$@"; do
  case "$arg" in
    --check) CHECK=1 ;;
    --minimal) MINIMAL=1 ;;
    --no-sudo) NO_SUDO=1 ;;
    --yes|-y) YES=1 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
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
status_service() { # name host port hint
  if have nc && nc -z "$2" "$3" 2>/dev/null; then ok "$1 on $2:$3"
  elif (exec 3<>"/dev/tcp/$2/$3") 2>/dev/null; then ok "$1 on $2:$3"
  else skip "$1 not reachable on $2:$3 ($4)"; fi
}
status_lidza() {
  if have lidza; then ok "lidza $(lidza version 2>/dev/null || echo '(version unknown)')"; return 0; fi
  todo "lidza CLI"; return 1
}

report() {
  echo "Līdza environment check ($OS/$ARCH)"
  echo "Toolchain:"
  status_go || true
  status_rust || true
  status_node || true
  status_lidza || true
  echo "Helper tools:"
  for t in staticcheck golangci-lint sqlc wasm-tools; do status_tool "$t" || true; done
  echo "Services (optional for the first app):"
  status_service postgres 127.0.0.1 5432 "docker run -d --name lidza-pg -e POSTGRES_PASSWORD=lidza -p 5432:5432 postgres:17"
  status_service redis 127.0.0.1 6379 "docker run -d --name lidza-redis -p 6379:6379 redis:7"
  echo "Agent CLIs (any one is enough):"
  for t in claude codex gemini; do
    if have "$t"; then ok "$t"; else skip "$t not installed"; fi
  done
}

# --------------------------------------------------------------- installs ---

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
  have staticcheck   || go install honnef.co/go/tools/cmd/staticcheck@latest
  have golangci-lint || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
  have sqlc          || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
  have wasm-tools    || cargo install wasm-tools --locked
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

echo "Līdza installer ($OS/$ARCH)"
echo "Will install what is missing from: Go, Rust + wasm targets, helper tools, lidza CLI."
if can_sudo; then echo "Go goes to /usr/local/go (sudo available)."; else echo "Go goes to ~/.local/go (no sudo)."; fi
echo "PATH additions are written to $LIDZA_ENV and sourced from your shell rc."
if [ "$YES" -eq 0 ]; then
  printf 'Continue? [Y/n] '; read -r ans
  case "${ans:-Y}" in [Yy]*) ;; *) echo "aborted"; exit 1;; esac
fi

reload_env
echo "Go:";    install_go
echo "Rust:";  install_rust
echo "Node:";  status_node || echo "         install Node $NODE_MIN_MAJOR+ (https://nodejs.org or fnm/nvm); needed by the react, svelte and astro templates"
echo "Tools:"; install_tools
echo "Lidza:"; install_lidza
ensure_env_sourced

echo
report
echo
echo "Open a new shell (or run: . $LIDZA_ENV), then: lidza new myapp && cd myapp && lidza dev"
echo "Next: docs/getting-started.md"
