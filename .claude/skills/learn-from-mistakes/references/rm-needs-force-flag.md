---
match: \brm\b(?!.*-[a-zA-Z]*f)
action: inject
---
# rm command needs -f flag in sandboxed/automated contexts

When using `rm` to delete files in Bash tool calls, the sandbox may prompt for confirmation which hangs the command. Always use `rm -f` (or `rm -rf` for directories) to avoid interactive prompts.

Symptom: `rm` command gets rejected or hangs waiting for confirmation.
Fix: Use `rm -f` for files, `rm -rf` for directories.
