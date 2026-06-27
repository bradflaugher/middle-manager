#!/usr/bin/env bash
# middle-manager installer — pure bash, no dependencies beyond git + python3
set -euo pipefail

REPO_URL="${MM_REPO:-https://github.com/bradflaugher/middle-manager.git}"
INSTALL_DIR="${MM_INSTALL_DIR:-${HOME}/.local/share/middle-manager}"
BIN_DIR="${MM_BIN_DIR:-${HOME}/.local/bin}"
BRANCH="${MM_BRANCH:-main}"

echo "middle-manager installer"
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
  echo "Cloning..."
  git clone --depth 1 --branch "${BRANCH}" "${REPO_URL}" "${INSTALL_DIR}"
fi

# Wrapper script — avoids PYTHONPATH hacks
cat > "${BIN_DIR}/mm" <<EOF
#!/usr/bin/env bash
set -euo pipefail
MM_HOME="${INSTALL_DIR}"
export PYTHONPATH="\${MM_HOME}:\${PYTHONPATH:-}"
exec python3 "\${MM_HOME}/mm.py" "\$@"
EOF

chmod +x "${BIN_DIR}/mm" "${INSTALL_DIR}/mm.py"

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
echo "One-liner for friends:"
echo "  curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash"