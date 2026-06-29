package tui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/bradflaugher/middle-manager/pkg/config"
)

// TestInterjectionRoundTrip proves the monitor captures a typed instruction and
// hands it to the loop exactly once (consumed on read, then cleared). This is
// the "queued, applied to the next step" path — not mid-agent steering.
func TestInterjectionRoundTrip(t *testing.T) {
	GlobalModel = NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})

	GlobalModel.textInput.SetValue("focus on error handling")
	GlobalModel.handleInput()

	if got := GetTUIInterjection(); got != "focus on error handling" {
		t.Fatalf("first read = %q, want the queued instruction", got)
	}
	if got := GetTUIInterjection(); got != "" {
		t.Fatalf("second read = %q, want empty (already consumed)", got)
	}
}

func TestSlashCommands(t *testing.T) {
	GlobalModel = NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})

	// A slash command must NOT be treated as an interjection.
	GlobalModel.textInput.SetValue("/pause")
	GlobalModel.handleInput()
	if !IsTUIPaused() {
		t.Fatal("/pause did not pause")
	}
	if got := GetTUIInterjection(); got != "" {
		t.Fatalf("/pause leaked into interjection: %q", got)
	}

	GlobalModel.textInput.SetValue("/resume")
	GlobalModel.handleInput()
	if IsTUIPaused() {
		t.Fatal("/resume did not resume")
	}

	GlobalModel.textInput.SetValue("/skip")
	GlobalModel.handleInput()
	if !IsTUISkipStep() {
		t.Fatal("/skip did not set skip")
	}
	if IsTUISkipStep() {
		t.Fatal("/skip should be a one-shot flag (cleared after read)")
	}

	// RequestPause is how interactive mode pauses before the next step.
	RequestPause()
	if !IsTUIPaused() {
		t.Fatal("RequestPause did not pause")
	}
}

// TestInterjectionNoTUIIsNoop documents that without an active monitor (e.g.
// --stream-output mode) interjection is simply disabled, not broken.
func TestInterjectionNoTUIIsNoop(t *testing.T) {
	GlobalModel = nil
	if got := GetTUIInterjection(); got != "" {
		t.Fatalf("expected empty interjection with no TUI, got %q", got)
	}
	if IsTUIPaused() || IsTUISkipStep() || IsTUIQuitting() {
		t.Fatal("expected all controls inert with no TUI")
	}
}

func TestLiveLogLeftAlignmentPreservesIndentation(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})

	m.pushLog(renderLiveLog("first line\n", false))
	m.pushLog(renderLiveLog("  indented child\n", false))
	m.pushLog(renderLiveLog("\tcode block\n", true))

	content := stripANSITest(m.logViewport.GetContent())
	if strings.Contains(content, "first line          indented child") {
		t.Fatalf("live log lines were joined by lipgloss padding: %q", content)
	}
	if !strings.Contains(content, "first line\n  indented child\n    code block\n") {
		t.Fatalf("live log did not preserve left alignment and indentation: %q", content)
	}
}

var ansiTestRe = regexp.MustCompile(`\x1B(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~]|[@-Z\\-_])`)

func stripANSITest(text string) string {
	return ansiTestRe.ReplaceAllString(text, "")
}

// ---------------------------------------------------------------------------
// Wizard input handling
// ---------------------------------------------------------------------------

func newTestWizard() *WizardModel { return NewWizardModel(config.NewDefaultConfig()) }

// sendW drives one message through the wizard's Update and returns the model.
func sendW(m *WizardModel, msg tea.Msg) *WizardModel {
	model, _ := m.Update(msg)
	return model.(*WizardModel)
}

// keySpace builds a real space keypress. In bubbletea v2 a space's String() is
// "space" (not " "); matching " " was the bug that broke checkbox toggling.
func keySpace() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: ' '} }
func keyEnter() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func keyDown() tea.KeyPressMsg       { return tea.KeyPressMsg{Code: tea.KeyDown} }
func keyUp() tea.KeyPressMsg         { return tea.KeyPressMsg{Code: tea.KeyUp} }
func keyRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// Guard the exact regression: a space press must stringify to "space".
func TestSpaceKeyStringIsSpace(t *testing.T) {
	if got := keySpace().String(); got != "space" {
		t.Fatalf("space keypress String() = %q, want %q", got, "space")
	}
}

func TestOptionsToggleWithSpace(t *testing.T) {
	m := newTestWizard()
	m.state = stateOptions
	m.optionsIndex = 0
	before := m.optionsValues[0]

	m = sendW(m, keySpace())
	if m.optionsValues[0] == before {
		t.Fatalf("space did not toggle option 0 (still %v)", before)
	}
	m = sendW(m, keySpace())
	if m.optionsValues[0] != before {
		t.Fatal("second space did not toggle option 0 back")
	}
}

