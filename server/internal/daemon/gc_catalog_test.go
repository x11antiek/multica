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

const catalogRel = "codex-home/cache/remote_plugin_catalog"

func TestGCArtifactsOnlyPreservesWorkspacesAndHistory(t *testing.T) {
	// Any parent lookup would mean we entered retention logic in cache-only mode.
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected retention request: %s", r.URL.Path)
		http.Error(w, "not expected", http.StatusInternalServerError)
	}))
	d.cfg.GCArtifactsOnly = true
	d.cfg.GCCompletedTaskTTL = time.Nanosecond
	d.cfg.GCTTL = time.Nanosecond
	d.cfg.GCOrphanTTL = time.Nanosecond
	d.cfg.GCCodexSessionTTL = time.Nanosecond
	d.cfg.GCHermesSessionTTL = time.Nanosecond
	old := time.Now().Add(-48 * time.Hour)
	for _, tc := range []struct {
		name          string
		completed     time.Time
		active, local bool
		remove        bool
	}{
		{"completed", old, false, false, true},
		{"active", old, true, false, false},
		{"fresh", time.Now(), false, false, false},
		{"unfinished", time.Time{}, false, false, false},
		{"local", old, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", tc.name, &execenv.GCMeta{
				Kind: execenv.GCKindIssue, WorkspaceID: "ws", IssueID: "issue", CompletedAt: tc.completed, LocalDirectory: tc.local,
			})
			writeFile(t, filepath.Join(dir, catalogRel, "catalog.json"), 100)
			writeFile(t, filepath.Join(dir, "workdir/repo/node_modules/package/index.js"), 50)
			for _, rel := range []string{"workdir/repo/source.ts", "workdir/repo/.git/objects/unpushed", "output/result.md", "logs/run.log", "codex-home/auth.json", "codex-home/sessions/rollout.jsonl", "codex-home/state_5.sqlite", "codex-home/cache/other-cache/keep", "workdir/repo/remote_plugin_catalog/keep"} {
				writeFile(t, filepath.Join(dir, rel), 20)
			}
			if tc.active {
				d.markActiveEnvRoot(dir)
			}
			d.runGC(context.Background())
			if tc.remove {
				assertGone(t, dir, catalogRel)
			} else {
				assertKept(t, dir, catalogRel+"/catalog.json")
			}
			assertKept(t, dir, "workdir/repo/source.ts", "workdir/repo/.git/objects/unpushed", "output/result.md", "logs/run.log", "codex-home/auth.json", "codex-home/sessions/rollout.jsonl", "codex-home/state_5.sqlite", "codex-home/cache/other-cache/keep", "workdir/repo/remote_plugin_catalog/keep")
		})
	}
	orphan := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "orphan", nil)
	writeFile(t, filepath.Join(orphan, "workdir/uncommitted.txt"), 50)
	writeFile(t, filepath.Join(orphan, catalogRel, "catalog.json"), 100)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	d.runGC(context.Background())
	assertKept(t, orphan, "workdir/uncommitted.txt", catalogRel+"/catalog.json")
}

func TestManagedArtifactCatalogRespectsLinksAndTTL(t *testing.T) {
	t.Parallel()
	for _, link := range []string{"codex-home", "codex-home/cache", catalogRel} {
		t.Run(link, func(t *testing.T) {
			d := newGCTestDaemon(t, http.NewServeMux())
			dir, outside := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(outside, "cache/remote_plugin_catalog/keep"), 50)
			writeFile(t, filepath.Join(outside, "remote_plugin_catalog/keep"), 50)
			writeFile(t, filepath.Join(outside, "keep"), 50)
			target := filepath.Join(dir, link)
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Skip(err)
			}
			if removed, _, _ := d.cleanManagedTaskArtifacts(dir); removed != 0 {
				t.Fatal("followed symlink")
			}
			assertKept(t, outside, "cache/remote_plugin_catalog/keep", "remote_plugin_catalog/keep", "keep")
		})
	}
	d := newGCTestDaemon(t, chatGCMux("chat", "active"))
	dir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "catalog", &execenv.GCMeta{Kind: execenv.GCKindChat, ChatSessionID: "chat", WorkspaceID: "ws", CompletedAt: time.Now().Add(-48 * time.Hour)})
	writeFile(t, filepath.Join(dir, catalogRel, "catalog.json"), 100)
	d.cfg.GCArtifactTTL = 0
	if action := d.shouldCleanTaskDir(context.Background(), dir); action != gcActionSkip {
		t.Fatalf("disabled artifacts: action=%v", action)
	}
	d.cfg.GCArtifactTTL = time.Hour
	action := d.shouldCleanTaskDir(context.Background(), dir)
	if action != gcActionCleanManagedArtifacts {
		t.Fatalf("catalog-only task: action=%v", action)
	}
	d.applyGCAction(dir, action, &gcStats{})
	assertGone(t, dir, catalogRel)
}

func TestScanDiskUsageCatalogIsExact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "ws", "task")
	writeFile(t, filepath.Join(dir, catalogRel, "catalog.json"), 100)
	writeFile(t, filepath.Join(dir, "workdir/repo/remote_plugin_catalog/source.json"), 50)
	report, err := ScanDiskUsage(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalArtifactSizeBytes != 100 || report.TotalSizeBytes != 150 {
		t.Fatalf("catalog accounting: %+v", report)
	}
}
