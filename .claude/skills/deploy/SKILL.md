---
name: deploy
description: |
  Build and deploy cc-relayer to production (cc.210k.cc).
  Triggers: deploy, 部署, push to production, 发布, ship it, 上线.
  Handles: frontend build, Go cross-compile, upload, atomic binary replace, systemctl restart, verification.
---

## Deploy cc-relayer

Run `scripts/deploy.sh` from the repo root (or any worktree). The script handles the full pipeline:

1. Build SvelteKit frontend (`web/` → `internal/ui/dist`)
2. Cross-compile Go binary for linux/amd64
3. Upload to remote via scp
4. Atomic replace (`mv`) + `systemctl restart`
5. Verify service health and print restart timing

```bash
bash .claude/skills/deploy/scripts/deploy.sh
```

If the script fails at any step it exits immediately with context. Common issues:

- **npm build fails** — check `web/` for TypeScript errors
- **go build fails** — run `go vet ./...` first
- **scp fails** — check SSH key / connectivity to cc.210k.cc
- **service fails to start** — `ssh root@cc.210k.cc journalctl -u cc-relayer -n 30 --no-pager`

### Partial deploy

To skip frontend build (Go-only change):

```bash
SKIP_FRONTEND=1 bash .claude/skills/deploy/scripts/deploy.sh
```
