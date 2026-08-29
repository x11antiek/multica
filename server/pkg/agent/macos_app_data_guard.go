package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	macOSAppDataSandboxExecutable = "/usr/bin/sandbox-exec"
	// MULTICA_ALLOW_MACOS_APP_DATA is an explicit escape hatch for tasks that
	// genuinely need to inspect another app's container. Any value other than
	// "1" keeps the guard enabled.
	macOSAppDataAccessOverrideEnv = "MULTICA_ALLOW_MACOS_APP_DATA"
)

// macOSAppDataSandboxProfile deliberately allows the task's existing access
// and only removes the three cross-app container roots covered by macOS App
// Data protection. The narrower outer Seatbelt policy keeps network access and
// normal repository/tool configuration working while making an accidental
// `find ~` fail with EPERM instead of opening a modal TCC consent dialog.
const macOSAppDataSandboxProfile = `(version 1)
(allow default)
(deny file-read* file-write*
  (subpath (param "MULTICA_APP_CONTAINERS"))
  (subpath (param "MULTICA_GROUP_CONTAINERS"))
  (subpath (param "MULTICA_APPLICATION_SCRIPTS")))`

// configureCodexMacOSAppDataGuard wraps the Codex process tree in a narrow
// macOS Seatbelt policy. Descendant tools inherit the policy, which is
// important because TCC attributes commands such as find/rg/node back to the
// Multica desktop app and otherwise presents the prompt in front of Multica.
func configureCodexMacOSAppDataGuard(cmd *exec.Cmd, env map[string]string) error {
	return configureCodexAppDataGuard(cmd, env, runtime.GOOS, os.UserHomeDir)
}

func configureCodexAppDataGuard(
	cmd *exec.Cmd,
	env map[string]string,
	goos string,
	userHomeDir func() (string, error),
) error {
	if goos != "darwin" || strings.TrimSpace(env[macOSAppDataAccessOverrideEnv]) == "1" {
		return nil
	}

	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		var err error
		home, err = userHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory for macOS app-data guard: %w", err)
		}
		if strings.TrimSpace(home) == "" {
			return fmt.Errorf("resolve home directory for macOS app-data guard: empty home directory")
		}
	}

	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args[1:]...)
	library := filepath.Join(home, "Library")
	cmd.Path = macOSAppDataSandboxExecutable
	cmd.Args = []string{
		macOSAppDataSandboxExecutable,
		"-D", "MULTICA_APP_CONTAINERS=" + filepath.Join(library, "Containers"),
		"-D", "MULTICA_GROUP_CONTAINERS=" + filepath.Join(library, "Group Containers"),
		"-D", "MULTICA_APPLICATION_SCRIPTS=" + filepath.Join(library, "Application Scripts"),
		"-p", macOSAppDataSandboxProfile,
		originalPath,
	}
	cmd.Args = append(cmd.Args, originalArgs...)
	return nil
}
