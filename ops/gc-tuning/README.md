# Multica GC tuning (launchd)

Per-machine operational config that tames the Multica daemon's garbage
collector so it reclaims agent workdirs proactively instead of hoarding them
until the disk is nearly full.

## Why

The daemon reads its GC policy from `MULTICA_GC_*` environment variables. On
self-hosted v0.5 builds, completed-task eviction is disabled by default and
artifact cleanup runs on a two-hour cycle with a 12-hour artifact TTL. This
fork also retains provider transcripts unless an operator opts into pruning.
Those conservative defaults preserve auditability, but per-task
`node_modules` can accumulate faster than they age out (observed: ~89 GB
across two workspace roots).

This profile makes the local tradeoff explicit: aggressively reclaim completed
task data and provider session state, while retaining issue-terminal fallback
data for up to seven days and keeping a 15% free-space emergency floor.

## What this sets

| Variable | Value | Effect |
|---|---|---|
| `MULTICA_GC_INTERVAL` | `10m` | Sweep frequency. |
| `MULTICA_GC_COMPLETED_TASK_TTL` | `12h` | Evict completed task workdirs 12h after they finish (main lever). |
| `MULTICA_GC_TTL` | `168h` | Retain terminal-issue fallback workdirs for up to 7d; this intentionally extends v0.5's 24h default. |
| `MULTICA_GC_ARTIFACT_TTL` | `1h` | Artifact retention. |
| `MULTICA_GC_CODEX_SESSION_TTL` | `24h` | Opt into pruning idle Codex rollout/sqlite session state after 24h. |
| `MULTICA_GC_MIN_FREE_PERCENT` | `15` | Emergency sweep fires before free space gets critical. |

Bump `COMPLETED_TASK_TTL` to `24h` if you frequently reopen recent runs.

## Apply

```sh
./apply-gc-tuning.sh
```

Then **quit and relaunch the Multica Desktop (gitcustom) app** — `launchctl
setenv` only affects processes started afterward, so the already-running daemon
won't pick up the new values until it restarts. On login the plist re-applies
them automatically (`RunAtLoad`).

## Notes

- These are runtime env vars the daemon honors after the disk-pressure GC
  implementation lands — this is configuration, not a change to compiled-in
  defaults.
- The 12h completed-task TTL removes an inactive managed task workdir regardless
  of whether its parent issue remains open. The 24h Codex session TTL can make a
  later continuation start without its old provider-native transcript. Both are
  deliberate space-for-history tradeoffs in this local profile.
- If a future auto-update swaps the binary, re-verify the knobs still apply
  (`config set disable_auto_update true` should prevent that).
