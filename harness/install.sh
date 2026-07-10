#!/usr/bin/env bash
# install.sh — one-shot installation of migration-harness.
#
# What it does:
#   1. Verifies goose, jq, git, go, graphify are installed.
#   2. Builds the Go binary from cmd/migration-harness/.
#   3. Installs the binary and data to ~/.migration-harness/install/
#   4. Symlinks ~/.local/bin/migration-harness → that binary
#   5. Installs the goose-migration skill bundle to ~/.config/goose/skills/
#   6. Prints next steps.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

C_G='\033[0;32m'; C_R='\033[0;31m'; C_Y='\033[1;33m'; C_B='\033[0;34m'; C_X='\033[0m'
ok()    { printf "${C_G}✓${C_X} %s\n" "$*"; }
warn()  { printf "${C_Y}⚠${C_X} %s\n" "$*"; }
err()   { printf "${C_R}✗${C_X} %s\n" "$*"; exit 1; }
info()  { printf "${C_B}→${C_X} %s\n" "$*"; }

# ── 1. Dependency check ─────────────────────────────────────────
info "Checking dependencies"
command -v goose >/dev/null 2>&1 || err "goose is not installed. https://block.github.io/goose/docs/getting-started/installation"
command -v jq    >/dev/null 2>&1 || err "jq is not installed (brew install jq | apt install jq)"
command -v git   >/dev/null 2>&1 || err "git is not installed"

# Check for graphify (uses claude code skill)
if ! command -v graphify >/dev/null 2>&1; then
  warn "graphify not found - attempting to install via uv"
  if command -v uv >/dev/null 2>&1; then
    info "Installing graphify via uv tool..."
    uv tool install --upgrade graphifyy || err "Failed to install graphify. Install manually: pip install graphifyy"
    ok "graphify installed via uv"
  else
    warn "uv not found - trying pip"
    if python3 -m pip install graphifyy 2>/dev/null || python3 -m pip install graphifyy --break-system-packages 2>/dev/null; then
      ok "graphify installed via pip"
    else
      err "Failed to install graphify. Install uv or pip, then run: pip install graphifyy"
    fi
  fi
else
  ok "graphify is available"
fi

ok "Dependencies present"

# ── 2. Build Go binary ──────────────────────────────────────────
command -v go >/dev/null 2>&1 || err "go is not installed (required to build migration-harness)"
info "Building migration-harness from source"
(cd "$SCRIPT_DIR" && go build -o migration-harness ./cmd/migration-harness) || err "Go build failed"
ok "Binary built"

# ── 3. Install payload ─────────────────────────────────────────
INSTALL_DIR="$HOME/.migration-harness/install"
info "Installing payload to $INSTALL_DIR"
mkdir -p "$INSTALL_DIR/bin"
rm -rf "$INSTALL_DIR"/{bin,recipes,skill-bundle}
mkdir -p "$INSTALL_DIR/bin"
cp "$SCRIPT_DIR/migration-harness" "$INSTALL_DIR/bin/"
cp -r "$SCRIPT_DIR/recipes"        "$INSTALL_DIR/"
cp -r "$SCRIPT_DIR/skill-bundle"   "$INSTALL_DIR/"
chmod +x "$INSTALL_DIR/bin/migration-harness"
rm -f "$SCRIPT_DIR/migration-harness"
ok "Payload installed"

# ── 4. Symlink the command ──────────────────────────────────────
BIN_DIR="$HOME/.local/bin"
mkdir -p "$BIN_DIR"
ln -sf "$INSTALL_DIR/bin/migration-harness" "$BIN_DIR/migration-harness"
ok "Symlink: $BIN_DIR/migration-harness → $INSTALL_DIR/bin/migration-harness"

# ── 5. Install skill bundle ─────────────────────────────────────
SKILLS_DIR="${GOOSE_SKILLS_DIR:-$HOME/.config/goose/skills}"
info "Installing skill bundle to $SKILLS_DIR/goose-migration"
mkdir -p "$SKILLS_DIR"
rm -rf "$SKILLS_DIR/goose-migration"
cp -r "$SCRIPT_DIR/skill-bundle/goose-migration" "$SKILLS_DIR/"
ok "Skill bundle installed"

# ── 6. PATH check ───────────────────────────────────────────────
if ! echo "$PATH" | tr ':' '\n' | grep -qx "$BIN_DIR"; then
  warn "$BIN_DIR is not in your PATH"
  echo
  echo "  Add this to ~/.bashrc or ~/.zshrc:"
  echo "    export PATH=\"\$HOME/.local/bin:\$PATH\""
  echo
  echo "  Then run:  source ~/.bashrc   (or open a new terminal)"
fi

echo
ok "Installation complete"
echo
echo "Dependencies verified:"
echo "  ✓ goose       (migration orchestrator)"
echo "  ✓ graphify    (code graph analysis)"
echo "  ✓ jq          (JSON processing)"
echo "  ✓ git         (version control)"
echo
echo "Next steps:"
echo "  1.  migration-harness init                              # Configure which model to use"
echo "  2.  migration-harness /path/to/your/app \"Migrate X to Y\""
echo
echo "Note: migration-harness config is separate from goose config."
echo "      You can use a different (e.g., more powerful) model for migrations."
