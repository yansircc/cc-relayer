#!/usr/bin/env bash
set -euo pipefail

# ── Config ──────────────────────────────────────────────
REMOTE="root@cc.210k.cc"
REMOTE_BIN="/usr/local/bin/cc-relayer"
SERVICE="cc-relayer"
TMP_LOCAL="/tmp/cc-relayer-new"
TMP_REMOTE="/tmp/cc-relayer-new"

# Find repo root (works from worktrees too)
REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

echo "==> repo: $REPO_ROOT"

# ── 1. Frontend build ──────────────────────────────────
if [[ "${SKIP_FRONTEND:-}" != "1" ]]; then
    echo "==> building frontend..."
    (cd web && npm run build --silent 2>&1) | tail -1
    echo "    done"
else
    echo "==> skipping frontend build (SKIP_FRONTEND=1)"
fi

# ── 2. Go cross-compile ────────────────────────────────
echo "==> compiling linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$TMP_LOCAL" ./cmd/relay/
SIZE=$(du -h "$TMP_LOCAL" | cut -f1 | xargs)
echo "    done ($SIZE)"

# ── 3. Upload ──────────────────────────────────────────
echo "==> uploading to $REMOTE..."
scp -q "$TMP_LOCAL" "$REMOTE:$TMP_REMOTE"
echo "    done"

# ── 4. Atomic replace + restart ────────────────────────
echo "==> replacing binary and restarting..."
ssh "$REMOTE" "
    chmod +x $TMP_REMOTE
    mv $TMP_REMOTE $REMOTE_BIN
    systemctl restart $SERVICE
"
echo "    done"

# ── 5. Verify ──────────────────────────────────────────
echo "==> verifying..."
sleep 2
STATUS=$(ssh "$REMOTE" "systemctl is-active $SERVICE 2>/dev/null || true")
if [[ "$STATUS" != "active" ]]; then
    echo "    FAIL: service is $STATUS"
    ssh "$REMOTE" "journalctl -u $SERVICE -n 15 --no-pager"
    exit 1
fi

# Show restart timing
ssh "$REMOTE" "journalctl -u $SERVICE --since '2 minutes ago' --no-pager -o short-precise" \
    | grep -E '(Stopping|Stopped|Started|server starting)' || true

echo ""
echo "==> deployed successfully"

# Clean up local temp
rm -f "$TMP_LOCAL"
