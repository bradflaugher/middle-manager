package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/config"
)

// ---------------------------------------------------------------------------
// Palette & shared styles  (synthwave-ish, tuned for dark terminals)
// ---------------------------------------------------------------------------

var (
	cMagenta = lipgloss.Color("#FF5FD7")
	cCyan    = lipgloss.Color("#36E2E2")
	cViolet  = lipgloss.Color("#9D7CFF")
	cGreen   = lipgloss.Color("#3DF5A0")
	cAmber   = lipgloss.Color("#FFC857")
	cRed     = lipgloss.Color("#FF5C72")
	cFg      = lipgloss.Color("#E6E6F0")
	cDim     = lipgloss.Color("#6C7086")
	cInk     = lipgloss.Color("#11111B")
)

var (
	stBold  = lipgloss.NewStyle().Bold(true)
	stFg    = lipgloss.NewStyle().Foreground(cFg)
	stDim   = lipgloss.NewStyle().Foreground(cDim)
	stCyan  = lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	stMag   = lipgloss.NewStyle().Foreground(cMagenta).Bold(true)
	stViol  = lipgloss.NewStyle().Foreground(cViolet).Bold(true)
	stGreen = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	stAmber = lipgloss.NewStyle().Foreground(cAmber).Bold(true)
	stRed   = lipgloss.NewStyle().Foreground(cRed).Bold(true)

	titleBar = lipgloss.NewStyle().
			Bold(true).
			Foreground(cInk).
			Background(cMagenta).
			Padding(0, 2)

	titleTag = lipgloss.NewStyle().
			Foreground(cInk).
			Background(cCyan).
			Padding(0, 1)

	panel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cViolet).
		Padding(0, 1)

	panelLabel = lipgloss.NewStyle().
			Foreground(cMagenta).
			Bold(true)

	logPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cCyan).
			Padding(0, 1)

	inputBar = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, false, false, true).
			BorderForeground(cMagenta).
			PaddingLeft(1)
)

var brailleFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinFrame(frame int) string { return brailleFrames[frame%len(brailleFrames)] }

// stepGlyph returns the emoji-free icon shown for each pipeline step.
var stepLabels = []string{"discover", "execute", "verify", "commit"}

// ---------------------------------------------------------------------------
// Banner / one-shot render helpers (used by main + merge mode)
// ---------------------------------------------------------------------------

// RenderBanner produces the masthead shown by `mm` headers.
func RenderBanner(version string) string {
	logo := stMag.Render("▌") + stViol.Render("▐ ") +
		titleBar.Render("middle-manager") + " " +
		titleTag.Render(version)
	tag := stDim.Render("  micromanaged multi-agent coding loop · bring your own agents")
	return logo + "\n" + tag
}

func RenderError(msg string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cRed).
		Padding(0, 1).Render(stRed.Render("✗ ") + stFg.Render(msg))
}

func RenderInfo(msg string) string {
	return stDim.Render("• ") + stFg.Render(msg)
}

// ---- merge-mode rendering ----

type MergeRow struct {
	Number int
	Title  string
	Author string
	Status string // merged | skip | failed
	Reason string
}

func RenderMergeHeader(repo, author, label string, requireChecks, dryRun bool) string {
	title := titleBar.Render("merge mode") + " " +
		stDim.Render(filepath.Base(repo))
	var filters []string
	if author != "" {
		filters = append(filters, "author="+author)
	}
	if label != "" {
		filters = append(filters, "label="+label)
	}
	if requireChecks {
		filters = append(filters, "require green checks")
	} else {
		filters = append(filters, stAmber.Render("checks NOT required"))
	}
	if dryRun {
		filters = append(filters, stAmber.Render("DRY RUN"))
	}
	return "\n" + title + "\n" + stDim.Render("  "+strings.Join(filters, " · ")) + "\n"
}

func RenderMergeTable(rows []MergeRow) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rows {
		var badge string
		switch r.Status {
		case "merged":
			badge = stGreen.Render("✓ merged")
		case "failed":
			badge = stRed.Render("✗ failed")
		default:
			badge = stDim.Render("· skip  ")
		}
		title := r.Title
		if len(title) > 48 {
			title = title[:47] + "…"
		}
		line := fmt.Sprintf("  %s  %s %s  %s",
			badge,
			stViol.Render(fmt.Sprintf("#%-4d", r.Number)),
			stFg.Render(fmt.Sprintf("%-49s", title)),
			stDim.Render(r.Reason),
		)
		b.WriteString(line + "\n")
	}
	return b.String()
}

