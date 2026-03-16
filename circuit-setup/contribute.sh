#!/usr/bin/env bash
set -euo pipefail

# Privacy Boost Trusted Setup — Contributor Script
#
# A standalone, interactive script for contributing to the Privacy Boost
# trusted setup ceremony. Contributors choose how to obtain the ceremony
# binary (pre-built release or self-build) and the script handles the rest.
#
# Usage:
#   curl -fsSLO https://raw.githubusercontent.com/testinprod-io/privacy-boost-ceremony/main/circuit-setup/contribute.sh
#   bash contribute.sh
#
# Environment overrides (all optional):
#   CEREMONY_COORDINATOR_URL   Coordinator server URL
#   CEREMONY_CONFIG_URL        URL to download ceremony config JSON

# ── Defaults ──────────────────────────────────────────────────────────────────

RELEASE_REPO="testinprod-io/privacy-boost-ceremony"
COORDINATOR_URL="${CEREMONY_COORDINATOR_URL:-}"
CONFIG_URL="${CEREMONY_CONFIG_URL:-}"

WORK_DIR=""
CEREMONY_BIN=""
RELEASE_TAG=""

# ── Helpers ───────────────────────────────────────────────────────────────────

log()      { printf '\033[1;34m[ceremony]\033[0m %s\n' "$*"; }
log_ok()   { printf '\033[1;32m[ceremony]\033[0m %s\n' "$*"; }
log_warn() { printf '\033[1;33m[ceremony]\033[0m %s\n' "$*" >&2; }
log_err()  { printf '\033[1;31m[ceremony]\033[0m %s\n' "$*" >&2; }

cleanup() {
  if [[ -n "${WORK_DIR}" && -d "${WORK_DIR}" ]]; then
    log "Cleaning up work directory: ${WORK_DIR}"
    rm -rf "${WORK_DIR}"
  fi
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log_err "Required command not found: $1"
    return 1
  fi
}

detect_platform() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "$os" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *)
      log_err "Unsupported OS: $os"
      exit 1
      ;;
  esac
  case "$arch" in
    x86_64|amd64)   arch="amd64" ;;
    arm64|aarch64)   arch="arm64" ;;
    *)
      log_err "Unsupported architecture: $arch"
      exit 1
      ;;
  esac
  GOOS="$os"
  GOARCH="$arch"
}

file_sha256() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  else
    log_err "No checksum tool found (need shasum or sha256sum)"
    return 1
  fi
}

# ── Release resolution ────────────────────────────────────────────────────────

