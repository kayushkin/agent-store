#!/usr/bin/env bash
set -euo pipefail

# Mirrors skill-store/deploy.sh: builds the cmd/server binary, drops it
# into ~/bin/agent-store, and bounces the user systemd unit. Same DBus
# env shim so the script works from non-login shells (Claude, automation).

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$HOME/bin"
SERVICE="agent-store.service"
BINARY="agent-store"

cd "$REPO_DIR"

export PATH="$HOME/.local/share/mise/shims:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=${XDG_RUNTIME_DIR}/bus}"

echo "==> Building $BINARY..."
go build -o "$BINARY" ./cmd/server
echo "    built: $(ls -lh "$BINARY" | awk '{print $5}')"

echo "==> Stopping $SERVICE..."
systemctl --user stop "$SERVICE" 2>/dev/null || true
sleep 1

echo "==> Installing binary to $BIN_DIR..."
mkdir -p "$BIN_DIR"
cp "$BINARY" "$BIN_DIR/$BINARY"

echo "==> Starting $SERVICE..."
systemctl --user daemon-reload
systemctl --user start "$SERVICE"

echo "==> Verifying..."
sleep 2
if systemctl --user is-active --quiet "$SERVICE"; then
  echo "    $SERVICE is running"
  journalctl --user -u "$SERVICE" -n 5 --no-pager 2>&1 | grep -v '^--' || true
else
  echo "ERROR: $SERVICE failed to start"
  journalctl --user -u "$SERVICE" -n 20 --no-pager 2>&1
  exit 1
fi

echo "==> Smoke test..."
if ! curl -fsS http://localhost:8300/agents/health >/dev/null 2>&1; then
  echo "ERROR: $SERVICE not responding on :8300/agents/health"
  journalctl --user -u "$SERVICE" -n 30 --no-pager 2>&1
  exit 1
fi
echo "    smoke test OK"

echo "==> Done."
