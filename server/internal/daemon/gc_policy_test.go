package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGCPolicyFileOverridesStaleEnvironment(t *testing.T) {
	stageFakeAgent(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	t.Setenv("MULTICA_GC_ARTIFACTS_ONLY", "false")
	t.Setenv("MULTICA_GC_INTERVAL", "2h")
	t.Setenv("MULTICA_GC_COMPLETED_TASK_TTL", "12h")
	dir := filepath.Join(home, ".multica")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gc-policy.json"), []byte(`{"artifacts_only":true,"interval":"10m","artifact_ttl":"1h","min_free_percent":15}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"", "desktop-test"} {
		cfg, err := LoadConfig(Overrides{Profile: profile, ServerURL: "http://localhost:0", WorkspacesRoot: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.GCArtifactsOnly || cfg.GCInterval != 10*time.Minute || cfg.GCArtifactTTL != time.Hour || cfg.GCMinFreePercent != 15 {
			t.Fatalf("profile %q did not load machine policy", profile)
		}
	}
	if os.Getenv("MULTICA_GC_ARTIFACTS_ONLY") != "false" {
		t.Fatal("must not mutate process environment")
	}
}

func TestGCPolicyFileValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".multica")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := Config{GCInterval: time.Hour, GCArtifactTTL: time.Hour}
	cfg := original
	if err := applyGCPolicyFile(&cfg); err != nil || cfg.GCInterval != time.Hour {
		t.Fatal("missing file must preserve existing settings", err)
	}
	for _, input := range []string{`{`, `{"unknown":true}`, `{"interval":"0"}`, `{"interval":"-1h"}`, `{"artifact_ttl":"-1s"}`, `{"min_free_percent":100}`, `{"min_free_percent":-1}`, `{"artifacts_only":true} {}`, `{"artifacts_only":true,"interval":"invalid"}`} {
		if err := os.WriteFile(filepath.Join(dir, "gc-policy.json"), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		cfg = original
		if err := applyGCPolicyFile(&cfg); err == nil {
			t.Fatalf("accepted %s", input)
		}
		if cfg.GCArtifactsOnly || cfg.GCInterval != original.GCInterval {
			t.Fatal("invalid policy partially applied")
		}
	}
}
