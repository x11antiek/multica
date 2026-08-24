// Package reposetup runs a repository's post-checkout setup command, if the
// repo declares one, so a freshly created worktree has its dependencies
// installed before an agent runs in it.
//
// Multica creates each agent's working directory with `git worktree add` from a
// bare cache (see internal/daemon/repocache) or from a local repo (see
// internal/daemon/execenv). Git only materialises committed content, so
// gitignored build artifacts — most importantly node_modules — never carry over
// into a fresh worktree. Without a provisioning step every fresh checkout lacks
// dependencies and the first lint/build/test invocation fails; in a multi-agent
// pipeline each downstream agent (review, fix, …) rediscovers the same failure
// in its own empty worktree, and a fix agent can commit tests it never ran.
//
// This mirrors Conductor's behaviour: it reads a Conductor-compatible
// `.conductor/settings.toml` and runs the `[scripts].setup` command.
//
//	[scripts]
//	setup = "npm ci"
//
// Contract: best-effort and idempotent. A missing settings file or empty setup
// command is a silent no-op. Failures are returned so callers can log them, but
// callers treat setup as non-fatal — a broken setup script must not block every
// agent from starting. A per-worktree stamp keyed on the exact command text
// prevents a reused worktree from reinstalling on every checkout.
package reposetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// SettingsRelPath is the repo-relative file Multica reads the setup command from.
const SettingsRelPath = ".conductor/settings.toml"

// stampName lives inside the per-worktree git dir (never the working tree), so it
// is invisible to `git status` and can never be committed by an agent.
const stampName = "multica-setup.done"

// DefaultTimeout bounds a single setup run. A cold `npm ci` can take minutes; the
// ceiling only guards against a hung command, and it is cancellable via ctx.
const DefaultTimeout = 30 * time.Minute

type settings struct {
	Scripts struct {
		Setup string `toml:"setup"`
	} `toml:"scripts"`
}

// Disabled reports whether repo setup is turned off via the environment. Setup
// runs by default; MULTICA_DISABLE_REPO_SETUP=1 is the opt-out escape hatch.
func Disabled() bool {
	switch os.Getenv("MULTICA_DISABLE_REPO_SETUP") {
	case "1", "true", "TRUE", "yes":
		return true
	default:
		return false
	}
}

// Run executes the repo's [scripts].setup command in worktreePath when present.
// It is safe to call on every checkout: it no-ops when there is no command and
// when the same command already ran in this worktree.
func Run(ctx context.Context, worktreePath string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if Disabled() {
		return nil
	}

	cmdText, err := readSetupCommand(worktreePath)
	if err != nil {
		// A malformed settings.toml is worth surfacing but must not block the agent.
		logger.Warn("repo setup: could not read settings (skipping)",
			"path", filepath.Join(worktreePath, SettingsRelPath), "error", err)
		return nil
	}
	if cmdText == "" {
		return nil
	}

	stamp := stampPath(ctx, worktreePath)
	if stamp != "" && stampMatches(stamp, cmdText) {
		return nil // already provisioned for this exact command
	}

	runCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	logger.Info("repo setup: running", "path", worktreePath, "command", cmdText)
	start := time.Now()

	sh := shell()
	c := exec.CommandContext(runCtx, sh[0], append(sh[1:], cmdText)...)
	c.Dir = worktreePath
	c.Env = os.Environ()
	out, runErr := c.CombinedOutput()
	if runErr != nil {
		if runCtx.Err() != nil {
			runErr = fmt.Errorf("timed out after %s: %w", DefaultTimeout, runErr)
		}
		logger.Error("repo setup: command failed (non-fatal)",
			"path", worktreePath,
			"command", cmdText,
			"error", runErr,
			"output", tail(out, 4000),
		)
		return runErr
	}

	if stamp != "" {
		if err := os.WriteFile(stamp, []byte(cmdText), 0o644); err != nil {
			logger.Warn("repo setup: could not write stamp (setup will re-run next checkout)",
				"stamp", stamp, "error", err)
		}
	}
	logger.Info("repo setup: done", "path", worktreePath, "duration", time.Since(start).String())
	return nil
}

func readSetupCommand(worktreePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(worktreePath, SettingsRelPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var s settings
	if err := toml.Unmarshal(data, &s); err != nil {
		return "", err
	}
	return strings.TrimSpace(s.Scripts.Setup), nil
}

// stampPath resolves the per-worktree git dir and returns a path inside it.
// Returns "" when the git dir can't be resolved; the caller then runs setup
// without stamping (correct, just not deduped).
func stampPath(ctx context.Context, worktreePath string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "--absolute-git-dir")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(string(out))
	if gitDir == "" {
		return ""
	}
	return filepath.Join(gitDir, stampName)
}

func stampMatches(stamp, cmdText string) bool {
	b, err := os.ReadFile(stamp)
	if err != nil {
		return false
	}
	return string(b) == cmdText
}

func shell() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C"}
	}
	return []string{"sh", "-c"}
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "…" + string(b[len(b)-n:])
}
