---
match: cd.*/code/52/cc-relayer\s*(&&|\s).*deploy
action: inject
---
# Deploy script must run from current worktree, not main repo

## Symptom
`deploy.sh` builds and deploys old code from `main` branch instead of the worktree's changes. The deploy "succeeds" but none of your changes take effect.

## Root Cause
`deploy.sh` uses `git rev-parse --show-toplevel` to determine the repo root. When you `cd /path/to/main-repo && bash .claude/skills/deploy/scripts/deploy.sh`, the script resolves to the main repo root and compiles from there — ignoring the worktree branch entirely.

## Correct Approach
Always run deploy from the worktree directory (or any subdirectory of it):

```bash
# WRONG — compiles main branch
cd /Users/yansir/code/52/cc-relayer && bash .claude/skills/deploy/scripts/deploy.sh

# RIGHT — compiles worktree branch
bash /Users/yansir/code/52/cc-relayer/.claude/skills/deploy/scripts/deploy.sh
# (runs from current cwd which is the worktree)
```

The key: don't `cd` to the main repo before invoking the script. Let the script's `git rev-parse --show-toplevel` resolve from your current working directory.
