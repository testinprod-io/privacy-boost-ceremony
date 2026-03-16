#!/usr/bin/env bash
set -euo pipefail

# Contributor quickstart for the Privacy Boost ceremony.
# - Reuses a provided or existing ceremony binary when available
# - Downloads an official ceremony GitHub release binary when available
# - Detects local Go installs (PATH, shell-managed, mise/asdf/homebrew)
# - Falls back to Docker or a repo-local Go toolchain when needed
# - Creates the configured stateDir (required for downloads)
# - Runs `ceremony contribute`

DEFAULT_GO_VERSION="1.24.0"

CONFIG_PATH="${CEREMONY_CONFIG_PATH:-}"
COORDINATOR_URL="${CEREMONY_COORDINATOR_URL:-}"
QUIET="${CEREMONY_QUIET:-}"
CEREMONY_BIN="${CEREMONY_BINARY_PATH:-}"
BUILD_MODE="${CEREMONY_BUILD_MODE:-auto}"
GO_VERSION="${CEREMONY_GO_VERSION:-$DEFAULT_GO_VERSION}"
DOCKER_IMAGE="${CEREMONY_DOCKER_IMAGE:-golang:${GO_VERSION}-bookworm}"
CEREMONY_RELEASE_REPO="${CEREMONY_RELEASE_REPO:-testinprod-io/privacy-boost-ceremony}"
CEREMONY_RELEASE_VERSION="${CEREMONY_RELEASE_VERSION:-}"
GO_BIN=""
GO_SOURCE=""

usage() {
  cat <<'EOF'
Usage:
  bash circuit-setup/contribute_quickstart.sh --config path.json --coordinator-url http://host [--binary path] [--release-version X.Y.Z] [--release-repo owner/repo] [--build-mode auto|local|docker] [--quiet]

Environment overrides:
  CEREMONY_CONFIG_PATH=...       (required: path to ceremony config json)
  CEREMONY_COORDINATOR_URL=...   (required: coordinator endpoint)
  CEREMONY_BINARY_PATH=...       (use an existing ceremony binary and skip build)
  CEREMONY_RELEASE_VERSION=...   (download ceremony/v<version> before building)
  CEREMONY_RELEASE_REPO=...      (default: testinprod-io/privacy-boost-ceremony)
  CEREMONY_BUILD_MODE=auto       (auto, local, or docker; default: auto)
  CEREMONY_GO_VERSION=1.24.0     (used for local Go fallback / Docker image tag)
  CEREMONY_DOCKER_IMAGE=...      (default: golang:${CEREMONY_GO_VERSION}-bookworm)
  CEREMONY_QUIET=1               (same as --quiet)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      CONFIG_PATH="$2"
      shift 2
      ;;
    --coordinator-url)
      COORDINATOR_URL="$2"
      shift 2
      ;;
    --binary)
      CEREMONY_BIN="$2"
      shift 2
      ;;
    --release-version)
      CEREMONY_RELEASE_VERSION="$2"
      shift 2
      ;;
    --release-repo)
      CEREMONY_RELEASE_REPO="$2"
      shift 2
      ;;
    --build-mode)
      BUILD_MODE="$2"
      shift 2
      ;;
    --quiet)
      QUIET=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown arg: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${CONFIG_PATH}" ]]; then
  echo "error: --config is required" >&2
  usage >&2
  exit 1
fi

if [[ -z "${COORDINATOR_URL}" ]]; then
  echo "error: --coordinator-url is required" >&2
  usage >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

log() {
  printf '[quickstart] %s\n' "$*"
}

log_warn() {
  printf '[quickstart] warning: %s\n' "$*" >&2
}

log_error() {
  printf '[quickstart] error: %s\n' "$*" >&2
}

log_done() {
  log "$* completed."
}

case "${BUILD_MODE}" in
  auto|local|docker)
    ;;
  *)
    log_error "unsupported build mode: ${BUILD_MODE}"
    usage >&2
    exit 2
    ;;
esac

log "Preparing contributor quickstart."
log "Using config: ${CONFIG_PATH}"
log "Using coordinator: ${COORDINATOR_URL}"
log "Build mode: ${BUILD_MODE}"
log "Release repo: ${CEREMONY_RELEASE_REPO}"

if [[ ! -f "${CONFIG_PATH}" ]]; then
  log_error "config not found: ${CONFIG_PATH}"
  log_error "tip: run from repo root, or pass --config <path>."
  exit 1
