package agent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestConfigureCodexAppDataGuardWrapsDarwinCommand(t *testing.T) {
	cmd := exec.Command("/usr/bin/find", "/tmp", "-maxdepth", "1")
	wantDir := "/tmp/workdir"
	cmd.Dir = wantDir

	err := configureCodexAppDataGuard(cmd, map[string]string{"HOME": "/Users/example"}, "darwin", func() (string, error) {
		t.Fatal("HOME fallback should not be called")
		return "", nil
	})
	if err != nil {
		t.Fatalf("configureCodexAppDataGuard: %v", err)
	}

	if cmd.Path != macOSAppDataSandboxExecutable {
		t.Fatalf("Path = %q, want %q", cmd.Path, macOSAppDataSandboxExecutable)
	}
	wantArgs := []string{
		macOSAppDataSandboxExecutable,
		"-D", "MULTICA_APP_CONTAINERS=/Users/example/Library/Containers",
		"-D", "MULTICA_GROUP_CONTAINERS=/Users/example/Library/Group Containers",
		"-D", "MULTICA_APPLICATION_SCRIPTS=/Users/example/Library/Application Scripts",
		"-p", macOSAppDataSandboxProfile,
		"/usr/bin/find", "/tmp", "-maxdepth", "1",
	}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", cmd.Args, wantArgs)
	}
	if cmd.Dir != wantDir {
		t.Fatalf("Dir = %q, want preserved %q", cmd.Dir, wantDir)
	}
}

func TestConfigureCodexAppDataGuardUsesHomeFallback(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	err := configureCodexAppDataGuard(cmd, nil, "darwin", func() (string, error) {
		return "/Users/fallback", nil
	})
	if err != nil {
		t.Fatalf("configureCodexAppDataGuard: %v", err)
	}
	if got := strings.Join(cmd.Args, "\n"); !strings.Contains(got, "/Users/fallback/Library/Containers") {
		t.Fatalf("wrapped args do not use fallback home: %q", got)
	}
}

func TestConfigureCodexAppDataGuardFailsWithoutHome(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	err := configureCodexAppDataGuard(cmd, nil, "darwin", func() (string, error) {
		return "", errors.New("home unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "home unavailable") {
		t.Fatalf("error = %v, want home resolution failure", err)
	}
}

func TestConfigureCodexAppDataGuardFailsWithEmptyHome(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	err := configureCodexAppDataGuard(cmd, nil, "darwin", func() (string, error) {
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "empty home directory") {
		t.Fatalf("error = %v, want empty home failure", err)
	}
}

func TestConfigureCodexAppDataGuardNoopsOutsideDarwin(t *testing.T) {
	cmd := exec.Command("/usr/bin/find", "/tmp")
	wantPath := cmd.Path
	wantArgs := append([]string(nil), cmd.Args...)
	if err := configureCodexAppDataGuard(cmd, nil, "linux", func() (string, error) {
		t.Fatal("home fallback should not be called")
		return "", nil
	}); err != nil {
		t.Fatalf("configureCodexAppDataGuard: %v", err)
	}
	if cmd.Path != wantPath || !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("non-darwin command changed: path=%q args=%#v", cmd.Path, cmd.Args)
	}
}

func TestConfigureCodexAppDataGuardHonorsExplicitOverride(t *testing.T) {
	cmd := exec.Command("/usr/bin/find", "/tmp")
	wantPath := cmd.Path
	wantArgs := append([]string(nil), cmd.Args...)
	env := map[string]string{macOSAppDataAccessOverrideEnv: "1"}
	if err := configureCodexAppDataGuard(cmd, env, "darwin", func() (string, error) {
		t.Fatal("home fallback should not be called")
		return "", nil
	}); err != nil {
		t.Fatalf("configureCodexAppDataGuard: %v", err)
	}
	if cmd.Path != wantPath || !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("opt-out command changed: path=%q args=%#v", cmd.Path, cmd.Args)
	}
}

func TestMacOSAppDataSandboxProfileDeniesConfiguredRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}

	protectedRoot := filepath.Join(t.TempDir(), "Containers")
	if err := os.MkdirAll(protectedRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// t.TempDir is rooted under /var on macOS, where /var is a symlink to
	// /private/var. Seatbelt matches the canonical path, so use it in the test
	// parameters just as the production /Users path is already canonical.
	protectedRoot, err := filepath.EvalSymlinks(protectedRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	cmd := exec.Command(
		macOSAppDataSandboxExecutable,
		"-D", "MULTICA_APP_CONTAINERS="+protectedRoot,
		"-D", "MULTICA_GROUP_CONTAINERS="+filepath.Join(t.TempDir(), "Group Containers"),
		"-D", "MULTICA_APPLICATION_SCRIPTS="+filepath.Join(t.TempDir(), "Application Scripts"),
		"-p", macOSAppDataSandboxProfile,
		"/bin/ls", protectedRoot,
	)
	if err := cmd.Run(); err == nil {
		t.Fatal("sandboxed ls unexpectedly read the protected root")
	}
}
