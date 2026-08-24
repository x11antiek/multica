package execenv

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// hydrateCodexAgents copies user-defined Codex agent roles from the shared
// ~/.codex/agents/ directory into the isolated per-task CODEX_HOME. Without
// this mirror, Codex can launch generic workers but cannot discover custom
// agent types configured by the user (for example, GSD's gsd-planner role).
//
// Agent definitions are small standalone TOML files, so copy them rather than
// linking them. This prevents a task from mutating the user's shared agent
// configuration. The target directory is rebuilt on every Prepare/Reuse so
// edits and removals in the shared home cannot leave stale task-local roles.
func hydrateCodexAgents(codexHome string, logger *slog.Logger) error {
	targetAgentsDir := filepath.Join(codexHome, "agents")
	if err := os.RemoveAll(targetAgentsDir); err != nil {
		return fmt.Errorf("clear codex agents dir: %w", err)
	}

	sharedAgentsDir := filepath.Join(resolveSharedCodexHome(), "agents")
	info, err := os.Stat(sharedAgentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat shared agents dir: %w", err)
	}
	if !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(sharedAgentsDir)
	if err != nil {
		return fmt.Errorf("read shared agents dir: %w", err)
	}

	createdTarget := false
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".toml" {
			continue
		}

		src := filepath.Join(sharedAgentsDir, name)
		fileInfo, err := os.Stat(src)
		if err != nil || !fileInfo.Mode().IsRegular() {
			if err != nil && logger != nil {
				logger.Warn("execenv: codex custom-agent stat failed", "name", name, "error", err)
			}
			continue
		}

		if !createdTarget {
			if err := os.MkdirAll(targetAgentsDir, 0o755); err != nil {
				return fmt.Errorf("create codex agents dir %s: %w", targetAgentsDir, err)
			}
			createdTarget = true
		}
		dst := filepath.Join(targetAgentsDir, name)
		if err := copyFile(src, dst); err != nil {
			// copyFile creates the destination before streaming bytes. Never
			// leave a partial TOML file for Codex to parse after a short write.
			_ = os.Remove(dst)
			if logger != nil {
				logger.Warn("execenv: codex custom-agent copy failed", "name", name, "error", err)
			}
		}
	}

	return nil
}