fi

require_cmd() {
  local c="$1"
  if ! command -v "$c" >/dev/null 2>&1; then
    log_error "missing dependency: $c"
    return 1
  fi
}

APT_UPDATED=""

apt_install() {
  if ! command -v apt-get >/dev/null 2>&1; then
    return 1
  fi
  if [[ -z "${APT_UPDATED}" ]]; then
    log "Updating apt package index."
    if command -v sudo >/dev/null 2>&1; then
      sudo apt-get update
    else
      apt-get update
    fi
    APT_UPDATED=1
  fi
  log "Installing packages via apt: $*"
  if command -v sudo >/dev/null 2>&1; then
    sudo apt-get install -y "$@"
  else
    apt-get install -y "$@"
  fi
}

ensure_cmd_or_install_apt() {
  local cmd="$1"
  shift
  local pkgs=("$@")
  if command -v "${cmd}" >/dev/null 2>&1; then
    return 0
  fi
  if command -v apt-get >/dev/null 2>&1; then
    if command -v sudo >/dev/null 2>&1; then
      log "Installing ${cmd} with apt (requires sudo)."
    else
      log "Installing ${cmd} with apt."
    fi
    apt_install "${pkgs[@]}"
    command -v "${cmd}" >/dev/null 2>&1
    return
  fi
  log_error "missing dependency: ${cmd}"
  return 1
}

detect_goos_goarch() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "$os" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *)
      log_error "unsupported OS: $(uname -s)"
      return 1
      ;;
  esac
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      log_error "unsupported arch: $(uname -m)"
      return 1
      ;;
  esac
  echo "${os}" "${arch}"
}

release_requested_explicitly() {
  [[ -n "${CEREMONY_RELEASE_VERSION}" ]]
}

normalize_release_tag() {
  local version="$1"
  if [[ -z "${version}" ]]; then
    return 1
  fi
  if [[ "${version}" == ceremony/v* ]]; then
    printf '%s\n' "${version}"
    return 0
  fi
  if [[ "${version}" == v* ]]; then
    printf 'ceremony/%s\n' "${version}"
    return 0
  fi
  printf 'ceremony/v%s\n' "${version}"
}

ensure_release_download_deps() {
  if [[ "$(uname -s)" == "Linux" ]]; then
    ensure_cmd_or_install_apt curl curl ca-certificates
    ensure_cmd_or_install_apt tar tar
  else
    require_cmd curl
    require_cmd tar
  fi
  if command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || command -v openssl >/dev/null 2>&1; then
    return 0
  fi
  log_error "missing checksum tool: need shasum, sha256sum, or openssl"
  return 1
}

resolve_latest_release_tag() {
  local releases_json
  if ! command -v python3 >/dev/null 2>&1; then
    log_warn "python3 not found. Skipping automatic GitHub release discovery."
    return 1
  fi
  releases_json="$(curl -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    -H "User-Agent: privacy-boost-ceremony-quickstart" \
    "https://api.github.com/repos/${CEREMONY_RELEASE_REPO}/releases?per_page=20" || true)"
  if [[ -z "${releases_json}" ]]; then
    log_warn "Could not query GitHub releases from ${CEREMONY_RELEASE_REPO}."
    return 1
  fi
  printf '%s' "${releases_json}" | python3 -c '
import json
import sys

releases = json.load(sys.stdin)
for release in releases:
    if release.get("draft") or release.get("prerelease"):
        continue
    tag = release.get("tag_name", "")
    if tag.startswith("ceremony/v"):
        print(tag)
        break
'
}

file_sha256() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${path}" | awk "{print \$1}"
    return 0
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${path}" | awk "{print \$1}"
    return 0
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "${path}" | awk "{print \$NF}"
    return 0
  fi
  return 1
}

verify_release_checksum() {
  local checksum_path="$1"
  local artifact_path="$2"
  local expected actual
  expected="$(awk "NR==1 {print \$1}" "${checksum_path}")"
  actual="$(file_sha256 "${artifact_path}")"
  if [[ -z "${expected}" || -z "${actual}" ]]; then
    log_error "checksum verification failed: missing digest"
    return 1
  fi
  if [[ "${expected,,}" != "${actual,,}" ]]; then
    log_error "checksum verification failed for $(basename "${artifact_path}")"
    return 1
  fi
  return 0
}