func RenderMergeSummary(merged, skipped, failed int, dryRun bool) string {
	verb := "merged"
	if dryRun {
		verb = "would merge"
	}
	parts := []string{
		stGreen.Render(fmt.Sprintf("%d %s", merged, verb)),
		stDim.Render(fmt.Sprintf("%d skipped", skipped)),
	}
	if failed > 0 {
		parts = append(parts, stRed.Render(fmt.Sprintf("%d failed", failed)))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cGreen).Padding(0, 2)
	return box.Render(strings.Join(parts, "   ")) + "\n"
}

// RenderSummaryPanel renders the end-of-run summary box for the loop.
func RenderSummaryPanel(success bool, reason, prURL string, iterations int, mission string) string {
	if success {
		body := stGreen.Render("✓ loop complete") + "\n\n" +
			stFg.Render("mission: ") + stFg.Render(truncate(mission, 60)) + "\n" +
			stFg.Render("iterations: ") + stBold.Render(strconv.Itoa(iterations))
		if prURL != "" {
			body += "\n" + stFg.Render("PR: ") + stCyan.Render(prURL)
		}
		return lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(cGreen).
			Padding(0, 2).Render(body)
	}
	body := stAmber.Render("⚠ loop did not complete") + "\n\n" +
		stFg.Render("reason: ") + stFg.Render(reason) + "\n" +
		stFg.Render("iterations: ") + stBold.Render(strconv.Itoa(iterations))
	if prURL != "" {
		body += "\n" + stFg.Render("PR: ") + stCyan.Render(prURL)
	}
	return lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(cAmber).
		Padding(0, 2).Render(body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func statDir(path string) (os.FileInfo, error) { return os.Stat(path) }

// ===========================================================================
// WIZARD
// ===========================================================================

type wizardState int

const (
	stateRepo wizardState = iota
	stateMode
	stateMission
	stateIssueDetails
	stateQueueFilters
	stateMergeFilters
	stateAgents
	stateSteps
	stateOptions
	stateMaxIters
	stateConfirm
)

type WizardModel struct {
	state         wizardState
	cfg           *config.LoopConfig
	textInput     textinput.Model
	err           error
	done          bool
	quitting      bool
	frame         int
	modeIndex     int
	modes         []string
	modeLabels    []string
	customAgents  bool
	agentIndex    int
	stepToAgent   map[string]string
	stepsIndex    int
	stepsOptions  []int
	optionsIndex  int
	optionsList   []string
	optionsValues []bool
	queueLabel    string
	queueAuthor   string
	queueLimit    string
	confirmed     bool
}

func NewWizardModel(initialCfg *config.LoopConfig) *WizardModel {
	ti := textinput.New()
	ti.Placeholder = "Repository path"
	ti.Focus()
	ti.SetValue(initialCfg.Repo)
	ti.CharLimit = 400
	ti.SetWidth(60)

	stepToAgent := map[string]string{
		"discover": initialCfg.Discover.Agent,
		"execute":  initialCfg.Execute.Agent,
		"verify":   initialCfg.Verify.Agent,
		"commit":   initialCfg.Commit.Agent,
	}
	if detected := agents.AutodetectStepAgents(initialCfg.BinaryOverrides); len(agents.AvailableAgents(initialCfg.BinaryOverrides)) > 0 {
		stepToAgent = detected
	}

	return &WizardModel{
		state:     stateRepo,
		cfg:       initialCfg,
		textInput: ti,
		modes:     []string{"feature", "repair", "issue", "queue", "merge"},
		modeLabels: []string{
			"Build something new  —  \"add feature XYZ\"   (recommended)",
			"Discover & fix problems across the codebase",
			"Work a single GitHub issue",
			"Batch-drain a filtered queue of GitHub issues",
			"Merge ready open PRs (no agents, just ship green ones)",
		},
		stepToAgent:   stepToAgent,
		stepsOptions:  []int{4, 3},
		optionsList:   []string{"YOLO mode (auto-approve)", "Interactive pause between steps", "Allow fixing unrelated test failures", "Fresh run (reset loop state)"},
		optionsValues: []bool{initialCfg.Yolo, initialCfg.Interactive, initialCfg.FixUnrelatedTests, initialCfg.Fresh},
	}
}

type wizardTickMsg struct{}

func wizardTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return wizardTickMsg{} })
}

