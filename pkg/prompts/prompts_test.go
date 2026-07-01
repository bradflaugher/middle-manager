package prompts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func fullContext() map[string]string {
	return BuildContext(Context{
		Repo:           "/tmp/repo",
		Issue:          "7",
		DiscoverOutput: "plan",
		ExecuteOutput:  "did it",
		AgentMemory:    "memory",
		TestOutput:     "prev verifier report",
		ErrorLog:       "errors",
		DiffSummary:    "M main.go",
		Notes:          "learned things",
		NotesFile:      "/state/notes.md",
		StateDir:       "/state",
		Iteration:      2,
		Mission:        "fix the bug",
	})
}

// Every placeholder in every step template must be a key BuildContext provides
// (plus the issue_* keys the loop injects) — a typo'd placeholder would
// otherwise be silently handed to the agent as literal "{foo}".
func TestTemplatesFullyRenderable(t *testing.T) {
	ctx := fullContext()
	ctx["issue_title"] = "title"
	ctx["issue_body"] = "body"
	ctx["issue_number"] = "7"

	templates := map[string]string{
		"discover":         DiscoverTemplate,
		"discover_feature": DiscoverFeatureTemplate,
		"execute":          ExecuteTemplate,
		"verify":           VerifyTemplate,
		"commit":           CommitTemplate,
		"solo":             SoloTemplate,
	}
	placeholder := regexp.MustCompile(`\{[a-z_]+\}`)
	for name, tmpl := range templates {
		rendered := RenderPrompt(tmpl, ctx)
		if leftover := placeholder.FindAllString(rendered, -1); len(leftover) > 0 {
			t.Errorf("template %s has unresolved placeholders: %v", name, leftover)
		}
	}
}

// The verifier must receive the programmer's report and the real git change
// surface — that handoff is the core of inter-agent context passing.
func TestVerifyTemplateCarriesHandoffs(t *testing.T) {
	rendered := RenderPrompt(VerifyTemplate, fullContext())
	if !strings.Contains(rendered, "did it") {
		t.Error("verify prompt missing the execute step's output")
	}
	if !strings.Contains(rendered, "M main.go") {
		t.Error("verify prompt missing the git diff summary")
	}
	if !strings.Contains(rendered, "prev verifier report") {
		t.Error("verify prompt missing the previous verifier report")
	}
}

// The repo-pollution guards: work prompts must forbid AGENTS.md edits, and the
// commit prompt must direct learnings to the out-of-repo notes file instead of
// instructing an AGENTS.md update (the old behavior).
func TestPromptsProtectRepoMemoryFiles(t *testing.T) {
	ctx := fullContext()
	commit := RenderPrompt(CommitTemplate, ctx)
	if !strings.Contains(commit, "/state/notes.md") {
		t.Error("commit prompt does not point at the notes file")
	}
	if !strings.Contains(commit, "Do NOT edit AGENTS.md") {
		t.Error("commit prompt lost the AGENTS.md guard")
	}
	for name, tmpl := range map[string]string{"execute": ExecuteTemplate, "solo": SoloTemplate} {
		if !strings.Contains(strings.ToLower(tmpl), "agents.md") {
			t.Errorf("%s prompt lost its AGENTS.md guard", name)
		}
	}
}

func TestClip(t *testing.T) {
	if got := Clip("short", 100, true); got != "short" {
		t.Errorf("short strings must pass through, got %q", got)
	}
	long := strings.Repeat("head\n", 100) + "TAIL-SENTINEL"
	tail := Clip(long, 50, true)
	if !strings.Contains(tail, "TAIL-SENTINEL") || !strings.Contains(tail, "truncated by middle-manager") {
		t.Errorf("keepEnd clip must keep the tail and mark the cut: %q", tail)
	}
	long2 := "HEAD-SENTINEL\n" + strings.Repeat("tail\n", 100)
	head := Clip(long2, 50, false)
	if !strings.Contains(head, "HEAD-SENTINEL") || !strings.Contains(head, "truncated by middle-manager") {
		t.Errorf("head clip must keep the head and mark the cut: %q", head)
	}
	if len(Clip(long, 50, true)) > 50+len(clipMarker) {
		t.Error("clip exceeded its budget")
	}
}

// Custom prompt overrides: state-root prompts (outside the repo) win over the
// repo's committed .middle-manager/prompts, which wins over the embedded default.
func TestLoadPromptPrecedence(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()

	if got := LoadPrompt(repo, stateRoot, "execute"); got != ExecuteTemplate {
		t.Error("no overrides present: expected the embedded default")
	}

	repoPrompts := filepath.Join(repo, ".middle-manager", "prompts")
	if err := os.MkdirAll(repoPrompts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPrompts, "execute.md"), []byte("repo override"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := LoadPrompt(repo, stateRoot, "execute"); got != "repo override" {
		t.Errorf("repo override not honored, got %q", got)
	}

	statePrompts := filepath.Join(stateRoot, "prompts")
	if err := os.MkdirAll(statePrompts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(statePrompts, "execute.md"), []byte("state override"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := LoadPrompt(repo, stateRoot, "execute"); got != "state override" {
		t.Errorf("state-root override must win, got %q", got)
	}
}
