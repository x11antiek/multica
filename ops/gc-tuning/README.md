# Multica cache-only GC policy (macOS)

This profile reclaims rebuildable artifacts while preserving task workspaces,
unpushed source, logs, outputs, provider conversations and repository history.
Install a daemon build supporting `MULTICA_GC_ARTIFACTS_ONLY` and the machine policy file before applying it.
Older builds ignore that flag; the long terminal TTL and disabled completed-task
and Codex-session TTLs are not a substitute for the cache-only mode.

## Policy

| Variable | Value | Effect |
|---|---|---|
| `MULTICA_GC_ARTIFACTS_ONLY` | `true` | Skip all whole-task, orphan, repository-history, session and memory retention passes. |
| `MULTICA_GC_INTERVAL` | `10m` | Sweep frequency. |
| `MULTICA_GC_ARTIFACT_TTL` | `1h` | Reclaim artifacts from completed, inactive tasks after one hour. |
| `MULTICA_GC_MIN_FREE_PERCENT` | `15` | Reclaim eligible artifacts earlier under disk pressure. |
| `MULTICA_GC_COMPLETED_TASK_TTL` | `0` | Disable completed-workspace expiry if cache-only mode is later switched off. |
| `MULTICA_GC_TTL` | `876000h` | Preserve the previous long terminal-issue retention if cache-only mode is switched off. |
| `MULTICA_GC_CODEX_SESSION_TTL` | `0` | Disable provider transcript expiry. |

Artifact cleanup includes `node_modules`, `.next`, `.turbo`, and the exact managed
paths `codex-home/.sandbox-bin` and
`codex-home/cache/remote_plugin_catalog`. It does not delete the rest of
`codex-home/cache`, auth/config files or SQLite/session state. Unknown or
unfinished tasks stay untouched. The existing ownership and execution locks
protect active runs, including ones owned by another daemon.

## Apply and verify

```sh
./apply-gc-tuning.sh
python3 ./check-gc-tuning.py --port 19514
```

The script atomically writes `~/.multica/gc-policy.json`. Every daemon profile
reads this machine policy at startup **after** inherited environment settings.
It controls `artifacts_only`, `interval`, `artifact_ttl`, and `min_free_percent`;
unknown fields or invalid values fail startup rather than silently ignoring the
policy. The launch agent also sets conservative environment values for other
launchers. Existing processes remain unchanged until restarted.

Environment variables are snapshots taken when a process starts. `launchctl
setenv` cannot update an already-running app or daemon. Desktop also reconnects
to an existing daemon at startup: **quitting and relaunching Desktop alone is
insufficient**. Reading the policy file directly removes stale-parent-environment
and login-job-ordering problems on the next daemon start.

After active tasks finish:

1. Restart the Desktop-managed runtime explicitly using Desktop's runtime controls.
2. For a launchd-supervised CLI runtime, restart its owning launchd job; stopping
   the child directly fights `KeepAlive`.
3. Run the checker against each runtime's health port (`--port 19514` for the
   default CLI daemon). It checks the running binary's effective policy and
   catalog-cache support; it does not print unrelated environment variables.
   Older daemons without the new health fields are reported as needing an upgrade.

The `gc: started` log also reports `artifacts_only=true` and lists
`remote_plugin_catalog`. Installing files or inspecting `launchctl getenv`
alone is not proof that a running daemon uses them.

Changes to retention require an explicit policy update and runtime restart.
This profile deliberately does not restart active work or prune history to
achieve a free-space target. Source workspaces still occupy space and require
separate review before deletion.