func (m *WizardModel) Init() tea.Cmd { return tea.Batch(textinput.Blink, wizardTick()) }

func (m *WizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case wizardTickMsg:
		m.frame++
		return m, wizardTick()
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			return m.nextStep()
		case "esc":
			return m.prevStep()
		case "c", "C":
			if m.state == stateAgents {
				m.customAgents = !m.customAgents
				m.agentIndex = 0
			}
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "left", "h":
			if m.state == stateAgents && m.customAgents {
				m.cycleAgent(m.agentIndex, -1)
			}
		case "right", "l":
			if m.state == stateAgents && m.customAgents {
				m.cycleAgent(m.agentIndex, 1)
			}
		case " ":
			if m.state == stateOptions {
				m.optionsValues[m.optionsIndex] = !m.optionsValues[m.optionsIndex]
			}
		}
	}

	if m.isTextState() {
		m.textInput, cmd = m.textInput.Update(msg)
	}
	return m, cmd
}

func (m *WizardModel) isTextState() bool {
	switch m.state {
	case stateRepo, stateMission, stateIssueDetails, stateQueueFilters, stateMergeFilters, stateMaxIters:
		return true
	}
	return false
}

func (m *WizardModel) moveCursor(dir int) {
	switch m.state {
	case stateMode:
		m.modeIndex = wrap(m.modeIndex+dir, len(m.modes))
	case stateSteps:
		m.stepsIndex = wrap(m.stepsIndex+dir, len(m.stepsOptions))
	case stateOptions:
		m.optionsIndex = wrap(m.optionsIndex+dir, len(m.optionsList))
	case stateAgents:
		if m.customAgents {
			m.agentIndex = wrap(m.agentIndex+dir, 4)
		}
	}
}

func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

