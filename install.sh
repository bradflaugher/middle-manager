#!/usr/bin/env bash
# middle-manager Go installer — pure bash + Go compiler
set -euo pipefail

REPO_URL="${MM_REPO:-https://github.com/bradflaugher/middle-manager.git}"
INSTALL_DIR="${MM_INSTALL_DIR:-${HOME}/.local/share/middle-manager}"
BIN_DIR="${MM_BIN_DIR:-${HOME}/.local/bin}"
BRANCH="${MM_BRANCH:-main}"

echo "middle-manager Go installer"
echo "  repo:    ${REPO_URL}"
echo "  install: ${INSTALL_DIR}"
echo "  bin:     ${BIN_DIR}/mm"
echo

mkdir -p "${BIN_DIR}" "${INSTALL_DIR}"

if [[ -d "${INSTALL_DIR}/.git" ]]; then
  echo "Updating existing install..."
  git -C "${INSTALL_DIR}" fetch origin "${BRANCH}"
  git -C "${INSTALL_DIR}" checkout "${BRANCH}"
  git -C "${INSTALL_DIR}" pull --ff-only origin "${BRANCH}" || true
else
  # Check if installing from current local dir
  if [[ -f "./main.go" ]]; then
    echo "Copying local files..."
    cp -r ./* "${INSTALL_DIR}/"
  else
    echo "Cloning..."
    git clone --depth 1 --branch "${BRANCH}" "${REPO_URL}" "${INSTALL_DIR}"
  fi
fi

# Verify Go installation
if ! command -v go &>/dev/null; then
  echo "Error: Go compiler is not installed."
  echo "Please install Go: https://go.dev/doc/install"
  exit 1
fi

echo "Compiling middle-manager in Go..."
(
  cd "${INSTALL_DIR}"
  rm -f "${BIN_DIR}/mm"
  go build -o "${BIN_DIR}/mm" .
  # Ship the default loop config next to the binary so `mm` finds it anywhere.
  [[ -f config.default.json ]] && cp -f config.default.json "${BIN_DIR}/config.default.json" || true
)

# User config dir
mkdir -p "${XDG_CONFIG_HOME:-${HOME}/.config}/middle-manager"

if ! echo ":${PATH}:" | grep -q ":${BIN_DIR}:"; then
  echo
  echo "Add ${BIN_DIR} to your PATH:"
  echo "  export PATH=\"${BIN_DIR}:\$PATH\""
  echo
  echo "Or re-run: mm install-path"
fi

echo
echo "✓ Installed. Run: mm"
echo "  (or: mm agents / mm --help)"
echo