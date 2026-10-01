#!/bin/sh
# apply-gc-tuning.sh — install/refresh the Multica GC env tuning on this Mac.
#
# Installs the cache-only policy. Existing processes keep their old environment;
# inspect them with check-gc-tuning.py and restart each idle runtime explicitly.
set -eu

PLIST_SRC="$(cd "$(dirname "$0")" && pwd)/ai.multica.gc-tuning.plist"
PLIST_DST="$HOME/Library/LaunchAgents/ai.multica.gc-tuning.plist"

# Publish atomically so a daemon starting concurrently sees a complete policy.
POLICY_SRC="$(dirname "$PLIST_SRC")/gc-policy.json"
mkdir -p "$HOME/.multica"
POLICY_TMP="$(mktemp "$HOME/.multica/gc-policy.json.XXXXXX")"
trap 'rm -f "$POLICY_TMP"' EXIT HUP INT TERM
cp "$POLICY_SRC" "$POLICY_TMP"
chmod 600 "$POLICY_TMP"
mv "$POLICY_TMP" "$HOME/.multica/gc-policy.json"

echo "Installing $PLIST_DST"
mkdir -p "$HOME/Library/LaunchAgents"
launchctl unload "$PLIST_DST" 2>/dev/null || true
cp "$PLIST_SRC" "$PLIST_DST"
launchctl load "$PLIST_DST"

# RunAtLoad propagation is asynchronous and unreliable, so set the values
# directly here too — keep this list in sync with the plist.
launchctl setenv MULTICA_GC_INTERVAL 10m
launchctl setenv MULTICA_GC_ARTIFACTS_ONLY true
launchctl setenv MULTICA_GC_TTL 876000h
launchctl setenv MULTICA_GC_ARTIFACT_TTL 1h
launchctl setenv MULTICA_GC_COMPLETED_TASK_TTL 0
launchctl setenv MULTICA_GC_MIN_FREE_PERCENT 15
launchctl setenv MULTICA_GC_CODEX_SESSION_TTL 0

echo "Current values:"
for k in MULTICA_GC_ARTIFACTS_ONLY MULTICA_GC_INTERVAL MULTICA_GC_TTL MULTICA_GC_ARTIFACT_TTL \
         MULTICA_GC_COMPLETED_TASK_TTL MULTICA_GC_MIN_FREE_PERCENT \
         MULTICA_GC_CODEX_SESSION_TTL; do
  printf '  %-32s %s\n' "$k" "$(launchctl getenv "$k")"
done

echo
echo "Policy installed for future processes; running daemons have NOT changed."
echo "After active tasks finish, restart each runtime to read gc-policy.json."
echo "Relaunching Desktop alone can reconnect to the old daemon."
echo "For launchd-supervised daemons, restart their owning launchd job when idle."
echo "Verify: python3 $(dirname "$0")/check-gc-tuning.py --port <health-port>"