func (m *WizardModel) nextStep() (tea.Model, tea.Cmd) {
	m.err = nil
	switch m.state {
	case stateRepo:
		repoPath := strings.TrimSpace(m.textInput.Value())
		abs, err := filepath.Abs(repoPath)
		if err != nil {
			m.err = fmt.Errorf("invalid path: %w", err)
			return m, nil
		}
		if _, err := statDir(abs); err != nil {
			m.err = fmt.Errorf("path does not exist: %s", abs)
			return m, nil
		}
		m.cfg.Repo = abs
		m.state = stateMode

	case stateMode:
		m.cfg.Mode = m.modes[m.modeIndex]
		switch m.cfg.Mode {
		case "feature", "repair":
			m.state = stateMission
			m.resetInput("Mission (e.g. add dark-mode toggle)")
		case "issue":
			m.state = stateIssueDetails
			m.resetInput("Issue number or URL")
		case "queue":
			m.state = stateQueueFilters
			m.resetInput("Label filter (blank for none)")
		case "merge":
			if m.cfg.MergeMethod == "" {
				m.cfg.MergeMethod = "squash"
			}
			m.state = stateMergeFilters
			m.resetInput("PR author filter (blank = all)")
		}

	case stateMission:
		m.cfg.Mission = strings.TrimSpace(m.textInput.Value())
		m.state = stateAgents

	case stateIssueDetails:
		m.cfg.Issue = strings.TrimSpace(m.textInput.Value())
		if m.cfg.Issue == "" {
			m.err = fmt.Errorf("issue number/url is required")
			return m, nil
		}
		m.state = stateAgents

	case stateQueueFilters:
		val := strings.TrimSpace(m.textInput.Value())
		if m.cfg.IssueQueue == nil {
			m.cfg.IssueQueue = &config.IssueQueueConfig{State: "open", Limit: 20, CloseOnSuccess: true}
		}
		if m.queueLabel == "" && m.queueAuthor == "" && m.queueLimit == "" {
			m.queueLabel = val
			m.resetInput("Author filter (blank for none)")
		} else if m.queueAuthor == "" && m.queueLimit == "" {
			m.queueAuthor = val
			m.resetInput("Max issues (default 20)")
		} else {
			m.queueLimit = val
			limit, err := strconv.Atoi(m.queueLimit)
			if err != nil || limit <= 0 {
				limit = 20
			}
			m.cfg.IssueQueue.Label = m.queueLabel
			m.cfg.IssueQueue.Author = m.queueAuthor
			m.cfg.IssueQueue.Limit = limit
			m.state = stateAgents
		}

	case stateMergeFilters:
		m.cfg.MergeAuthor = strings.TrimSpace(m.textInput.Value())
		m.state = stateConfirm

	case stateAgents:
		m.cfg.Discover.Agent = m.stepToAgent["discover"]
		m.cfg.Execute.Agent = m.stepToAgent["execute"]
		m.cfg.Verify.Agent = m.stepToAgent["verify"]
		m.cfg.Commit.Agent = m.stepToAgent["commit"]
		m.state = stateSteps

	case stateSteps:
		m.cfg.Steps = m.stepsOptions[m.stepsIndex]
		m.cfg.Commit.Enabled = m.cfg.Steps == 4
		m.state = stateOptions

	case stateOptions:
		m.cfg.Yolo = m.optionsValues[0]
		m.cfg.Interactive = m.optionsValues[1]
		m.cfg.FixUnrelatedTests = m.optionsValues[2]
		m.cfg.Fresh = m.optionsValues[3]
		m.state = stateMaxIters
		m.resetInput("Max iterations per task (default 10)")
		m.textInput.SetValue("10")

	case stateMaxIters:
		val := strings.TrimSpace(m.textInput.Value())
		iters, err := strconv.Atoi(val)
		if err != nil || iters <= 0 {
			iters = 10
		}
		m.cfg.MaxIterations = iters
		m.state = stateConfirm

	case stateConfirm:
		m.confirmed = true
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *WizardModel) resetInput(placeholder string) {
	m.textInput.Reset()
	m.textInput.Placeholder = placeholder
	m.textInput.Focus()
}

func (m *WizardModel) prevStep() (tea.Model, tea.Cmd) {
	m.err = nil
	switch m.state {
	case stateMode:
		m.state = stateRepo
		m.textInput.SetValue(m.cfg.Repo)
		m.textInput.Focus()
	case stateMission, stateIssueDetails, stateQueueFilters, stateMergeFilters:
		m.state = stateMode
	case stateAgents:
		switch m.cfg.Mode {
		case "issue":
			m.state = stateIssueDetails
			m.textInput.SetValue(m.cfg.Issue)
		case "queue":
			m.state = stateQueueFilters
			m.textInput.SetValue(m.queueLabel)
			m.queueLabel, m.queueAuthor, m.queueLimit = "", "", ""
		default:
			m.state = stateMission
			m.textInput.SetValue(m.cfg.Mission)
		}
		m.textInput.Focus()
	case stateSteps:
		m.state = stateAgents
	case stateOptions:
		m.state = stateSteps
	case stateMaxIters:
		m.state = stateOptions
	case stateConfirm:
		if m.cfg.Mode == "merge" {
			m.state = stateMergeFilters
			m.textInput.SetValue(m.cfg.MergeAuthor)
			m.textInput.Focus()
		} else {
			m.state = stateMaxIters
		}
	}
	return m, nil
}

func (m *WizardModel) cycleAgent(stepIdx, dir int) {
	step := stepLabels[stepIdx]
	current := m.stepToAgent[step]
	idx := 0
	for i, name := range agents.AgentNames {
		if name == current {
			idx = i
			break
		}
	}
	idx = wrap(idx+dir, len(agents.AgentNames))
	m.stepToAgent[step] = agents.AgentNames[idx]
}

func (m *WizardModel) View() tea.View {
	if m.quitting {
		return tea.NewView(stDim.Render("Aborted.\n"))
	}

	var s strings.Builder
	s.WriteString(RenderBanner("setup") + "\n\n")

	if m.err != nil {
		s.WriteString(stRed.Render("  ✗ "+m.err.Error()) + "\n\n")
	}

	switch m.state {
	case stateRepo:
		s.WriteString(stepHeader(1, "Repository"))
		s.WriteString("  Where is the codebase?\n\n  " + m.textInput.View() + "\n")
	case stateMode:
		s.WriteString(stepHeader(2, "What do you want to do?"))
		for i, label := range m.modeLabels {
			s.WriteString(radio(i == m.modeIndex, label))
		}
	case stateMission:
		s.WriteString(stepHeader(3, "Mission"))
		s.WriteString("  What should the agents build or fix?\n\n  " + m.textInput.View() + "\n")
	case stateIssueDetails:
		s.WriteString(stepHeader(3, "GitHub issue"))
		s.WriteString("  Issue number (e.g. 42) or URL:\n\n  " + m.textInput.View() + "\n")
	case stateQueueFilters:
		s.WriteString(stepHeader(3, "Queue filters"))
		hint := "Filter by label (optional):"
		if m.queueLabel != "" && m.queueAuthor == "" {
			hint = "Filter by author login (optional):"
		} else if m.queueAuthor != "" {
			hint = "Max issues (default 20):"
		}
		s.WriteString("  " + hint + "\n\n  " + m.textInput.View() + "\n")
	case stateMergeFilters:
		s.WriteString(stepHeader(3, "Merge filters"))
		s.WriteString("  Only merge PRs by this author (blank = everyone):\n\n  " + m.textInput.View() + "\n")
		s.WriteString("\n" + stDim.Render("  Only PRs that are mergeable with green checks will be merged.") + "\n")
	case stateAgents:
		s.WriteString(stepHeader(4, "Agents per step"))
		if m.customAgents {
			s.WriteString("  Pick an agent for each step (←/→ to change):\n\n")
			for i, step := range stepLabels {
				cursor := "  "
				st := stFg
				if i == m.agentIndex {
					cursor = stMag.Render("❯ ")
					st = stMag
				}
				s.WriteString(fmt.Sprintf("  %s%-12s %s\n", cursor, step+":", st.Render("◄ "+m.stepToAgent[step]+" ►")))
			}
			s.WriteString("\n" + stDim.Render("  c: done customizing"))
		} else {
			s.WriteString("  Autodetected agents:\n\n")
			for _, step := range stepLabels {
				s.WriteString(fmt.Sprintf("  %-12s %s\n", step+":", stGreen.Render(m.stepToAgent[step])))
			}
			s.WriteString("\n" + stDim.Render("  c: customize"))
		}
		s.WriteString("\n")
	case stateSteps:
		s.WriteString(stepHeader(5, "Loop shape"))
		labels := []string{"4 steps  discover → execute → verify → commit  (opens PR)", "3 steps  discover → execute → verify  (local commit, no PR agent)"}
		for i, label := range labels {
			s.WriteString(radio(i == m.stepsIndex, label))
		}
	case stateOptions:
		s.WriteString(stepHeader(6, "Options"))
		for i, name := range m.optionsList {
			s.WriteString(checkbox(i == m.optionsIndex, m.optionsValues[i], name))
		}
	case stateMaxIters:
		s.WriteString(stepHeader(7, "Iteration budget"))
		s.WriteString("  Max loop iterations per task:\n\n  " + m.textInput.View() + "\n")
	case stateConfirm:
		s.WriteString(stepHeader(8, "Review & launch"))
		s.WriteString(m.confirmView())
	}

	s.WriteString("\n" + stDim.Render("  enter: continue   esc: back   ^c: quit") + "\n")
	return tea.NewView(s.String())
}

func (m *WizardModel) confirmView() string {
	row := func(k, v string) string {
		return fmt.Sprintf("  %s %s\n", stDim.Render(fmt.Sprintf("%-11s", k)), stFg.Render(v))
	}
	var b strings.Builder
	b.WriteString(row("repo", m.cfg.Repo))
	b.WriteString(row("mode", stCyan.Render(m.cfg.Mode)))
	if m.cfg.Mode == "merge" {
		author := m.cfg.MergeAuthor
		if author == "" {
			author = "(everyone)"
		}
		b.WriteString(row("pr author", author))
		b.WriteString(row("method", m.cfg.MergeMethod))
		b.WriteString("\n  " + stGreen.Render("Press enter to merge ready PRs."))
		return b.String()
	}
	if m.cfg.Mission != "" {
		b.WriteString(row("mission", stAmber.Render(truncate(m.cfg.Mission, 56))))
	}
	if m.cfg.Issue != "" {
		b.WriteString(row("issue", m.cfg.Issue))
	}
	b.WriteString(row("steps", fmt.Sprintf("%d  (%s)", m.cfg.Steps, strings.Join(m.cfg.ActiveSteps(), " → "))))
	b.WriteString(row("agents", fmt.Sprintf("%s / %s / %s / %s", m.cfg.Discover.Agent, m.cfg.Execute.Agent, m.cfg.Verify.Agent, m.cfg.Commit.Agent)))
	b.WriteString(row("yolo", boolStr(m.cfg.Yolo)))
	b.WriteString(row("interactive", boolStr(m.cfg.Interactive)))
	b.WriteString(row("max iters", strconv.Itoa(m.cfg.MaxIterations)))
	b.WriteString("\n  " + stGreen.Render("Press enter to launch the loop."))
	return b.String()
}

func stepHeader(n int, title string) string {
	return stMag.Render(fmt.Sprintf("  ▸ Step %d  ", n)) + stBold.Render(title) + "\n\n"
}

func radio(selected bool, label string) string {
	if selected {
		return stMag.Render("  ❯ ● ") + stBold.Render(label) + "\n"
	}
	return stDim.Render("    ○ ") + stFg.Render(label) + "\n"
}

func checkbox(cursor, checked bool, label string) string {
	box := stDim.Render("[ ]")
	if checked {
		box = stGreen.Render("[✓]")
	}
	prefix := "    "
	st := stFg
	if cursor {
		prefix = stMag.Render("  ❯ ")
		st = stBold
	}
	return prefix + box + " " + st.Render(label) + "\n"
}

func boolStr(b bool) string {
	if b {
		return stGreen.Render("on")
	}
	return stDim.Render("off")
}

func RunWizardTUI(initialCfg *config.LoopConfig) (*config.LoopConfig, error) {
	m := NewWizardModel(initialCfg)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return nil, err
	}
	if !m.confirmed {
		return nil, nil
	}
	return m.cfg, nil
}

