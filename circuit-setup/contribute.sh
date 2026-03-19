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
#   bash contribute.sh --coordinator-url http://...
#
# Environment overrides:
#   CEREMONY_COORDINATOR_URL   Coordinator server URL (or use --coordinator-url flag)

# ── Defaults ──────────────────────────────────────────────────────────────────

RELEASE_REPO="testinprod-io/privacy-boost-ceremony"
COORDINATOR_URL="${CEREMONY_COORDINATOR_URL:-}"

WORK_DIR=""
CEREMONY_BIN=""
RELEASE_TAG=""

# ── Argument parsing ─────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --coordinator-url)
      COORDINATOR_URL="$2"
      shift 2
      ;;
    -h|--help)
      echo "Usage: bash contribute.sh [--coordinator-url <url>]"
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      echo "Usage: bash contribute.sh [--coordinator-url <url>]" >&2
      exit 2
      ;;
  esac
done

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

  log "Checking GitHub for the latest ceremony release..." >&2
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
    # Fallback: simple grep for tag pattern (match both v* and ceremony/v*)
    tag="$(printf '%s' "$releases_json" | grep -oE '"tag_name":\s*"(ceremony/v|v)[^"]*"' | head -1 | grep -oE '(ceremony/v|v)[^"]*' || true)"
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

  local state_dir="${repo}/ceremony-state"
  mkdir -p "$state_dir"

  docker run --rm -it \
    -v "${state_dir}:/work/ceremony-state" \
    ceremony-build \
    contribute \
    --coordinator-url "$COORDINATOR_URL"

  # Docker runs inside a temporary clone, so preserve the full local state
  # directory before the outer script cleanup removes the working tree.
  if [[ -d "${state_dir}" && -n "$(ls -A "${state_dir}" 2>/dev/null)" ]]; then
    rm -rf "${PWD}/ceremony-state"
    mkdir -p "${PWD}/ceremony-state"
    cp -R "${state_dir}/." "${PWD}/ceremony-state/"
    log_ok "Local contribution files copied to ${PWD}/ceremony-state"
  fi
}

# ── Contribution ──────────────────────────────────────────────────────────────

run_contribution() {
  "$CEREMONY_BIN" contribute \
    --coordinator-url "$COORDINATOR_URL"
}

# ── Interactive menu ──────────────────────────────────────────────────────────

show_banner() {
  echo ""
  echo "  ╔════════════════════════════════════════════════════════════╗"
  echo "  ║  Privacy Boost - Trusted Setup Ceremony                    ║"
  echo "  ║                                                            ║"
  echo "  ║  Thank you for contributing to the ceremony!               ║"
  echo "  ║  Your participation strengthens protocol security.         ║"
  echo "  ║  This may take 10-15 minutes.                              ║"
  echo "  ╚════════════════════════════════════════════════════════════╝"
  echo ""
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

main() {
  show_banner

  prompt_coordinator_url

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

  run_contribution
}

main "$@"