// A key RELEASE must not toggle — Update matches KeyPressMsg only.
func TestOptionsReleaseDoesNotToggle(t *testing.T) {
	m := newTestWizard()
	m.state = stateOptions
	before := m.optionsValues[0]
	model, _ := m.Update(tea.KeyReleaseMsg{Code: ' '})
	if model.(*WizardModel).optionsValues[0] != before {
		t.Fatal("a key release toggled the checkbox")
	}
}

func TestOptionsCursorNavigation(t *testing.T) {
	m := newTestWizard()
	m.state = stateOptions
	m.optionsIndex = 0

	if m = sendW(m, keyDown()); m.optionsIndex != 1 {
		t.Fatalf("down: optionsIndex=%d want 1", m.optionsIndex)
	}
	if m = sendW(m, keyUp()); m.optionsIndex != 0 {
		t.Fatalf("up: optionsIndex=%d want 0", m.optionsIndex)
	}
	if m = sendW(m, keyRune('j')); m.optionsIndex != 1 {
		t.Fatalf("j: optionsIndex=%d want 1", m.optionsIndex)
	}
	if m = sendW(m, keyRune('k')); m.optionsIndex != 0 {
		t.Fatalf("k: optionsIndex=%d want 0", m.optionsIndex)
	}
}

// Space inside a text field must still type a literal space.
func TestSpaceTypedIntoTextField(t *testing.T) {
	m := newTestWizard()
	m.cfg.Mode = "feature"
	m.state = stateMission
	m.textInput.SetValue("dark")
	m = sendW(m, keyRune(' '))
	if got := m.textInput.Value(); got != "dark " {
		t.Fatalf("space not typed into text field: %q", got)
	}
}

// Reported corruption: a blank label must not shove the author into the label.
func TestQueueFilterBlankLabel(t *testing.T) {
	m := newTestWizard()
	m.cfg.Mode = "queue"
	m.state = stateQueueFilters
	m.queueSub = 0

	m.textInput.SetValue("") // blank label
	if m = sendW(m, keyEnter()); m.queueSub != 1 {
		t.Fatalf("blank label did not advance to author: queueSub=%d", m.queueSub)
	}
	m.textInput.SetValue("octocat")
	if m = sendW(m, keyEnter()); m.queueSub != 2 {
		t.Fatalf("author did not advance to limit: queueSub=%d", m.queueSub)
	}
	m.textInput.SetValue("5")
	m = sendW(m, keyEnter())

	q := m.cfg.IssueQueue
	if q == nil {
		t.Fatal("IssueQueue is nil")
	}
	if q.Label != "" {
		t.Errorf("label = %q, want empty", q.Label)
	}
	if q.Author != "octocat" {
		t.Errorf("author = %q, want octocat", q.Author)
	}
	if q.Limit != 5 {
		t.Errorf("limit = %d, want 5", q.Limit)
	}
	if m.state != stateAgents {
		t.Errorf("state = %v, want stateAgents", m.state)
	}
}

// Blank author must not capture the limit value as the author.
func TestQueueFilterBlankAuthor(t *testing.T) {
	m := newTestWizard()
	m.cfg.Mode = "queue"
	m.state = stateQueueFilters
	m.queueSub = 0

	m.textInput.SetValue("bug")
	m = sendW(m, keyEnter())
	m.textInput.SetValue("") // blank author
	m = sendW(m, keyEnter())
	m.textInput.SetValue("7")
	m = sendW(m, keyEnter())

	q := m.cfg.IssueQueue
	if q.Label != "bug" || q.Author != "" || q.Limit != 7 {
		t.Fatalf("got label=%q author=%q limit=%d, want bug/\"\"/7", q.Label, q.Author, q.Limit)
	}
}

func TestFeatureMissionRequired(t *testing.T) {
	m := newTestWizard()
	m.cfg.Mode = "feature"
	m.state = stateMission

	m.textInput.SetValue("   ") // whitespace only
	m = sendW(m, keyEnter())
	if m.state != stateMission {
		t.Fatalf("empty mission advanced past stateMission (state=%v)", m.state)
	}
	if m.err == nil {
		t.Fatal("expected a validation error for empty mission")
	}

	m.textInput.SetValue("add dark mode toggle")
	m = sendW(m, keyEnter())
	if m.state != stateAgents {
		t.Fatalf("valid mission did not advance to stateAgents (state=%v)", m.state)
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	s := "日本語のミッション説明文" // multibyte, longer than the limit
	got := truncate(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != 5 {
		t.Fatalf("truncate returned %d runes, want 5: %q", utf8.RuneCountInString(got), got)
	}
	if truncate("hi", 5) != "hi" {
		t.Fatal("short string should be returned unchanged")
	}
}

// Explicitly-configured per-step agents must be respected, not autodetected over.
func TestWizardRespectsConfiguredAgents(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Discover.Agent = "codex"
	cfg.Execute.Agent = "codex"
	cfg.Verify.Agent = "codex"
	cfg.Commit.Agent = "codex"
	m := NewWizardModel(cfg)
	if m.stepToAgent["discover"] != "codex" || m.stepToAgent["execute"] != "codex" {
		t.Fatalf("configured agents overridden: %v", m.stepToAgent)
	}
}