// ===========================================================================
// MONITOR
// ===========================================================================

type TUIUpdateMsg struct {
	Text      string
	IsThought bool
}
type TUIStatusMsg struct {
	Iteration int
	Step      string
	Agent     string
	State     string
	Branch    string
	Duration  time.Duration
}
type TUIStatsMsg struct {
	Descendants int
	Sockets     int
	CPUPercent  float64
}
type TUIPlanMsg struct{ PlanText string }
type monitorTickMsg struct{}

func monitorTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return monitorTickMsg{} })
}

type MonitorModel struct {
	cfg          *config.LoopConfig
	width        int
	height       int
	frame        int
	iteration    int
	currentStep  string
	currentAgent string
	state        string
	branch       string
	duration     time.Duration
	descendants  int
	sockets      int
	cpuPercent   float64
	cpuHist      []float64
	logViewport  viewport.Model
	logs         []string
	mu           sync.Mutex
	quitting     bool
	paused       bool
	skipStep     bool
	interject    string
	textInput    textinput.Model
}

func NewMonitorModel(cfg *config.LoopConfig) *MonitorModel {
	vp := viewport.New(viewport.WithWidth(96), viewport.WithHeight(14))
	vp.SetContent(stDim.Render("waiting for the loop to start…"))

	ti := textinput.New()
	ti.Placeholder = "queue a note for the NEXT step · /pause /resume /skip /quit"
	ti.Focus()
	ti.CharLimit = 400
	ti.SetWidth(90)

	return &MonitorModel{
		cfg:         cfg,
		width:       100,
		height:      32,
		state:       "starting",
		logViewport: vp,
		textInput:   ti,
	}
}

