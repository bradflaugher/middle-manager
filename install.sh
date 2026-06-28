#!/usr/bin/env bash
# middle-manager installer.
#
# Prefers a prebuilt binary from the latest GitHub release (no Go required) and
# falls back to building from source if Go is available.
set -euo pipefail

REPO_SLUG="${MM_REPO_SLUG:-bradflaugher/middle-manager}"
REPO_URL="${MM_REPO:-https://github.com/${REPO_SLUG}.git}"
INSTALL_DIR="${MM_INSTALL_DIR:-${HOME}/.local/share/middle-manager}"
BIN_DIR="${MM_BIN_DIR:-${HOME}/.local/bin}"
BRANCH="${MM_BRANCH:-main}"

mkdir -p "${BIN_DIR}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
esac
asset="mm_${os}_${arch}"
[ "${os}" = "windows" ] && asset="${asset}.exe"

echo "middle-manager installer"
echo "  platform: ${os}/${arch}"
echo "  bin:      ${BIN_DIR}/mm"
echo

# ---------------------------------------------------------------------------
# 1) Prebuilt binary from the latest GitHub release (no Go toolchain needed).
# ---------------------------------------------------------------------------
install_prebuilt() {
  command -v curl >/dev/null 2>&1 || return 1
  local api url cfg
  api="https://api.github.com/repos/${REPO_SLUG}/releases/latest"
  url="$(curl -fsSL "${api}" 2>/dev/null \
        | grep -oE "https://[^\"[:space:]]*/${asset}([\"]|$)" \
        | tr -d '"' | head -1 || true)"
  [ -n "${url}" ] || return 1

  echo "Downloading prebuilt binary:"
  echo "  ${url}"
  curl -fsSL "${url}" -o "${BIN_DIR}/mm" || return 1
  chmod +x "${BIN_DIR}/mm"

  # Best-effort: grab config.default.json from the same release.
  cfg="$(echo "${url}" | sed "s#/${asset}\$#/config.default.json#")"
  curl -fsSL "${cfg}" -o "${BIN_DIR}/config.default.json" 2>/dev/null || true
  return 0
}

# ---------------------------------------------------------------------------
# 2) Build from source (requires Go 1.25+).
# ---------------------------------------------------------------------------
install_from_source() {
  if ! command -v go >/dev/null 2>&1; then
    cat >&2 <<EOF
No prebuilt binary was found for ${os}/${arch}, and Go is not installed.

Options:
  • Install Go 1.25+ and re-run this script:  https://go.dev/doc/install
  • Or download a binary directly from:        https://github.com/${REPO_SLUG}/releases
EOF
    return 1
  fi

  echo "Building from source with Go ($(go version | awk '{print $3}'))..."
  if [ -d "${INSTALL_DIR}/.git" ]; then
    git -C "${INSTALL_DIR}" fetch origin "${BRANCH}" --quiet
    git -C "${INSTALL_DIR}" checkout "${BRANCH}" --quiet
    git -C "${INSTALL_DIR}" pull --ff-only origin "${BRANCH}" --quiet || true
  elif [ -f "./main.go" ]; then
    mkdir -p "${INSTALL_DIR}"; cp -r ./* "${INSTALL_DIR}/"
  else
    git clone --depth 1 --branch "${BRANCH}" "${REPO_URL}" "${INSTALL_DIR}"
  fi
  ( cd "${INSTALL_DIR}"
    rm -f "${BIN_DIR}/mm"
    go build -o "${BIN_DIR}/mm" .
    [ -f config.default.json ] && cp -f config.default.json "${BIN_DIR}/config.default.json" || true )
  return 0
}

if install_prebuilt; then
  echo "✓ Installed prebuilt binary."
elif install_from_source; then
  echo "✓ Built and installed from source."
else
  exit 1
fi

mkdir -p "${XDG_CONFIG_HOME:-${HOME}/.config}/middle-manager"

if ! echo ":${PATH}:" | grep -q ":${BIN_DIR}:"; then
  echo
  echo "Add ${BIN_DIR} to your PATH:"
  echo "  export PATH=\"${BIN_DIR}:\$PATH\""
fi

echo
"${BIN_DIR}/mm" --version >/dev/null 2>&1 && "${BIN_DIR}/mm" --version || true
echo "✓ Installed. Run: mm        (or: mm agents / mm merge --dry-run)"