download_ceremony_binary_release() {
  local release_tag goos goarch asset_name base_url tmpdir
  ensure_release_download_deps || return 1

  if release_requested_explicitly; then
    release_tag="$(normalize_release_tag "${CEREMONY_RELEASE_VERSION}")" || return 1
    log "Using requested ceremony release: ${release_tag}"
  else
    log "Checking GitHub for a prebuilt ceremony release."
    release_tag="$(resolve_latest_release_tag || true)"
    if [[ -z "${release_tag}" ]]; then
      log_warn "No stable ceremony release found in ${CEREMONY_RELEASE_REPO}. Continuing with local setup."
      return 1
    fi
    log "Found ceremony release: ${release_tag}"
  fi

  read -r goos goarch < <(detect_goos_goarch)
  asset_name="ceremony-${goos}-${goarch}.tar.gz"
  base_url="https://github.com/${CEREMONY_RELEASE_REPO}/releases/download/${release_tag}"
  tmpdir="$(mktemp -d)"

  log "Downloading ${asset_name}."
  if ! curl -fL --progress-bar "${base_url}/${asset_name}" -o "${tmpdir}/${asset_name}"; then
    rm -rf "${tmpdir}"
    log_warn "Could not download ${asset_name} from ${release_tag}."
    return 1
  fi

  log "Downloading ${asset_name}.sha256."
  if ! curl -fL --progress-bar "${base_url}/${asset_name}.sha256" -o "${tmpdir}/${asset_name}.sha256"; then
    rm -rf "${tmpdir}"
    log_warn "Could not download checksum for ${asset_name}."
    return 1
  fi

  log "Verifying ${asset_name} checksum."
  if ! verify_release_checksum "${tmpdir}/${asset_name}.sha256" "${tmpdir}/${asset_name}"; then
    rm -rf "${tmpdir}"
    return 1
  fi

  log "Extracting ${asset_name} into ./bin."
  mkdir -p bin
  tar -xzf "${tmpdir}/${asset_name}" -C "${tmpdir}"
  if [[ ! -f "${tmpdir}/ceremony" ]]; then
    rm -rf "${tmpdir}"
    log_error "release asset ${asset_name} did not contain a ceremony binary"
    return 1
  fi
  cp "${tmpdir}/ceremony" "${REPO_ROOT}/bin/ceremony"
  chmod 0755 "${REPO_ROOT}/bin/ceremony"
  CEREMONY_BIN="${REPO_ROOT}/bin/ceremony"
  rm -rf "${tmpdir}"
  log_done "Prebuilt ceremony binary download"
  return 0
}