func (m *MonitorModel) Init() tea.Cmd { return tea.Batch(textinput.Blink, monitorTick()) }

func (m *MonitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case monitorTickMsg:
		m.frame++
		cmds = append(cmds, monitorTick())

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()

	case TUIUpdateMsg:
		styled := msg.Text
		if msg.IsThought {
			styled = stDim.Render(msg.Text)
		} else {
			styled = stFg.Render(msg.Text)
		}
		m.logs = append(m.logs, styled)
		if len(m.logs) > 2000 {
			m.logs = m.logs[len(m.logs)-2000:]
		}
		m.logViewport.SetContent(strings.Join(m.logs, ""))
		m.logViewport.GotoBottom()

	case TUIStatusMsg:
		m.iteration = msg.Iteration
		if msg.Step != "" {
			m.currentStep = msg.Step
		}
		if msg.Agent != "" {
			m.currentAgent = msg.Agent
		}
		m.state = msg.State
		m.branch = msg.Branch
		m.duration = msg.Duration

	case TUIStatsMsg:
		m.descendants = msg.Descendants
		m.sockets = msg.Sockets
		m.cpuPercent = msg.CPUPercent
		m.cpuHist = append(m.cpuHist, msg.CPUPercent)
		if len(m.cpuHist) > 24 {
			m.cpuHist = m.cpuHist[len(m.cpuHist)-24:]
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "pgup":
			m.logViewport.HalfPageUp()
			return m, nil
		case "pgdown":
			m.logViewport.HalfPageDown()
			return m, nil
		case "enter":
			m.handleInput()
			return m, nil
		}
	}

	var tiCmd tea.Cmd
	m.textInput, tiCmd = m.textInput.Update(msg)
	cmds = append(cmds, tiCmd)
	return m, tea.Batch(cmds...)
}

