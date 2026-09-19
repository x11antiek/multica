package execenv

import (
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/cli"
)

// sessionConversationSegment returns the stable scope that owns a provider's
// native transcript. Issue and chat conversations retain their existing keys
// so follow-up tasks resume the same history. One-shot tasks fall back to their
// own durable identifiers so quick-create and autopilot transcripts outlive the
// disposable task directory even though nothing resumes them later.
func sessionConversationSegment(task TaskContextForEnv) string {
	if issue := sanitizePathSegment(task.IssueID); issue != "" {
		return issue
	}
	if chat := sanitizePathSegment(task.ChatSessionID); chat != "" {
		return "chat_" + chat
	}
	if run := sanitizePathSegment(task.AutopilotRunID); run != "" {
		return "autopilot_" + run
	}
	if taskID := sanitizePathSegment(task.TaskID); taskID != "" {
		return "task_" + taskID
	}
	return ""
}

// ProviderSessionStorePath returns a profile-owned directory for provider data
// that would otherwise live below the disposable task environment. It is not
// used for providers whose native CLI already writes transcripts to a durable
// user-level store (Claude Code, OpenCode and Antigravity).
func ProviderSessionStorePath(daemonProfile, provider string, task TaskContextForEnv) string {
	provider = sanitizePathSegment(provider)
	agent := sanitizePathSegment(task.AgentID)
	conversation := sessionConversationSegment(task)
	if provider == "" || agent == "" || conversation == "" {
		return ""
	}
	profileDir, err := cli.ProfileDir(daemonProfile)
	if err != nil {
		return ""
	}
	return filepath.Join(profileDir, "provider-sessions", provider, agent, conversation)
}
