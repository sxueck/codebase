#!/usr/bin/env bash

# Install latest codebase release for the current macOS system and
# set up configuration under ~/.codebase/config.json (empty file by default).

set -euo pipefail

# Usage: ./install-codebase-macos.sh [INSTALL_DIR]
# Default install dir if not provided: /usr/local/codebase
INSTALL_DIR="${1:-/usr/local/codebase}"

info() {
  echo "[INFO] $*"
}

warn() {
  echo "[WARN] $*" >&2
}

error() {
  echo "[ERROR] $*" >&2
  exit 1
}

ensure_dependencies() {
  # jq is used to parse GitHub API JSON
  for cmd in curl jq tar; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      error "Required command '$cmd' not found. Please install it and retry."
    fi
  done
}

get_latest_release_asset() {
  local owner="sxueck"
  local repo="codebase"
  local api="https://api.github.com/repos/${owner}/${repo}/releases/latest"

  info "Fetching latest release info from ${api}"

  local json
  if ! json=$(curl -fsSL -H "User-Agent: codebase-installer" "${api}"); then
    error "Failed to fetch release info from GitHub."
  fi

  local arch os_tag arch_tag pattern
  arch=$(uname -m)
  os_tag="darwin|macos|mac"

  case "${arch}" in
    arm64|aarch64)
      arch_tag="arm64|aarch64"
      ;;
    x86_64)
      arch_tag="amd64|x86_64|x64"
      ;;
    *)
      arch_tag="${arch}"
      ;;
  esac

  # Primary match: OS + arch
  pattern="(?i)(${os_tag}).*(${arch_tag})"

  ASSET_NAME=$(printf '%s
' "${json}" | jq -r --arg pat "${pattern}" '.assets[] | select(.name | test($pat)) | .name' | head -n1)
  DOWNLOAD_URL=$(printf '%s
' "${json}" | jq -r --arg pat "${pattern}" '.assets[] | select(.name | test($pat)) | .browser_download_url' | head -n1)

  if [ -z "${ASSET_NAME:-}" ] || [ -z "${DOWNLOAD_URL:-}" ]; then
    # Fallback: match only OS
    pattern="(?i)(${os_tag})"
    ASSET_NAME=$(printf '%s
' "${json}" | jq -r --arg pat "${pattern}" '.assets[] | select(.name | test($pat)) | .name' | head -n1)
    DOWNLOAD_URL=$(printf '%s\n' "${json}" | jq -r --arg pat "${pattern}" '.assets[] | select(.name | test($pat)) | .browser_download_url' | head -n1)
  fi

  if [ -z "${ASSET_NAME:-}" ] || [ -z "${DOWNLOAD_URL:-}" ]; then
    error "No suitable build asset found for current macOS system."
  fi

  info "Selected asset: ${ASSET_NAME}"
}

install_codebase_binary() {
  local download_path filename candidates preferred

  mkdir -p "${INSTALL_DIR}"

  download_path=$(mktemp "/tmp/${ASSET_NAME}.XXXXXX")
  info "Downloading asset to temporary file: ${download_path}"

  if ! curl -fL -H "User-Agent: codebase-installer" -o "${download_path}" "${DOWNLOAD_URL}"; then
    rm -f "${download_path}"
    error "Failed to download asset from GitHub."
  fi

  filename="${ASSET_NAME}"

  case "${filename}" in
    *.tar.gz|*.tgz)
      info "Detected tar.gz archive, extracting to ${INSTALL_DIR}"
      tar -xzf "${download_path}" -C "${INSTALL_DIR}"
      ;;
    *.zip)
      if ! command -v unzip >/dev/null 2>&1; then
        rm -f "${download_path}"
        error "unzip not found. Please install it to handle zip archives."
      fi
      info "Detected zip archive, extracting to ${INSTALL_DIR}"
      unzip -o "${download_path}" -d "${INSTALL_DIR}" >/dev/null
      ;;
    *)
      info "Treating download as a binary, installing as codebase."
      cp "${download_path}" "${INSTALL_DIR}/codebase"
      chmod +x "${INSTALL_DIR}/codebase"
      ;;
  esac

  # Normalize to INSTALL_DIR/codebase if not already present
  if [ ! -x "${INSTALL_DIR}/codebase" ]; then
    info "Searching for executable in ${INSTALL_DIR}"
    candidates=$(find "${INSTALL_DIR}" -type f -perm -111 2>/dev/null || true)
    if [ -n "${candidates}" ]; then
      preferred=$(printf '%s\n' "${candidates}" | grep -Ei '/codebase$' | head -n 1 || true)
      if [ -z "${preferred}" ]; then
        preferred=$(printf '%s\n' "${candidates}" | head -n 1)
      fi
      info "Using executable: ${preferred}"
      cp "${preferred}" "${INSTALL_DIR}/codebase"
      chmod +x "${INSTALL_DIR}/codebase"
    else
      warn "No executable file found in ${INSTALL_DIR} after extraction."
    fi
  fi

  rm -f "${download_path}"
  info "Installation completed in directory: ${INSTALL_DIR}"
}

setup_environment() {
  # Export for the current process (does not persist across shells)
  export CODEBASE_HOME="${INSTALL_DIR}"

  # Update PATH in user shell profile so future shells pick it up.
  local shell_name profile
  shell_name=$(basename "${SHELL:-}")
  case "${shell_name}" in
    zsh)
      profile="${HOME}/.zshrc"
      ;;
    bash)
      # On macOS bash typically uses .bash_profile
      profile="${HOME}/.bash_profile"
      ;;
    *)
      profile="${HOME}/.profile"
      ;;
  esac

  info "Updating shell profile: ${profile}"
  touch "${profile}"

  if ! grep -q "CODEBASE_HOME" "${profile}" 2>/dev/null; then
    {
      echo ""
      echo "# codebase CLI configuration"
      echo "export CODEBASE_HOME=\"${INSTALL_DIR}\""
      echo 'export PATH="$PATH:$CODEBASE_HOME"'
    } >> "${profile}"
    info "Appended CODEBASE_HOME and PATH settings to ${profile}."
  else
    info "Shell profile already references CODEBASE_HOME; skipping append."
  fi

  info "Environment setup complete. Open a new terminal or run 'source "${profile}"' to apply it."
}

setup_config_file() {
  local config_dir config_path
  config_dir="${HOME}/.codebase"
  config_path="${config_dir}/config.json"

  if [ ! -d "${config_dir}" ]; then
    info "Creating config directory: ${config_dir}"
    mkdir -p "${config_dir}"
  fi

  if [ ! -f "${config_path}" ]; then
    : > "${config_path}"
    info "Created empty config file: ${config_path}"
  else
    info "Config file already exists: ${config_path}"
  fi
}

main() {
  info "Starting installation of codebase into ${INSTALL_DIR}"

  if [ "$(uname -s)" != "Darwin" ]; then
    warn "This script is intended for macOS (Darwin). Proceeding anyway, but behavior may be undefined."
  fi

  ensure_dependencies
  get_latest_release_asset
  install_codebase_binary
  setup_environment
  setup_config_file

  info "All steps completed."
}

main "$@"
