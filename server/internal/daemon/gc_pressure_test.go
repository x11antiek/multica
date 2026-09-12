package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// stubFreeSeq returns a diskFree stub that reports a scripted sequence of
// free-percent values (avail=pct, total=100) so freeDiskPercent yields pct
// directly. The last value is repeated once the sequence is exhausted.
func stubFreeSeq(pcts ...uint64) func(string) (uint64, uint64, error) {
	i := 0
	return func(string) (uint64, uint64, error) {
		v := pcts[len(pcts)-1]
		if i < len(pcts) {
			v = pcts[i]
		}
		i++
		return v, 100, nil
	}
}

// completedWorkdirWithNodeModules creates a completed issue task dir carrying a
// node_modules artifact and stamps its completed_at.
func completedWorkdirWithNodeModules(t *testing.T, root, wsID, name string, completedAt time.Time) string {
	t.Helper()
	taskDir := createTaskDir(t, root, wsID, name, &execenv.GCMeta{
		Kind:        execenv.GCKindIssue,
		IssueID:     "issue-" + name,
		WorkspaceID: wsID,
		CompletedAt: completedAt,
	})
	nm := filepath.Join(taskDir, "workdir", "demobae-next", "node_modules", "lodash")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "index.js"), []byte("module.exports = {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return taskDir
}

func hasNodeModules(taskDir string) bool {
	_, err := os.Stat(filepath.Join(taskDir, "workdir", "demobae-next", "node_modules"))
	return err == nil
}

func TestPressureSweep_DisabledWhenZero(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d.cfg.GCMinFreePercent = 0  // disabled
	d.diskFree = stubFreeSeq(0) // report a full disk; must still be ignored

	taskDir := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "t1", time.Now().Add(-time.Hour))

	stats := &gcStats{byPattern: map[string]int{}}
	d.runArtifactPressureSweep(context.Background(), stats)

	if !hasNodeModules(taskDir) {
		t.Fatal("node_modules removed while pressure sweep disabled")
	}
	if stats.artifactRemoved != 0 {
		t.Fatalf("expected no reclaim when disabled, got %d", stats.artifactRemoved)
	}
}

func TestPressureSweep_NoPressureNoop(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d.cfg.GCMinFreePercent = 20
	d.diskFree = stubFreeSeq(80) // plenty free

	taskDir := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "t1", time.Now().Add(-time.Hour))

	stats := &gcStats{byPattern: map[string]int{}}
	d.runArtifactPressureSweep(context.Background(), stats)

	if !hasNodeModules(taskDir) {
		t.Fatal("node_modules removed despite healthy free space")
	}
}

func TestPressureSweep_ReclaimsOldestFirstThenStops(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d.cfg.GCMinFreePercent = 20
	// initial=5 (proceed), after A=5 (continue), after B=50 (relieved -> stop
	// before C).
	d.diskFree = stubFreeSeq(5, 5, 50)

	now := time.Now()
	oldest := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "a-oldest", now.Add(-72*time.Hour))
	middle := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "b-middle", now.Add(-48*time.Hour))
	newest := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "c-newest", now.Add(-1*time.Hour))

	stats := &gcStats{byPattern: map[string]int{}}
	d.runArtifactPressureSweep(context.Background(), stats)

	if hasNodeModules(oldest) {
		t.Error("oldest workdir should have been swept first")
	}
	if hasNodeModules(middle) {
		t.Error("middle workdir should have been swept before relief")
	}
	if !hasNodeModules(newest) {
		t.Error("newest workdir should be preserved once pressure relieved")
	}
	if stats.artifactDirs != 2 {
		t.Fatalf("expected 2 dirs swept, got %d", stats.artifactDirs)
	}
}

func TestPressureSweep_SkipsActiveLocalAndOrphan(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d.cfg.GCMinFreePercent = 90
	d.diskFree = stubFreeSeq(1) // always under pressure

	now := time.Now()
	completed := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "completed", now.Add(-time.Hour))

	// Active: a live task holds the env root.
	active := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "active", now.Add(-time.Hour))
	d.markActiveEnvRoot(active)
	defer d.unmarkActiveEnvRoot(active)

	// local_directory: user's own tree, audit trail preserved.
	localDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "local", &execenv.GCMeta{
		Kind: execenv.GCKindIssue, IssueID: "issue-local", WorkspaceID: "ws1",
		CompletedAt: now.Add(-time.Hour), LocalDirectory: true,
	})
	if err := os.MkdirAll(filepath.Join(localDir, "workdir", "demobae-next", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Orphan: no completion signal to trust.
	orphan := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "orphan", nil)
	if err := os.MkdirAll(filepath.Join(orphan, "workdir", "demobae-next", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	stats := &gcStats{byPattern: map[string]int{}}
	d.runArtifactPressureSweep(context.Background(), stats)

	if hasNodeModules(completed) {
		t.Error("completed workdir should have been swept")
	}
	if !hasNodeModules(active) {
		t.Error("active workdir must never be swept")
	}
	if _, err := os.Stat(filepath.Join(localDir, "workdir", "demobae-next", "node_modules")); err != nil {
		t.Error("local_directory workdir must never be swept")
	}
	if _, err := os.Stat(filepath.Join(orphan, "workdir", "demobae-next", "node_modules")); err != nil {
		t.Error("orphan workdir must never be swept")
	}
}

func TestPressureSweep_RefusesUnownedTaskDirectory(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d.cfg.GCMinFreePercent = 90
	d.diskFree = stubFreeSeq(1)

	taskDir := completedWorkdirWithNodeModules(t, d.cfg.WorkspacesRoot, "ws1", "unowned", time.Now().Add(-time.Hour))
	if err := os.Remove(filepath.Join(taskDir, ".task_owner")); err != nil {
		t.Fatal(err)
	}

	stats := &gcStats{byPattern: map[string]int{}}
	d.runArtifactPressureSweep(context.Background(), stats)

	if !hasNodeModules(taskDir) {
		t.Fatal("pressure sweep mutated a task directory without authoritative ownership")
	}
	if stats.artifactRemoved != 0 {
		t.Fatalf("expected no reclaim for unowned directory, got %d", stats.artifactRemoved)
	}
	if stats.skipped != 1 {
		t.Fatalf("expected unowned directory to be recorded as skipped, got %d", stats.skipped)
	}
}

func TestFreeDiskPercent_ProbeFailureReportsNotOK(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d.diskFree = func(string) (uint64, uint64, error) { return 0, 0, os.ErrPermission }
	if _, ok := d.freeDiskPercent(d.cfg.WorkspacesRoot); ok {
		t.Fatal("expected ok=false when the probe errors")
	}

	// total==0 (no error) must also be treated as unknown, not as 0% free.
	d.diskFree = func(string) (uint64, uint64, error) { return 0, 0, nil }
	if _, ok := d.freeDiskPercent(d.cfg.WorkspacesRoot); ok {
		t.Fatal("expected ok=false when total is zero")
	}
}

func TestRealDiskFreeStats(t *testing.T) {
	// Smoke-test the platform probe against a real path: it should report a
	// non-zero capacity and an available figure that does not exceed it.
	avail, total, err := diskFreeStats(t.TempDir())
	if err != nil {
		t.Fatalf("diskFreeStats: %v", err)
	}
	if total == 0 {
		t.Fatal("expected non-zero total capacity")
	}
	if avail > total {
		t.Fatalf("available %d exceeds total %d", avail, total)
	}
}
