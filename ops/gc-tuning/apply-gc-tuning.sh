#!/bin/sh
# apply-gc-tuning.sh — install/refresh the Multica GC env tuning on this Mac.
#
# The Multica daemon reads its garbage-collection policy from MULTICA_GC_*
# environment variables. Without tuning, the gitcustom build hoards workdirs:
# completed-task and codex-session eviction are disabled and the general TTL is
# effectively infinite, so disk only gets reclaimed once free space drops below
# the MIN_FREE_PERCENT backstop. This job sets finite, age-based TTLs so GC
# sweeps completed workdirs (and their node_modules) proactively.
#
# launchctl setenv only affects processes started AFTER it runs, so after
# applying you must quit and relaunch the Multica Desktop (gitcustom) app for
# the running daemon to inherit the new values. On login the plist re-applies
# them automatically (RunAtLoad).
set -eu

PLIST_SRC="$(cd "$(dirname "$0")" && pwd)/ai.multica.gc-tuning.plist"
PLIST_DST="$HOME/Library/LaunchAgents/ai.multica.gc-tuning.plist"

echo "Installing $PLIST_DST"
mkdir -p "$HOME/Library/LaunchAgents"
launchctl unload "$PLIST_DST" 2>/dev/null || true
cp "$PLIST_SRC" "$PLIST_DST"
launchctl load "$PLIST_DST"

# RunAtLoad propagation is asynchronous and unreliable, so set the values
# directly here too — keep this list in sync with the plist.
launchctl setenv MULTICA_GC_INTERVAL 10m
launchctl setenv MULTICA_GC_TTL 168h
launchctl setenv MULTICA_GC_ARTIFACT_TTL 1h
launchctl setenv MULTICA_GC_COMPLETED_TASK_TTL 12h
launchctl setenv MULTICA_GC_MIN_FREE_PERCENT 15
launchctl setenv MULTICA_GC_CODEX_SESSION_TTL 24h

echo "Current values:"
for k in MULTICA_GC_INTERVAL MULTICA_GC_TTL MULTICA_GC_ARTIFACT_TTL \
         MULTICA_GC_COMPLETED_TASK_TTL MULTICA_GC_MIN_FREE_PERCENT \
         MULTICA_GC_CODEX_SESSION_TTL; do
  printf '  %-32s %s\n' "$k" "$(launchctl getenv "$k")"
done

echo
echo "Done. Now quit and relaunch the Multica Desktop (gitcustom) app so the"
echo "running daemon picks up the new environment."