last_glob_match() {
  local pattern="$1"
  local candidate=""
  local match
  while IFS= read -r match; do
    candidate="${match}"
  done < <(compgen -G "${pattern}" || true)
  if [[ -n "${candidate}" && -x "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi
  return 1
}

resolve_go_from_user_shell() {
  local shell_path candidate line
  shell_path="${SHELL:-}"
  if [[ -z "${shell_path}" || ! -x "${shell_path}" ]]; then
    return 1
  fi
  candidate=""
  while IFS= read -r line; do
    line="${line%$'\r'}"
    if [[ -n "${line}" && -x "${line}" ]]; then
      candidate="${line}"
    fi
  done < <("${shell_path}" -ilc 'command -v go 2>/dev/null || true' 2>/dev/null || true)
  if [[ -n "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi
  return 1
}

resolve_go_from_mise() {
  local mise_bin candidate
  mise_bin=""
  if command -v mise >/dev/null 2>&1; then
    mise_bin="$(command -v mise)"
  elif [[ -x "${HOME}/.local/bin/mise" ]]; then
    mise_bin="${HOME}/.local/bin/mise"
  fi
  if [[ -n "${mise_bin}" ]]; then
    candidate="$("${mise_bin}" which go 2>/dev/null || true)"
    if [[ -n "${candidate}" && -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  fi
  last_glob_match "${HOME}/.local/share/mise/installs/go/*/bin/go"
}

resolve_go_from_asdf() {
  local candidate
  if command -v asdf >/dev/null 2>&1; then
    candidate="$(asdf which golang 2>/dev/null || true)"
    if [[ -z "${candidate}" ]]; then
      candidate="$(asdf which go 2>/dev/null || true)"
    fi
    if [[ -n "${candidate}" && -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  fi
  last_glob_match "${HOME}/.asdf/installs/golang/*/bin/go" || \
    last_glob_match "${HOME}/.asdf/installs/go/*/bin/go"
}

resolve_go_from_homebrew() {
  local prefix candidate
  if command -v brew >/dev/null 2>&1; then
    prefix="$(brew --prefix go 2>/dev/null || true)"
    for candidate in "${prefix}/libexec/bin/go" "${prefix}/bin/go"; do
      if [[ -x "${candidate}" ]]; then
        printf '%s\n' "${candidate}"
        return 0
      fi
    done
  fi
  for candidate in \
    /opt/homebrew/opt/go/libexec/bin/go \
    /usr/local/opt/go/libexec/bin/go \
    /usr/local/go/bin/go; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

discover_go() {
  local candidate=""
  candidate="$(command -v go 2>/dev/null || true)"
  if [[ -n "${candidate}" && -x "${candidate}" ]]; then
    GO_BIN="${candidate}"
    GO_SOURCE="PATH"
    return 0
  fi

  candidate="$(resolve_go_from_mise || true)"
  if [[ -n "${candidate}" ]]; then
    GO_BIN="${candidate}"
    GO_SOURCE="mise"
    return 0
  fi

  candidate="$(resolve_go_from_asdf || true)"
  if [[ -n "${candidate}" ]]; then
    GO_BIN="${candidate}"
    GO_SOURCE="asdf"
    return 0
  fi

  candidate="$(resolve_go_from_homebrew || true)"
  if [[ -n "${candidate}" ]]; then
    GO_BIN="${candidate}"
    GO_SOURCE="homebrew"
    return 0
  fi

  candidate="$(resolve_go_from_user_shell || true)"
  if [[ -n "${candidate}" ]]; then
    GO_BIN="${candidate}"
    GO_SOURCE="user shell"
    return 0
  fi

  return 1
}

use_go_bin() {
  local version
  export PATH="$(dirname "${GO_BIN}"):${PATH}"
  version="$("${GO_BIN}" version 2>/dev/null || true)"
  if [[ -n "${version}" ]]; then
    log "Using Go from ${GO_SOURCE}: ${version}"
  else
    log "Using Go from ${GO_SOURCE}: ${GO_BIN}"
  fi
}

ensure_local_go() {
  local tools_dir="${REPO_ROOT}/.tools"
  local go_root="${tools_dir}/go${GO_VERSION}"
  local local_go_bin="${go_root}/go/bin/go"

  if [[ -x "${local_go_bin}" ]]; then
    GO_BIN="${local_go_bin}"
    GO_SOURCE="repo-local cache"
    use_go_bin
    return 0
  fi

  if [[ "$(uname -s)" == "Linux" ]]; then
    ensure_cmd_or_install_apt curl curl ca-certificates
    ensure_cmd_or_install_apt tar tar
  else
    require_cmd curl
    require_cmd tar
  fi

  mkdir -p "${tools_dir}"
  read -r goos goarch < <(detect_goos_goarch)
  local tarball="go${GO_VERSION}.${goos}-${goarch}.tar.gz"
  local url="https://go.dev/dl/${tarball}"
  log "Downloading Go ${GO_VERSION} (${goos}-${goarch}) into ./.tools."
  curl -fL --progress-bar "${url}" -o "${tools_dir}/${tarball}"
  log "Extracting Go ${GO_VERSION}."
  rm -rf "${tools_dir}/go" "${go_root}"
  tar -C "${tools_dir}" -xzf "${tools_dir}/${tarball}"
  mv "${tools_dir}/go" "${go_root}"
  GO_BIN="${local_go_bin}"
  GO_SOURCE="repo-local download"
  use_go_bin
  log_done "Local Go installation"
}

ensure_executable_file() {
  local path="$1"
  if [[ -f "${path}" && ! -x "${path}" ]]; then
    chmod +x "${path}" 2>/dev/null || true
  fi
  [[ -x "${path}" ]]
}

use_provided_binary() {
  if [[ -z "${CEREMONY_BIN}" ]]; then
    return 1
  fi
  if ! ensure_executable_file "${CEREMONY_BIN}"; then
    log_error "ceremony binary is not executable: ${CEREMONY_BIN}"
    exit 1
  fi
  log "Using provided ceremony binary: ${CEREMONY_BIN}"
  return 0
}

use_existing_repo_binary() {
  local candidate="${REPO_ROOT}/bin/ceremony"
  if ! ensure_executable_file "${candidate}"; then
    return 1
  fi
  CEREMONY_BIN="${candidate}"
  log "Using existing ceremony binary: ${CEREMONY_BIN}"
  return 0
}

build_ceremony_binary_local() {
  if [[ -z "${GO_BIN}" ]]; then
    log_error "internal error: GO_BIN is empty before local build"
    exit 1
  fi
  mkdir -p bin
  log "Building ceremony CLI with local Go."
  "${GO_BIN}" build -o ./bin/ceremony ./cmd/ceremony
  CEREMONY_BIN="${REPO_ROOT}/bin/ceremony"
  if ! ensure_executable_file "${CEREMONY_BIN}"; then
    log_error "local build did not produce an executable binary at ${CEREMONY_BIN}"
    exit 1
  fi
  log_done "Local ceremony build"
}

build_ceremony_binary_docker() {
  require_cmd docker
  mkdir -p bin
  log "Building ceremony CLI with Docker image ${DOCKER_IMAGE}."
  docker run --rm \
    -u "$(id -u):$(id -g)" \
    -v "${REPO_ROOT}:/work" \
    -w /work \
    "${DOCKER_IMAGE}" \
    sh -lc 'go build -o ./bin/ceremony ./cmd/ceremony'
  CEREMONY_BIN="${REPO_ROOT}/bin/ceremony"
  if ! ensure_executable_file "${CEREMONY_BIN}"; then
    log_error "Docker build did not produce an executable binary at ${CEREMONY_BIN}"
    exit 1
  fi
  log_done "Docker ceremony build"
}

prepare_ceremony_binary() {
  if use_provided_binary; then
    return 0
  fi

  if release_requested_explicitly; then
    if download_ceremony_binary_release; then
      return 0
    fi
    log_error "failed to download requested ceremony release: ${CEREMONY_RELEASE_VERSION}"
    exit 1
  fi

  if [[ "${BUILD_MODE}" == "auto" ]] && use_existing_repo_binary; then
    return 0
  fi

  case "${BUILD_MODE}" in
    auto)
      if download_ceremony_binary_release; then
        return 0
      fi
      if discover_go; then
        use_go_bin
        build_ceremony_binary_local
        return 0
      fi
      if command -v docker >/dev/null 2>&1; then
        log "No local Go detected. Falling back to Docker build."
        build_ceremony_binary_docker
        return 0
      fi
      log_warn "No local Go or Docker detected. Falling back to a repo-local Go download."
      ensure_local_go
      build_ceremony_binary_local
      ;;
    local)
      if discover_go; then
        use_go_bin
      else
        log_warn "No local Go detected. Falling back to a repo-local Go download."
        ensure_local_go
      fi
      build_ceremony_binary_local
      ;;
    docker)
      build_ceremony_binary_docker
      ;;
  esac
}

extract_state_dir() {
  # Prefer python3 for correct JSON parsing.
  if command -v python3 >/dev/null 2>&1; then
    python3 - "${CONFIG_PATH}" <<'PY'
import json, sys
with open(sys.argv[1], "r", encoding="utf-8") as f:
    cfg = json.load(f)
print(cfg.get("stateDir", ""))
PY
    return 0
  fi

  # Fallback: best-effort extraction for our known, formatted config files.
  local line
  line="$(grep -E '"stateDir"\\s*:' "${CONFIG_PATH}" | head -n 1 || true)"
  if [[ -z "${line}" ]]; then
    echo ""
    return 0
  fi
  echo "${line}" | sed -E 's/.*"stateDir"\\s*:\\s*"([^"]+)".*/\\1/'
}

prepare_ceremony_binary

STATE_DIR="$(extract_state_dir)"
if [[ -z "${STATE_DIR}" ]]; then
  log_error "could not read stateDir from config: ${CONFIG_PATH}"
  exit 1
fi
log "Ensuring state directory exists: ${STATE_DIR}"
mkdir -p "${STATE_DIR}"
log_done "State directory setup"

log "Starting contribution against ${COORDINATOR_URL}."
ARGS=("${CEREMONY_BIN}" contribute --config "${CONFIG_PATH}" --coordinator-url "${COORDINATOR_URL}")
if [[ -n "${QUIET}" ]]; then
  ARGS+=(--quiet)
fi
"${ARGS[@]}"
log_done "Contribution flow"
