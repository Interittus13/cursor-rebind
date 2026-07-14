#!/usr/bin/env bash
# Install cursor-rebind from GitHub Releases onto PATH.
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Interittus13/cursor-rebind/main/scripts/install.sh | bash
#   CURSOR_REBIND_VERSION=v1.0.0 ./scripts/install.sh
set -euo pipefail

REPO="${CURSOR_REBIND_REPO:-Interittus13/cursor-rebind}"
VERSION="${CURSOR_REBIND_VERSION:-}"
INSTALL_DIR="${CURSOR_REBIND_INSTALL_DIR:-}"
BIN_NAME="cursor-rebind"

if [[ -n "${INSTALL_DIR}" ]]; then
  :
elif [[ -w /usr/local/bin ]] || [[ "$(id -u)" -eq 0 ]]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
fi

die() {
  echo "error: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required"
}

need curl
need tar
need mktemp
need uname

os="$(uname -s)"
arch="$(uname -m)"

case "${os}" in
  Linux)  os_title="Linux" ;;
  Darwin) os_title="Darwin" ;;
  MINGW*|MSYS*|CYGWIN*)
    die "use the Windows .zip from GitHub Releases, or run via WSL"
    ;;
  *)
    die "unsupported OS: ${os}"
    ;;
esac

case "${arch}" in
  x86_64|amd64) arch_name="x86_64" ;;
  arm64|aarch64) arch_name="arm64" ;;
  *)
    die "unsupported architecture: ${arch}"
    ;;
esac

asset="${BIN_NAME}_${os_title}_${arch_name}.tar.gz"
base_url="https://github.com/${REPO}/releases"

if [[ -z "${VERSION}" || "${VERSION}" == "latest" ]]; then
  download_url="${base_url}/latest/download/${asset}"
  checksum_url="${base_url}/latest/download/checksums.txt"
  version_label="latest"
else
  # Accept v1.0.0 or 1.0.0
  tag="${VERSION}"
  [[ "${tag}" == v* ]] || tag="v${tag}"
  download_url="${base_url}/download/${tag}/${asset}"
  checksum_url="${base_url}/download/${tag}/checksums.txt"
  version_label="${tag}"
fi

tmpdir="$(mktemp -d)"
cleanup() { rm -rf "${tmpdir}"; }
trap cleanup EXIT

archive="${tmpdir}/${asset}"
echo "Downloading cursor-rebind (${version_label}) for ${os_title}/${arch_name}..."
if ! curl -fsSL "${download_url}" -o "${archive}"; then
  die "download failed (${download_url}). Check that a release exists for this platform."
fi

# Best-effort checksum verification
checksums="${tmpdir}/checksums.txt"
if curl -fsSL "${checksum_url}" -o "${checksums}" 2>/dev/null; then
  expect="$(awk -v f="${asset}" '$2 == f { print $1; exit }' "${checksums}" || true)"
  if [[ -n "${expect}" ]]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got="$(sha256sum "${archive}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
      got="$(shasum -a 256 "${archive}" | awk '{print $1}')"
    else
      got=""
    fi
    if [[ -n "${got}" && "${got}" != "${expect}" ]]; then
      die "checksum mismatch for ${asset}"
    fi
    if [[ -n "${got}" ]]; then
      echo "Checksum OK"
    fi
  fi
fi

tar -xzf "${archive}" -C "${tmpdir}"
src="${tmpdir}/${BIN_NAME}"
[[ -f "${src}" ]] || die "archive did not contain ${BIN_NAME}"

mkdir -p "${INSTALL_DIR}"
install -m 755 "${src}" "${INSTALL_DIR}/${BIN_NAME}"

echo "Installed ${INSTALL_DIR}/${BIN_NAME}"

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    echo "Ready. Try: cursor-rebind version"
    ;;
  *)
    echo
    echo "Add this to your shell profile, then open a new terminal:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo
    echo "Or run directly: ${INSTALL_DIR}/${BIN_NAME} version"
    ;;
esac
