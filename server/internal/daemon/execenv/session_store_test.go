package execenv

import (
	"path/filepath"
	"testing"
)

func TestSessionConversationSegmentCoversEveryTaskKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		task TaskContextForEnv
		want string
	}{
		{"issue", TaskContextForEnv{IssueID: "issue-1", TaskID: "task-1"}, "issue-1"},
		{"chat", TaskContextForEnv{ChatSessionID: "chat-1", TaskID: "task-1"}, "chat_chat-1"},
		{"autopilot", TaskContextForEnv{AutopilotRunID: "run-1", TaskID: "task-1"}, "autopilot_run-1"},
		{"one-shot", TaskContextForEnv{TaskID: "task-1"}, "task_task-1"},
		{"missing", TaskContextForEnv{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sessionConversationSegment(tc.task); got != tc.want {
				t.Fatalf("sessionConversationSegment() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderSessionStorePathIsProfileOwnedAndScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	task := TaskContextForEnv{AgentID: "agent-1", IssueID: "issue-1", TaskID: "task-1"}
	got := ProviderSessionStorePath("", "cursor", task)
	want := filepath.Join(home, ".multica", "provider-sessions", "cursor", "agent-1", "issue-1")
	if got != want {
		t.Fatalf("ProviderSessionStorePath() = %q, want %q", got, want)
	}
	if got := ProviderSessionStorePath("", "cursor", TaskContextForEnv{TaskID: "task-1"}); got != "" {
		t.Fatalf("store without agent = %q, want empty", got)
	}
}
