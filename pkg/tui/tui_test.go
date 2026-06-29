package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
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

// ---------------------------------------------------------------------------
// Monitor: batch-queue dashboard
// ---------------------------------------------------------------------------

// sendM drives one message through the monitor's Update and returns the model.
func sendM(m *MonitorModel, msg tea.Msg) *MonitorModel {
	model, _ := m.Update(msg)
	return model.(*MonitorModel)
}

// A queue update must surface position both on the title bar (survives panel
// shedding on short screens) and in the dashboard panel with the issue label.
func TestQueueProgressRenders(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/artwork", Mode: "queue"})
	m.width, m.height = 100, 32
	m = sendM(m, TUIQueueMsg{Position: 2, Total: 7, Issue: "#123 Fix the thing"})

	if m.queuePos != 2 || m.queueTotal != 7 {
		t.Fatalf("queue fields not set: pos=%d total=%d", m.queuePos, m.queueTotal)
	}
	if title := stripANSITest(m.titleRow(100)); !strings.Contains(title, "queue 2/7") {
		t.Fatalf("title row missing queue progress: %q", title)
	}
	panels := stripANSITest(m.panelsRow())
	if !strings.Contains(panels, "2/7") {
		t.Fatalf("dashboard missing queue count: %q", panels)
	}
	if !strings.Contains(panels, "#123") {
		t.Fatalf("dashboard missing issue label: %q", panels)
	}
}

// A single run (no queue) must show none of the queue chrome.
func TestNoQueueChromeForSingleRun(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x", Mode: "feature"})
	m.width, m.height = 100, 32
	if title := stripANSITest(m.titleRow(100)); strings.Contains(title, "queue") {
		t.Fatalf("single-run title should not show queue chrome: %q", title)
	}
	if panels := stripANSITest(m.panelsRow()); strings.Contains(panels, "queue") {
		t.Fatalf("single-run dashboard should not show queue row: %q", panels)
	}
}

// TUIDoneMsg is the whole reason the queue terminal state is a separate message:
// it must NOT clobber the last issue's iteration/branch the way a status update
// would, yet must still flip state and post the exit notice.
func TestDoneMsgPreservesLastIssueContext(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x", Mode: "queue"})
	m = sendM(m, TUIStatusMsg{Iteration: 4, Step: "verify", Agent: "claude", State: "running", Branch: "mm/issue-9", Duration: time.Second})
	m = sendM(m, TUIDoneMsg{State: "completed"})

	if m.state != "completed" {
		t.Fatalf("state = %q, want completed", m.state)
	}
	if m.iteration != 4 {
		t.Fatalf("iteration clobbered by done msg: %d, want 4", m.iteration)
	}
	if m.branch != "mm/issue-9" {
		t.Fatalf("branch clobbered by done msg: %q, want mm/issue-9", m.branch)
	}
}

// Terminal-notice wording adapts to queue vs single run, success vs failure.
func TestTerminalNoticeWording(t *testing.T) {
	cases := []struct {
		name       string
		queue      bool
		state      string
		wantPhrase string
	}{
		{"queue success", true, "completed", "Queue finished. Press Enter to exit."},
		{"queue failure", true, "failed", "Queue finished with failures. Press Enter to exit."},
		{"single success", false, "completed", "Loop completed successfully. Press Enter to exit."},
		{"single failure", false, "failed", "Loop failed. Press Enter to exit."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})
			if tc.queue {
				m = sendM(m, TUIQueueMsg{Position: 1, Total: 3, Issue: "#1 x"})
			}
			m = sendM(m, TUIDoneMsg{State: tc.state})
			content := stripANSITest(m.logViewport.GetContent())
			if !strings.Contains(content, tc.wantPhrase) {
				t.Fatalf("notice = %q, want it to contain %q", content, tc.wantPhrase)
			}
		})
	}
}

// The terminal notice must fire exactly once per terminal transition, not on
// every repeated done/status message (guards the prevState != state check).
func TestTerminalNoticeFiresOnce(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x", Mode: "queue"})
	m = sendM(m, TUIDoneMsg{State: "completed"})
	m = sendM(m, TUIDoneMsg{State: "completed"})
	content := stripANSITest(m.logViewport.GetContent())
	if n := strings.Count(content, "Press Enter to exit."); n != 1 {
		t.Fatalf("terminal notice rendered %d times, want exactly 1", n)
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
