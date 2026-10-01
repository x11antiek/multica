#!/usr/bin/env python3
"""Verify a running daemon's effective cache-only policy without reading secrets."""
import argparse
import json
import sys
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=19514,
                        help="daemon health port (default profile: 19514)")
    args = parser.parse_args()
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{args.port}/health", timeout=5) as response:
            health = json.load(response)
    except (OSError, ValueError, urllib.error.URLError) as error:
        print(f"Cannot read daemon health: {error}", file=sys.stderr)
        return 2
    policy = health.get("gc_policy")
    if not isinstance(policy, dict):
        print("Running daemon does not report its GC policy; install the new build and restart when idle.")
        return 1
    expected = {"enabled": True, "artifacts_only": True, "interval": "10m0s",
                "artifact_ttl": "1h0m0s", "min_free_percent": 15}
    differences = [(key, policy.get(key), value) for key, value in expected.items()
                   if policy.get(key) != value]
    catalog = "codex-home/cache/remote_plugin_catalog"
    if catalog not in policy.get("managed_artifact_subpaths", []):
        differences.append(("managed_artifact_subpaths", "catalog missing", catalog))
    print(f"Daemon PID {health.get('pid')}, profile {health.get('profile') or 'default'}: "
          f"{'STALE' if differences else 'cache-only policy verified'}")
    for key, old, new in differences:
        print(f"  {key}: running={old!r}, expected={new!r}")
    if differences:
        print("Restart the owning runtime/job after active tasks finish. "
              "Relaunching Desktop alone may reconnect to the old daemon.")
    return int(bool(differences))


if __name__ == "__main__":
    sys.exit(main())
