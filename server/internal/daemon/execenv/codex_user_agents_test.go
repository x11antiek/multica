package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareCodexHomeHydratesCustomAgents(t *testing.T) {
	sharedHome := t.TempDir()
	t.Setenv("CODEX_HOME", sharedHome)
	sharedAgentsDir := filepath.Join(sharedHome, "agents")
	if err := os.MkdirAll(sharedAgentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedAgentsDir, "gsd-planner.toml"), []byte("name = \"planner v1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedAgentsDir, "README.md"), []byte("not an agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedAgentsDir, ".hidden.toml"), []byte("name = \"hidden\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	taskHome := filepath.Join(t.TempDir(), "codex-home")
	if err := prepareCodexHome(taskHome, testLogger()); err != nil {
		t.Fatalf("prepareCodexHome: %v", err)
	}

	taskAgent := filepath.Join(taskHome, "agents", "gsd-planner.toml")
	body, err := os.ReadFile(taskAgent)
	if err != nil {
		t.Fatalf("read hydrated agent: %v", err)
	}
	if string(body) != "name = \"planner v1\"\n" {
		t.Fatalf("hydrated agent = %q", body)
	}
	fi, err := os.Lstat(taskAgent)
	if err != nil {
		t.Fatalf("lstat hydrated agent: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("hydrated agent must be an isolated regular file: mode=%v", fi.Mode())
	}
	for _, name := range []string{"README.md", ".hidden.toml"} {
		if _, err := os.Lstat(filepath.Join(taskHome, "agents", name)); !os.IsNotExist(err) {
			t.Errorf("unexpected non-agent file %q in task home (err=%v)", name, err)
		}
	}

	// Reuse refreshes changed definitions and removes roles no longer present
	// in the user's shared home.
	if err := os.WriteFile(filepath.Join(sharedAgentsDir, "gsd-planner.toml"), []byte("name = \"planner v2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedAgentsDir, "gsd-reviewer.toml"), []byte("name = \"reviewer\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskHome, "agents", "stale.toml"), []byte("name = \"stale\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexHome(taskHome, testLogger()); err != nil {
		t.Fatalf("prepareCodexHome reuse: %v", err)
	}

	body, err = os.ReadFile(taskAgent)
	if err != nil || string(body) != "name = \"planner v2\"\n" {
		t.Fatalf("refreshed agent = %q, err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(taskHome, "agents", "gsd-reviewer.toml")); err != nil {
		t.Fatalf("new agent missing after reuse: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(taskHome, "agents", "stale.toml")); !os.IsNotExist(err) {
		t.Fatalf("stale agent survived reuse (err=%v)", err)
	}

	// Mutating the task-local copy must not write through to the shared file.
	if err := os.WriteFile(taskAgent, []byte("name = \"task edit\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sharedBody, err := os.ReadFile(filepath.Join(sharedAgentsDir, "gsd-planner.toml"))
	if err != nil || string(sharedBody) != "name = \"planner v2\"\n" {
		t.Fatalf("task mutation reached shared agent: %q, err=%v", sharedBody, err)
	}
}

func TestHydrateCodexAgentsRemovesDirectoryWhenSharedAgentsDisappear(t *testing.T) {
	sharedHome := t.TempDir()
	t.Setenv("CODEX_HOME", sharedHome)
	taskHome := t.TempDir()
	taskAgentsDir := filepath.Join(taskHome, "agents")
	if err := os.MkdirAll(taskAgentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskAgentsDir, "stale.toml"), []byte("name = \"stale\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := hydrateCodexAgents(taskHome, testLogger()); err != nil {
		t.Fatalf("hydrateCodexAgents: %v", err)
	}
	if _, err := os.Lstat(taskAgentsDir); !os.IsNotExist(err) {
		t.Fatalf("stale agents directory survived missing shared source (err=%v)", err)
	}
}