func (m *MonitorModel) handleInput() {
	val := strings.TrimSpace(m.textInput.Value())
	if val == "" {
		return
	}
	m.textInput.Reset()
	if strings.HasPrefix(val, "/") {
		switch val {
		case "/pause":
			m.paused = true
			m.pushLog(stAmber.Render("⏸ paused\n"))
		case "/resume", "/unpause":
			m.paused = false
			m.pushLog(stGreen.Render("▶ resumed\n"))
		case "/skip":
			m.skipStep = true
			m.pushLog(stAmber.Render("⏭ skipping current step\n"))
		case "/quit", "/abort":
			m.quitting = true
		default:
			m.pushLog(stRed.Render("unknown command: " + val + "\n"))
		}
		return
	}
	m.interject = val
	m.pushLog(stMag.Render("✎ note queued — added to the NEXT step's prompt (it can't change the step running now): ") + stFg.Render(val) + "\n")
}

func (m *MonitorModel) pushLog(s string) {
	m.logs = append(m.logs, s)
	m.logViewport.SetContent(strings.Join(m.logs, ""))
	m.logViewport.GotoBottom()
}

func (m *MonitorModel) resize() {
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	h := m.height - 18
	if h < 6 {
		h = 6
	}
	m.logViewport.SetWidth(w)
	m.logViewport.SetHeight(h)
	m.textInput.SetWidth(w - 4)
}

func (m *MonitorModel) View() tea.View {
	m.mu.Lock()
	defer m.mu.Unlock()

	inner := m.width - 2
	if inner < 40 {
		inner = 40
	}

	var b strings.Builder
	b.WriteString(m.titleRow(inner) + "\n\n")
	b.WriteString(m.pipelineRow() + "\n\n")
	b.WriteString(m.panelsRow() + "\n")
	b.WriteString(panelLabel.Render(" live agent output") + "\n")
	b.WriteString(logPanel.Render(m.logViewport.View()) + "\n")
	b.WriteString(inputBar.Render(m.textInput.View()) + "\n")
	b.WriteString(stDim.Render(" pgup/pgdn scroll · enter: queue note for next step · /pause /resume /skip · /quit aborts now · ^c quit"))
	return tea.NewView(b.String())
}

func (m *MonitorModel) titleRow(width int) string {
	left := titleBar.Render("middle-manager") + " " + stDim.Render(filepath.Base(m.cfg.Repo))
	state := strings.ToUpper(m.state)
	var stState lipgloss.Style
	switch m.state {
	case "paused":
		stState = stAmber
	case "completed":
		stState = stGreen
	case "failed":
		stState = stRed
	default:
		stState = stCyan
		state = spinFrame(m.frame) + " " + state
	}
	right := stState.Render(state)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *MonitorModel) pipelineRow() string {
	active := -1
	for i, s := range stepLabels {
		if s == m.currentStep {
			active = i
		}
	}
	pills := make([]string, 0, len(stepLabels))
	for i, step := range stepLabels {
		label := strings.ToUpper(step)
		switch {
		case i < active || m.state == "completed":
			pills = append(pills, lipgloss.NewStyle().Foreground(cGreen).Render("✓ "+label))
		case i == active:
			pill := lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cMagenta).Padding(0, 1).
				Render(spinFrame(m.frame) + " " + label)
			pills = append(pills, pill)
		default:
			pills = append(pills, stDim.Render("○ "+label))
		}
	}
	arrow := stDim.Render(" ❯ ")
	return " " + strings.Join(pills, arrow)
}

