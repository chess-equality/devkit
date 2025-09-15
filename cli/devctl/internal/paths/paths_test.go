package paths

import "testing"

func TestAgentRepoPath_EmptyRepo(t *testing.T) {
	if got := AgentRepoPath("dev-all", "1", ""); got != "/workspaces/dev" {
		t.Fatalf("dev-all idx1: want /workspaces/dev, got %q", got)
	}
	if got := AgentRepoPath("dev-all", "2", ""); got != "/workspaces/dev/"+AgentWorktreesDir+"/agent2" {
		t.Fatalf("dev-all idx2: want /workspaces/dev/%s/agent2, got %q", AgentWorktreesDir, got)
	}
	if got := AgentRepoPath("codex", "1", ""); got != "/workspace" {
		t.Fatalf("codex: want /workspace, got %q", got)
	}
}

func TestAgentHomePath(t *testing.T) {
	if got := AgentHomePath("dev-all", "1", "ouroboros-ide"); got != "/workspaces/dev/ouroboros-ide/.devhome-agent1" {
		t.Fatalf("dev-all home idx1: got %q", got)
	}
	if got := AgentHomePath("dev-all", "3", "anything"); got != "/workspaces/dev/"+AgentWorktreesDir+"/agent3/.devhome-agent3" {
		t.Fatalf("dev-all home idx3: got %q", got)
	}
	if got := AgentHomePath("codex", "2", ""); got != "/workspace/.devhome-agent2" {
		t.Fatalf("codex home: got %q", got)
	}
}
