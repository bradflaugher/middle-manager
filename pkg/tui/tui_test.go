package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/bradflaugher/middle-manager/pkg/agents"
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
func keyEsc() tea.KeyPressMsg        { return tea.KeyPressMsg{Code: tea.KeyEsc} }

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
	// Queue filters now lead to the strategy screen (per-issue vs worktree).
	if m.state != stateStrategy {
		t.Errorf("state = %v, want stateStrategy", m.state)
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
	// Loop shape now comes right after the mission (before agents).
	if m.state != stateSteps {
		t.Fatalf("valid mission did not advance to stateSteps (state=%v)", m.state)
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

// The new default in the agent picker is "random" for every step (issue #3),
// applied only when the incoming config is untouched.
func TestWizardDefaultsToRandom(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	for _, step := range []string{"discover", "execute", "verify", "commit"} {
		if m.stepToAgent[step] != "random" {
			t.Errorf("default %s agent = %q, want random", step, m.stepToAgent[step])
		}
	}
}

// The agent carousel must include "random" first, then cycle through the roster.
func TestCycleAgentIncludesRandom(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	m.stepToAgent["discover"] = "random"
	m.cycleAgent(0, 1) // forward off "random" → first concrete agent
	if m.stepToAgent["discover"] == "random" {
		t.Fatal("cycling forward off random should move to a concrete agent")
	}
	m.cycleAgent(0, -1) // back → "random" again
	if m.stepToAgent["discover"] != "random" {
		t.Fatalf("cycling back should return to random, got %q", m.stepToAgent["discover"])
	}
}

// Selecting the 1-step loop shape turns on solo mode (and its merge wait).
func TestWizardSoloSelection(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	m.state = stateSteps
	// stepsOptions is [4, 3, 1]; index 2 is solo.
	m.stepsIndex = 2
	m = sendW(m, keyEnter())
	if !m.cfg.Solo {
		t.Fatal("1-step selection did not enable Solo")
	}
	if !m.cfg.WaitForMerge {
		t.Fatal("Solo must enable WaitForMerge")
	}
	if m.cfg.Commit.Enabled {
		t.Fatal("Solo must disable the commit step")
	}
}

// Choosing the worktree strategy must remove solo from the loop-shape menu (the
// two are mutually exclusive), so the wizard can never emit a Solo+Worktree
// config that Validate() would reject.
func TestWizardWorktreeExcludesSolo(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	m.cfg.Mode = "queue"
	m.cfg.IssueQueue = &config.IssueQueueConfig{State: "open", Limit: 20}
	// Strategy screen: pick "worktree collapse" (index 1).
	m.state = stateStrategy
	m.strategyIndex = 1
	m = sendW(m, keyEnter())
	if !m.cfg.Worktree {
		t.Fatal("worktree strategy did not set cfg.Worktree")
	}
	if m.state != stateSteps {
		t.Fatalf("strategy should advance to the loop-shape step, got %v", m.state)
	}
	// The shape menu must NOT offer the solo (1-step) option under worktree.
	for _, s := range m.stepsChoices() {
		if s == 1 {
			t.Fatal("worktree mode must exclude the solo loop shape")
		}
	}
	if err := m.cfg.Validate(); err != nil {
		t.Fatalf("worktree queue config should be valid, got: %v", err)
	}
}

// The new/adaptive screens must render without panicking, including the tricky
// cases: solo's single agent row and a stale step index under worktree (2
// choices) left over from a non-worktree visit (3 choices).
func TestWizardRendersNewScreensWithoutPanic(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	m.width, m.height = 100, 40

	for _, st := range []wizardState{stateStrategy, stateSteps, stateAgents} {
		m.state = st
		_ = m.View()
	}

	// Solo: the agents screen shows exactly one (Execute) row.
	m.cfg.Solo, m.cfg.Steps = true, 1
	m.state = stateAgents
	_ = m.View()
	m.customAgents = true
	_ = m.View()

	// Worktree: loop shape has only 2 choices; a stale stepsIndex must not panic.
	m.cfg.Solo, m.cfg.Steps, m.cfg.Worktree = false, 4, true
	m.stepsIndex = 2
	m.state = stateSteps
	_ = m.View()
}

// Full queue path drives the new screen order forwards then backwards, locking
// the rewired transitions: filters → strategy → loop shape → agents, and esc
// unwinds them in reverse.
func TestWizardQueueNavigationOrder(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	m.cfg.Repo = os.TempDir()
	m.state = stateMode
	m.modeIndex = 3 // queue
	m = sendW(m, keyEnter())
	if m.state != stateQueueFilters {
		t.Fatalf("mode→ %v, want queueFilters", m.state)
	}
	m = sendW(m, keyEnter()) // blank label → author
	m = sendW(m, keyEnter()) // blank author → max-issues
	m.textInput.SetValue("10")
	m = sendW(m, keyEnter())
	if m.state != stateStrategy {
		t.Fatalf("filters→ %v, want strategy", m.state)
	}
	m = sendW(m, keyEnter()) // per-issue → loop shape
	if m.state != stateSteps {
		t.Fatalf("strategy→ %v, want steps", m.state)
	}
	m = sendW(m, keyEnter()) // 4-step → agents
	if m.state != stateAgents {
		t.Fatalf("steps→ %v, want agents", m.state)
	}
	// Unwind.
	for _, want := range []wizardState{stateSteps, stateStrategy, stateQueueFilters} {
		m = sendW(m, keyEsc())
		if m.state != want {
			t.Fatalf("esc → %v, want %v", m.state, want)
		}
	}
}

// Per-issue strategy keeps the solo shape available.
func TestWizardPerIssueKeepsSolo(t *testing.T) {
	m := NewWizardModel(config.NewDefaultConfig())
	m.cfg.Mode = "queue"
	m.state = stateStrategy
	m.strategyIndex = 0 // per-issue PRs
	m = sendW(m, keyEnter())
	if m.cfg.Worktree {
		t.Fatal("per-issue strategy must not set worktree")
	}
	hasSolo := false
	for _, s := range m.stepsChoices() {
		if s == 1 {
			hasSolo = true
		}
	}
	if !hasSolo {
		t.Fatal("per-issue strategy should keep the solo shape available")
	}
}

// rainbowText must produce visible, styled output (one ANSI-wrapped run per
// rune) and stay rune-safe for multibyte input.
// fakeInstalled pins EVERY builtin to a nonexistent binary (overrides never
// fall back to PATH) and marks just the listed agents as installed, so wizard
// tests don't depend on which CLIs the host machine happens to have.
func fakeInstalled(installed ...string) map[string]string {
	m := map[string]string{}
	for _, name := range agents.AgentNames {
		m[name] = "/nonexistent/mm-test-binary"
	}
	for _, name := range installed {
		m[name] = "/bin/sh"
	}
	return m
}

// The options screen owns the two new factory toggles. Escalation ON routes
// through the strength screen, whose user-defined ranking builds the ladder;
// OFF clears the ladder; a config-shaped ladder skips the screen untouched.
func TestWizardFactoryOptions(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.BinaryOverrides = fakeInstalled("claude", "codex", "opencode")
	cfg.Execute.Agent = "opencode"
	m := NewWizardModel(cfg)
	m.state = stateOptions
	if len(m.optionsList) != 8 || len(m.optionsValues) != 8 {
		t.Fatalf("options screen has %d/%d entries, want 8", len(m.optionsList), len(m.optionsValues))
	}

	m.optionsValues[5] = true // distinct verifier
	m.optionsValues[6] = true // escalation
	m = sendW(m, keyEnter())  // commit options → strength screen

	if !m.cfg.DistinctVerifier {
		t.Error("distinct-verifier checkbox not applied")
	}
	if m.state != stateStrength {
		t.Fatalf("escalation ON should route to the strength screen, got state %d", m.state)
	}
	// Installed agents seeded strongest-first per the default ordering.
	want := []string{"claude", "codex", "opencode"}
	if len(m.strengthOrder) != 3 || m.strengthOrder[0] != want[0] || m.strengthOrder[1] != want[1] || m.strengthOrder[2] != want[2] {
		t.Fatalf("seeded strength order = %v, want %v", m.strengthOrder, want)
	}

	// The user promotes codex to strongest: cursor starts at 0... move to row 1
	// then drag it up one (shift+up).
	m = sendW(m, keyDown())
	m = sendW(m, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if m.strengthOrder[0] != "codex" || m.strengthIndex != 0 {
		t.Fatalf("shift+up did not promote codex: %v (cursor %d)", m.strengthOrder, m.strengthIndex)
	}

	m = sendW(m, keyEnter()) // commit strength → maxIters
	if m.state != stateMaxIters {
		t.Fatalf("strength commit should land on maxIters, got %d", m.state)
	}
	if len(m.cfg.StrengthOrder) != 3 || m.cfg.StrengthOrder[0] != "codex" {
		t.Errorf("StrengthOrder not committed: %v", m.cfg.StrengthOrder)
	}
	// Base opencode is ranked #3 → ladder climbs claude (nearer) then codex (top).
	if len(m.cfg.Execute.Escalate) != 2 || m.cfg.Execute.Escalate[0].Agent != "claude" || m.cfg.Execute.Escalate[1].Agent != "codex" {
		t.Errorf("ladder = %+v, want [claude codex]", m.cfg.Execute.Escalate)
	}

	// Toggling escalation OFF must clear the ladder and skip the strength screen.
	cfg2 := config.NewDefaultConfig()
	cfg2.BinaryOverrides = fakeInstalled("claude", "codex")
	m2 := NewWizardModel(cfg2)
	m2.state = stateOptions
	m2.cfg.Execute.Escalate = []config.AgentRef{{Agent: "claude"}}
	m2.optionsValues[6] = false
	m2 = sendW(m2, keyEnter())
	if m2.state != stateMaxIters || len(m2.cfg.Execute.Escalate) != 0 {
		t.Errorf("escalation OFF: state=%d ladder=%+v", m2.state, m2.cfg.Execute.Escalate)
	}

	// A config-shaped ladder survives when the toggle stays ON, no screen shown.
	cfg3 := config.NewDefaultConfig()
	cfg3.BinaryOverrides = fakeInstalled("claude", "codex")
	cfg3.Execute.Escalate = []config.AgentRef{{Agent: "claude", Model: "opus"}, {Agent: "codex"}}
	m3 := NewWizardModel(cfg3)
	m3.state = stateOptions
	m3.optionsValues[6] = true
	m3 = sendW(m3, keyEnter())
	if m3.state != stateMaxIters {
		t.Errorf("config-ladder path should skip the strength screen, got state %d", m3.state)
	}
	if len(m3.cfg.Execute.Escalate) != 2 {
		t.Errorf("config ladder clobbered: %+v", m3.cfg.Execute.Escalate)
	}
}

// escalationLadder must climb the ranking gradually: every installed agent
// stronger than the base, nearest first; unranked bases jump to the strongest;
// a base already at the top gets no ladder.
func TestEscalationLadder(t *testing.T) {
	overrides := fakeInstalled() // nothing installed
	if got := escalationLadder("opencode", overrides, nil); got != nil {
		t.Errorf("no installed agents should yield no ladder, got %+v", got)
	}

	overrides = fakeInstalled("claude", "codex", "opencode")
	got := escalationLadder("opencode", overrides, nil) // default order: claude > codex > opencode
	if len(got) != 2 || got[0].Agent != "codex" || got[1].Agent != "claude" {
		t.Errorf("ladder = %+v, want [codex claude]", got)
	}
	// User ranking flips claude and codex.
	got = escalationLadder("opencode", overrides, []string{"codex", "claude", "opencode"})
	if len(got) != 2 || got[0].Agent != "claude" || got[1].Agent != "codex" {
		t.Errorf("user-ranked ladder = %+v, want [claude codex]", got)
	}
	// The strongest agent has nowhere to climb.
	if got := escalationLadder("claude", overrides, nil); got != nil {
		t.Errorf("strongest base should have no ladder, got %+v", got)
	}
	// Random (unranked) base escalates straight to the user's strongest.
	got = escalationLadder(agents.RandomAgent, overrides, []string{"codex", "claude"})
	if len(got) != 1 || got[0].Agent != "codex" {
		t.Errorf("random base ladder = %+v, want [codex]", got)
	}
}

// The wizard must carry the seat playbook: agents screen shows where strong
// and weak models go; the loop-shape screen warns solo isn't a savings mode.
func TestWizardPlaybookGuidance(t *testing.T) {
	m := newTestWizard()
	m.state = stateAgents
	view := stripANSITest(m.View().Content)
	if !strings.Contains(view, "Playbook: strongest model") || !strings.Contains(view, "cheapest") {
		t.Errorf("agents screen missing playbook hint: %q", view)
	}
	m.customAgents = true
	view = stripANSITest(m.View().Content)
	if !strings.Contains(view, "Playbook: strongest model") {
		t.Error("customize view must keep the playbook hint")
	}

	m2 := newTestWizard()
	m2.state = stateSteps
	view = stripANSITest(m2.View().Content)
	if !strings.Contains(view, "convenience, not savings") {
		t.Errorf("loop-shape screen missing the solo tip: %q", view)
	}
}

// The Models screen appears only for concrete, model-capable seats; cycling a
// row pins that step's Model, "(CLI default)" clears it; random-only
// configurations skip the screen entirely.
func TestWizardModelsScreen(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.BinaryOverrides = fakeInstalled("claude", "codex")
	m := NewWizardModel(cfg)
	m.state = stateAgents
	for _, step := range []string{"discover", "execute", "verify", "commit"} {
		m.stepToAgent[step] = "claude"
	}
	m = sendW(m, keyEnter())
	if m.state != stateModels {
		t.Fatalf("concrete model-capable seats should route to stateModels, got %d", m.state)
	}
	if len(m.modelRowsCache) != 4 {
		t.Fatalf("modelRows = %v, want 4 claude rows", m.modelRowsCache)
	}
	// Row 0 = discover; claude's curated suggestions start with "fable".
	m = sendW(m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = sendW(m, keyEnter()) // commit models -> options
	if m.state != stateOptions {
		t.Fatalf("models commit should land on options, got %d", m.state)
	}
	if m.cfg.Discover.Model != "fable" {
		t.Errorf("discover model = %q, want fable", m.cfg.Discover.Model)
	}
	if m.cfg.Execute.Model != "" {
		t.Errorf("untouched rows must keep the CLI default, got %q", m.cfg.Execute.Model)
	}

	// All-random seats: no models screen. (Fresh config — the first wizard
	// committed concrete agents into the shared one.)
	cfg2 := config.NewDefaultConfig()
	cfg2.BinaryOverrides = fakeInstalled("claude", "codex")
	m2 := NewWizardModel(cfg2)
	m2.state = stateAgents
	m2 = sendW(m2, keyEnter())
	if m2.state != stateOptions {
		t.Errorf("random seats should skip stateModels, got %d", m2.state)
	}

	// A live listing arriving grows the carousel without breaking choices.
	model, _ := m.Update(wizardModelsMsg{agent: "claude", models: []string{"claude-x-test"}})
	m = model.(*WizardModel)
	found := false
	for _, o := range m.modelOptionsFor("discover") {
		if o == "claude-x-test" {
			found = true
		}
	}
	if !found {
		t.Error("fetched models did not join the carousel")
	}
}

// The Spend screen is gated by the options toggle, lists every agent the
// configuration could run, defaults from DefaultSpendRates (zero for the free
// seat), and commits cfg.SpendRates.
func TestWizardSpendScreen(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.BinaryOverrides = fakeInstalled("claude", "opencode")
	m := NewWizardModel(cfg)
	m.state = stateOptions
	m.optionsValues[6] = false // no escalation -> no strength screen in between
	m.optionsValues[7] = true  // track spend
	m.cfg.Execute.Agent = "opencode"
	m.cfg.Discover.Agent = "claude"
	m.cfg.Verify.Agent = "claude"
	m.cfg.Commit.Agent = "claude"
	m.stepToAgent = map[string]string{"discover": "claude", "execute": "opencode", "verify": "claude", "commit": "claude"}

	m = sendW(m, keyEnter())
	if m.state != stateSpend {
		t.Fatalf("spend toggle should route to stateSpend, got %d", m.state)
	}
	rows := map[string]bool{}
	for _, r := range m.spendRowsCache {
		rows[r] = true
	}
	if !rows["claude"] || !rows["opencode"] {
		t.Fatalf("spend rows = %v, want claude+opencode", m.spendRowsCache)
	}
	// Defaults: opencode seeds at the $0.00 (free/subscription/local) preset.
	if spendPresets[m.spendChoice["opencode"]] != 0 {
		t.Errorf("opencode should default to $0.00, got %v", spendPresets[m.spendChoice["opencode"]])
	}

	m = sendW(m, keyEnter()) // commit spend -> maxIters
	if m.state != stateMaxIters {
		t.Fatalf("spend commit should land on maxIters, got %d", m.state)
	}
	if m.cfg.SpendRates["opencode"] != 0 {
		t.Errorf("SpendRates[opencode] = %v, want 0", m.cfg.SpendRates["opencode"])
	}
	if m.cfg.SpendRates["claude"] != config.DefaultSpendRates["claude"] {
		t.Errorf("SpendRates[claude] = %v, want default %v", m.cfg.SpendRates["claude"], config.DefaultSpendRates["claude"])
	}

	// Toggle off -> screen skipped, straight to maxIters.
	m3 := NewWizardModel(cfg)
	m3.state = stateOptions
	m3.optionsValues[6] = false
	m3.optionsValues[7] = false
	m3 = sendW(m3, keyEnter())
	if m3.state != stateMaxIters {
		t.Errorf("spend off should skip stateSpend, got %d", m3.state)
	}
}

func TestRainbowTextRenders(t *testing.T) {
	out := rainbowText("random", 0)
	if stripANSITest(out) != "random" {
		t.Fatalf("rainbow stripped = %q, want random", stripANSITest(out))
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("rainbow output carries no ANSI color")
	}
	// Different frames must differ (animation actually moves).
	if rainbowText("random", 0) == rainbowText("random", 10) {
		t.Fatal("rainbow did not change across frames")
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

// gradientText must keep the visible width of its input (styling only) and
// handle degenerate inputs without panicking.
func TestGradientTextWidth(t *testing.T) {
	got := gradientText("middle-manager", cMagenta, cViolet, cCyan)
	if w := utf8.RuneCountInString(stripANSITest(got)); w != len("middle-manager") {
		t.Fatalf("gradient changed visible width: %d", w)
	}
	if gradientText("", cMagenta) != "" {
		t.Fatal("empty input must render empty")
	}
	if utf8.RuneCountInString(stripANSITest(gradientText("x", cMagenta, cCyan))) != 1 {
		t.Fatal("single rune must render one cell")
	}
}

// The wizard breadcrumb adapts its length to the selected mode's flow and
// reports a sane position counter.
func TestWizardBreadcrumbAdaptsToMode(t *testing.T) {
	// One installed agent: both factory toggles default OFF, so the flow has no
	// strength screen — the original 9-screen feature / 10-screen queue shape.
	cfg := config.NewDefaultConfig()
	cfg.BinaryOverrides = fakeInstalled("claude")
	m := NewWizardModel(cfg)
	if !strings.Contains(stripANSITest(m.breadcrumb()), "1/9") {
		t.Fatalf("feature flow should start at 1/9: %q", stripANSITest(m.breadcrumb()))
	}
	// Highlighting the queue mode previews its longer flow (extra strategy step).
	for i, mode := range m.modes {
		if mode == "queue" {
			m.modeIndex = i
		}
	}
	if !strings.Contains(stripANSITest(m.breadcrumb()), "/10") {
		t.Fatalf("queue flow should preview 10 steps: %q", stripANSITest(m.breadcrumb()))
	}
	if got, want := len(m.flow()), 10; got != want {
		t.Fatalf("queue flow length = %d, want %d", got, want)
	}

	// Two+ installed agents: escalation defaults ON, adding the strength screen.
	cfg2 := config.NewDefaultConfig()
	cfg2.BinaryOverrides = fakeInstalled("claude", "codex")
	m2 := NewWizardModel(cfg2)
	if got, want := len(m2.flow()), 10; got != want {
		t.Fatalf("feature flow with strength screen = %d, want %d", got, want)
	}
}

// Follow mode: appended output must NOT yank the view down while the operator
// has scrolled up, and must keep auto-scrolling while they are at the bottom.
func TestPushLogFollowMode(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})
	m.logViewport.SetHeight(4)
	m.logViewport.SetWidth(40)
	for i := 0; i < 30; i++ {
		m.pushLog("line\n")
	}
	if !m.logViewport.AtBottom() {
		t.Fatal("should follow output while at the bottom")
	}
	m.logViewport.GotoTop()
	m.pushLog("new line while scrolled\n")
	if m.logViewport.AtBottom() {
		t.Fatal("append must not force-scroll while the operator is reading history")
	}
	m.logViewport.GotoBottom()
	m.pushLog("another\n")
	if !m.logViewport.AtBottom() {
		t.Fatal("follow mode must resume once back at the bottom")
	}
}

// A step change restarts the live pill timer; a repeat of the same step must
// not (the timer measures the step, not the message cadence).
func TestStepTimerResetsOnStepChange(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})
	m.Update(TUIStatusMsg{Step: "execute", State: "running"})
	first := m.stepStart
	if first.IsZero() {
		t.Fatal("step start not recorded")
	}
	m.Update(TUIStatusMsg{Step: "execute", State: "running"})
	if !m.stepStart.Equal(first) {
		t.Fatal("same-step status must not restart the timer")
	}
	m.Update(TUIStatusMsg{Step: "verify", State: "running"})
	if m.stepStart.Equal(first) {
		t.Fatal("new step must restart the timer")
	}
}

// The iteration budget bar must render inside the dashboard panel and cap at
// 100% even if iteration overshoots MaxIterations.
func TestDashboardIterationBar(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x", MaxIterations: 10})
	m.width, m.height = 100, 40
	m.iteration = 3
	out := stripANSITest(m.panelsRow())
	if !strings.Contains(out, "3/10") {
		t.Fatalf("dashboard missing iteration fraction: %q", out)
	}
	m.iteration = 99 // overshoot must not panic or exceed the bar
	_ = m.panelsRow()
}