func (m *MonitorModel) panelsRow() string {
	repo := filepath.Base(m.cfg.Repo)
	dash := panelLabel.Render("dashboard") + "\n" +
		kv("repo", stFg.Render(repo)) +
		kv("branch", stViol.Render(orDash(m.branch))) +
		kv("iter", stBold.Render(strconv.Itoa(m.iteration))) +
		kv("step", stCyan.Render(strings.ToUpper(orDash(m.currentStep)))) +
		kv("agent", stGreen.Render(orDash(m.currentAgent))) +
		kv("elapsed", stFg.Render(m.duration.Round(time.Second).String()))

	res := panelLabel.Render("resources") + "\n" +
		kv("cpu", m.cpuBar()) +
		kv("procs", stFg.Render(strconv.Itoa(m.descendants))) +
		kv("sockets", stFg.Render(strconv.Itoa(m.sockets))) +
		kv("mode", stFg.Render(m.cfg.Mode))

	half := (m.width-6)/2
	if half < 22 {
		half = 22
	}
	left := panel.Width(half).Render(dash)
	right := panel.Width(half).Render(res)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func kv(k, v string) string {
	return stDim.Render(fmt.Sprintf("%-8s", k)) + v + "\n"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// cpuBar renders a tiny sparkline of recent CPU plus the current percentage.
func (m *MonitorModel) cpuBar() string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	var spark strings.Builder
	for _, v := range m.cpuHist {
		idx := int(v / 100.0 * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx > len(blocks)-1 {
			idx = len(blocks) - 1
		}
		spark.WriteRune(blocks[idx])
	}
	col := cGreen
	if m.cpuPercent > 75 {
		col = cRed
	} else if m.cpuPercent > 40 {
		col = cAmber
	}
	return lipgloss.NewStyle().Foreground(col).Render(spark.String()) +
		" " + stFg.Render(fmt.Sprintf("%.0f%%", m.cpuPercent))
}

// ---- global program plumbing ----

var (
	GlobalProgram *tea.Program
	GlobalModel   *MonitorModel
)

func StartMonitorTUI(cfg *config.LoopConfig) {
	GlobalModel = NewMonitorModel(cfg)
	GlobalProgram = tea.NewProgram(GlobalModel)
}

func NotifyTUIUpdate(text string, isThought bool) {
	if GlobalProgram != nil {
		GlobalProgram.Send(TUIUpdateMsg{Text: text, IsThought: isThought})
	}
}

func NotifyTUIStatus(iter int, step, agent, state, branch string, dur time.Duration) {
	if GlobalProgram != nil {
		GlobalProgram.Send(TUIStatusMsg{Iteration: iter, Step: step, Agent: agent, State: state, Branch: branch, Duration: dur})
	}
}

func NotifyTUIStats(descendants, sockets int, cpu float64) {
	if GlobalProgram != nil {
		GlobalProgram.Send(TUIStatsMsg{Descendants: descendants, Sockets: sockets, CPUPercent: cpu})
	}
}

func NotifyTUIPlan(planText string) {
	if GlobalProgram != nil {
		GlobalProgram.Send(TUIPlanMsg{PlanText: planText})
	}
}

func IsTUIPaused() bool {
	if GlobalModel == nil {
		return false
	}
	GlobalModel.mu.Lock()
	defer GlobalModel.mu.Unlock()
	return GlobalModel.paused
}

// RequestPause asks the monitor to pause before the next step (interactive mode).
func RequestPause() {
	if GlobalModel == nil {
		return
	}
	GlobalModel.mu.Lock()
	GlobalModel.paused = true
	GlobalModel.mu.Unlock()
}

func IsTUISkipStep() bool {
	if GlobalModel == nil {
		return false
	}
	GlobalModel.mu.Lock()
	defer GlobalModel.mu.Unlock()
	if GlobalModel.skipStep {
		GlobalModel.skipStep = false
		return true
	}
	return false
}

func IsTUIQuitting() bool {
	if GlobalModel == nil {
		return false
	}
	GlobalModel.mu.Lock()
	defer GlobalModel.mu.Unlock()
	return GlobalModel.quitting
}

func GetTUIInterjection() string {
	if GlobalModel == nil {
		return ""
	}
	GlobalModel.mu.Lock()
	defer GlobalModel.mu.Unlock()
	res := GlobalModel.interject
	GlobalModel.interject = ""
	return res
}

// PendingInterjection peeks at a queued note without consuming it, so the loop
// can announce "your note is being applied now" at the step boundary before
// GetTUIInterjection folds it into the prompt.
func PendingInterjection() string {
	if GlobalModel == nil {
		return ""
	}
	GlobalModel.mu.Lock()
	defer GlobalModel.mu.Unlock()
	return GlobalModel.interject
}