resolve_release_tag() {
  # Return cached result if already resolved
  if [[ -n "$RELEASE_TAG" ]]; then
    printf '%s\n' "$RELEASE_TAG"
    return 0
  fi

  log "Checking GitHub for the latest ceremony release..."
  local releases_json
  releases_json="$(curl -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    -H "User-Agent: privacy-boost-ceremony" \
    "https://api.github.com/repos/${RELEASE_REPO}/releases?per_page=20" 2>/dev/null || true)"

  if [[ -z "$releases_json" ]]; then
    log_err "Could not query GitHub releases."
    return 1
  fi

  local tag
  if command -v python3 >/dev/null 2>&1; then
    tag="$(printf '%s' "$releases_json" | python3 -c '
import json, sys
for r in json.load(sys.stdin):
    if r.get("draft") or r.get("prerelease"):
        continue
    t = r.get("tag_name", "")
    if t.startswith("ceremony/v") or t.startswith("v"):
        print(t)
        break
' 2>/dev/null || true)"
  else
    # Fallback: simple grep for tag pattern
    tag="$(printf '%s' "$releases_json" | grep -o '"tag_name":\s*"ceremony/v[^"]*"' | head -1 | grep -o 'ceremony/v[^"]*' || true)"
  fi

  if [[ -z "$tag" ]]; then
    log_err "No stable ceremony release found."
    return 1
  fi
  RELEASE_TAG="$tag"
  printf '%s\n' "$RELEASE_TAG"
}

# ── Option 1: Download pre-built release ──────────────────────────────────────

do_prebuilt_release() {
  require_cmd curl
  require_cmd tar
  detect_platform

  local release_tag
  release_tag="$(resolve_release_tag)" || exit 1
  log "Using release: $release_tag"

  local asset="ceremony-${GOOS}-${GOARCH}.tar.gz"
  local base_url="https://github.com/${RELEASE_REPO}/releases/download/${release_tag}"

  WORK_DIR="$(mktemp -d)"
  trap cleanup EXIT

  log "Downloading ${asset}..."
  if ! curl -fL --progress-bar "${base_url}/${asset}" -o "${WORK_DIR}/${asset}"; then
    log_err "Failed to download ${asset} from ${release_tag}"
    exit 1
  fi

  log "Downloading checksum..."
  if ! curl -fL --progress-bar "${base_url}/${asset}.sha256" -o "${WORK_DIR}/${asset}.sha256"; then
    log_err "Failed to download checksum for ${asset}"
    exit 1
  fi

  log "Verifying checksum..."
  local expected actual
  expected="$(awk '{print $1}' "${WORK_DIR}/${asset}.sha256" | tr '[:upper:]' '[:lower:]')"
  actual="$(file_sha256 "${WORK_DIR}/${asset}" | tr '[:upper:]' '[:lower:]')"
  if [[ "$expected" != "$actual" ]]; then
    log_err "Checksum verification FAILED"
    log_err "  expected: ${expected}"
    log_err "  got:      ${actual}"
    exit 1
  fi
  log_ok "Checksum verified."

  tar -xzf "${WORK_DIR}/${asset}" -C "${WORK_DIR}"
  if [[ ! -f "${WORK_DIR}/ceremony" ]]; then
    log_err "Release archive did not contain a ceremony binary"
    exit 1
  fi
  chmod 0755 "${WORK_DIR}/ceremony"
  CEREMONY_BIN="${WORK_DIR}/ceremony"
  log_ok "Pre-built binary ready."
}

# ── Clone repo (shared by self-build options) ─────────────────────────────────

clone_repo() {
  require_cmd git

  local release_tag
  release_tag="$(resolve_release_tag)" || exit 1
  log "Cloning repository at tag: ${release_tag}"

  WORK_DIR="$(mktemp -d)"
  trap cleanup EXIT

  git clone --depth 1 --branch "${release_tag}" \
    "https://github.com/${RELEASE_REPO}.git" "${WORK_DIR}/repo"
  log_ok "Repository cloned."
}

# ── Option 2: Build from source (local Go) ────────────────────────────────────

discover_go() {
  local candidate=""

  # 1. PATH
  candidate="$(command -v go 2>/dev/null || true)"
  if [[ -n "$candidate" && -x "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return 0
  fi

  # 2. mise
  if command -v mise >/dev/null 2>&1; then
    candidate="$(mise which go 2>/dev/null || true)"
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  fi

  # 3. Homebrew (macOS)
  local brew_go
  for brew_go in \
    /opt/homebrew/opt/go/libexec/bin/go \
    /usr/local/opt/go/libexec/bin/go \
    /usr/local/go/bin/go; do
    if [[ -x "$brew_go" ]]; then
      printf '%s\n' "$brew_go"
      return 0
    fi
  done

  # 4. User's login shell
  local shell_path="${SHELL:-}"
  if [[ -n "$shell_path" && -x "$shell_path" ]]; then
    candidate="$("$shell_path" -ilc 'command -v go 2>/dev/null || true' 2>/dev/null || true)"
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  fi

  return 1
}

do_build_local() {
  local go_bin
  go_bin="$(discover_go || true)"
  if [[ -z "$go_bin" ]]; then
    log_err "Go not found. Install Go 1.24+ or choose the Docker build option."
    exit 1
  fi
  log "Found Go: $("$go_bin" version 2>/dev/null || echo "$go_bin")"

  clone_repo

  local repo="${WORK_DIR}/repo"
  log "Building ceremony binary..."
  (cd "$repo" && CGO_ENABLED=0 "$go_bin" build -trimpath -o ./bin/ceremony ./cmd/ceremony)

  CEREMONY_BIN="${repo}/bin/ceremony"
  if [[ ! -x "$CEREMONY_BIN" ]]; then
    log_err "Build did not produce an executable binary"
    exit 1
  fi
  log_ok "Build complete."
}

# ── Option 3: Build from source (Docker) ──────────────────────────────────────

do_build_and_run_docker() {
  require_cmd docker

  # Verify Docker daemon is running
  if ! docker info >/dev/null 2>&1; then
    log_err "Docker daemon is not running. Please start Docker and try again."
    exit 1
  fi

  clone_repo

  local repo="${WORK_DIR}/repo"

  log "Building ceremony image with Docker..."
  docker build -t ceremony-build -f "${repo}/Dockerfile" "${repo}"

  log_ok "Build complete."
  echo ""
  log "Starting contribution in Docker container..."
  log "  Coordinator: ${COORDINATOR_URL}"
  log "  You will be asked to open a URL in your browser for GitHub authentication."
  echo ""

  local config_path
  config_path="$(download_config "${repo}/config")"

  docker run --rm -it \
    -v "${config_path}:/work/ceremony.config.json:ro" \
    ceremony-build \
    contribute \
    --config /work/ceremony.config.json \
    --coordinator-url "$COORDINATOR_URL"

  log_ok "Contribution complete. Thank you!"
}

# ── Config download ───────────────────────────────────────────────────────────

download_config() {
  local dest_dir="$1"
  mkdir -p "$dest_dir"
  local config_path="${dest_dir}/ceremony.config.json"

  if [[ -z "$CONFIG_URL" ]]; then
    log_err "No config URL provided."
    log_err "Set CEREMONY_CONFIG_URL or pass it when prompted."
    exit 1
  fi

  log "Downloading ceremony config..."
  if ! curl -fsSL "$CONFIG_URL" -o "$config_path"; then
    log_err "Failed to download config from ${CONFIG_URL}"
    exit 1
  fi
  printf '%s\n' "$config_path"
}

# ── Config extraction ─────────────────────────────────────────────────────────

extract_state_dir() {
  local config_path="$1"
  grep -o '"stateDir"[[:space:]]*:[[:space:]]*"[^"]*"' "$config_path" \
    | head -1 \
    | sed 's/.*"stateDir"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'
}

# ── Contribution ──────────────────────────────────────────────────────────────

run_contribution() {
  local config_path="$1"

  local state_dir
  state_dir="$(extract_state_dir "$config_path")"
  if [[ -z "$state_dir" ]]; then
    log_err "Could not read stateDir from config"
    exit 1
  fi
  mkdir -p "$state_dir"

  log "Starting contribution..."
  log "  Coordinator: ${COORDINATOR_URL}"
  log "  Config:      ${config_path}"
  echo ""

  "$CEREMONY_BIN" contribute \
    --config "$config_path" \
    --coordinator-url "$COORDINATOR_URL"

  log_ok "Contribution complete. Thank you!"
}

# ── Interactive menu ──────────────────────────────────────────────────────────

show_banner() {
  cat <<'BANNER'

  ╔══════════════════════════════════════════════════════════════╗
  ║         Privacy Boost — Trusted Setup Ceremony              ║
  ║                                                              ║
  ║  Thank you for contributing to the ceremony!                 ║
  ║  Your participation strengthens the security of the system.  ║
  ╚══════════════════════════════════════════════════════════════╝

BANNER
}

show_menu() {
  cat <<'MENU'
  How would you like to obtain the ceremony binary?

    1) Download pre-built release          (fastest)
    2) Build from source (local Go)        (requires Go 1.24+)
    3) Build from source (Docker)          (no local toolchain needed)

MENU
}

prompt_coordinator_url() {
  if [[ -n "$COORDINATOR_URL" ]]; then
    return 0
  fi
  echo ""
  printf '  Enter the coordinator URL: '
  read -r COORDINATOR_URL
  if [[ -z "$COORDINATOR_URL" ]]; then
    log_err "Coordinator URL is required."
    exit 1
  fi
}

prompt_config_url() {
  if [[ -n "$CONFIG_URL" ]]; then
    return 0
  fi
  echo ""
  printf '  Enter the ceremony config URL: '
  read -r CONFIG_URL
  if [[ -z "$CONFIG_URL" ]]; then
    log_err "Config URL is required."
    exit 1
  fi
}

main() {
  show_banner

  prompt_coordinator_url
  prompt_config_url

  local use_docker=false

  show_menu
  local choice
  while true; do
    printf '  Enter your choice [1-3]: '
    read -r choice
    case "$choice" in
      1) do_prebuilt_release; break ;;
      2) do_build_local;      break ;;
      3) use_docker=true;     break ;;
      *)
        log_warn "Invalid choice. Please enter 1, 2, or 3."
        ;;
    esac
  done

  # Docker builds and runs contribution inside the container
  if [[ "$use_docker" == true ]]; then
    do_build_and_run_docker
    return 0
  fi

  echo ""
  log_ok "Ceremony binary: ${CEREMONY_BIN}"
  echo ""

  # Download config
  local config_dir="${WORK_DIR:-$(mktemp -d)}"
  if [[ -z "${WORK_DIR}" ]]; then
    WORK_DIR="$config_dir"
    trap cleanup EXIT
  fi
  local config_path
  config_path="$(download_config "${config_dir}/config")"

  run_contribution "$config_path"
}

main "$@"
