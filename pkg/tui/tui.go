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
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/config"
)

// Styling definitions using Lipgloss
var (
	normalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EEEEEE"))
	titleStyle  = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00FFFF")).
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#008888")).
			Padding(0, 2)

	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00FF")).Bold(true)
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Bold(true)
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00")).Bold(true)
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true)
	greyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	boldStyle   = lipgloss.NewStyle().Bold(true)

	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#008888")).
			Padding(0, 1)

	logBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#FF00FF")).
			Padding(0, 1)
)

// ----------------------------------------------------
// WIZARD TUI MODEL & IMPLEMENTATION
// ----------------------------------------------------

type wizardState int

const (
	stateRepo wizardState = iota
	stateMode
	stateMission
	stateIssueDetails
	stateQueueFilters
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
	issueInput    string
	queueLabel    string
	queueAuthor   string
	queueLimit    string
	maxIters      string
	confirmed     bool
}

func NewWizardModel(initialCfg *config.LoopConfig) *WizardModel {
	ti := textinput.New()
	ti.Placeholder = "Repository Path"
	ti.Focus()
	ti.SetValue(initialCfg.Repo)
	ti.CharLimit = 250
	ti.SetWidth(60)

	cfg := initialCfg

	stepToAgent := map[string]string{
		"discover": cfg.Discover.Agent,
		"execute":  cfg.Execute.Agent,
		"verify":   cfg.Verify.Agent,
		"commit":   cfg.Commit.Agent,
	}

	return &WizardModel{
		state:     stateRepo,
		cfg:       cfg,
		textInput: ti,
		modes:     []string{"feature", "repair", "issue", "queue"},
		modeLabels: []string{
			"Build something new (e.g. \"add feature XYZ\") — recommended",
			"Discover & fix problems in the codebase",
			"Work a single GitHub issue",
			"Batch loop: drain a filtered queue of GitHub issues",
		},
		stepToAgent:   stepToAgent,
		stepsOptions:  []int{4, 3},
		optionsList:   []string{"YOLO Mode (Auto-approve permissions)", "Interactive Pause Between Steps", "Allow fixing unrelated test failures", "Fresh Mode (Reset loop state and start clean)"},
		optionsValues: []bool{cfg.Yolo, cfg.Interactive, cfg.FixUnrelatedTests, cfg.Fresh},
		maxIters:      strconv.Itoa(cfg.MaxIterations),
	}
}

func (m *WizardModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *WizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
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
			if m.state == stateMode {
				m.modeIndex--
				if m.modeIndex < 0 {
					m.modeIndex = len(m.modes) - 1
				}
			} else if m.state == stateSteps {
				m.stepsIndex--
				if m.stepsIndex < 0 {
					m.stepsIndex = len(m.stepsOptions) - 1
				}
			} else if m.state == stateOptions {
				m.optionsIndex--
				if m.optionsIndex < 0 {
					m.optionsIndex = len(m.optionsList) - 1
				}
			} else if m.state == stateAgents && m.customAgents {
				m.agentIndex--
				if m.agentIndex < 0 {
					m.agentIndex = 3
				}
			}
		case "down", "j":
			if m.state == stateMode {
				m.modeIndex++
				if m.modeIndex >= len(m.modes) {
					m.modeIndex = 0
				}
			} else if m.state == stateSteps {
				m.stepsIndex++
				if m.stepsIndex >= len(m.stepsOptions) {
					m.stepsIndex = 0
				}
			} else if m.state == stateOptions {
				m.optionsIndex++
				if m.optionsIndex >= len(m.optionsList) {
					m.optionsIndex = 0
				}
			} else if m.state == stateAgents && m.customAgents {
				m.agentIndex++
				if m.agentIndex > 3 {
					m.agentIndex = 0
				}
			}
		case "left", "h":
			if m.state == stateAgents && m.customAgents {
				m.cycleAgent(m.agentIndex, -1)
			}
		case "right", "l":
			if m.state == stateAgents && m.customAgents {
				m.cycleAgent(m.agentIndex, 1)
			}
		case "space":
			if m.state == stateOptions {
				m.optionsValues[m.optionsIndex] = !m.optionsValues[m.optionsIndex]
			}
		}
	}

	if m.state == stateRepo || m.state == stateMission || m.state == stateIssueDetails || m.state == stateQueueFilters || m.state == stateMaxIters {
		m.textInput, cmd = m.textInput.Update(msg)
	}

	return m, cmd
}

func (m *WizardModel) nextStep() (tea.Model, tea.Cmd) {
	m.err = nil

	switch m.state {
	case stateRepo:
		repoPath := strings.TrimSpace(m.textInput.Value())
		absPath, err := filepath.Abs(repoPath)
		if err != nil {
			m.err = fmt.Errorf("invalid path: %w", err)
			return m, nil
		}
		if _, err := os.Stat(absPath); err != nil {
			m.err = fmt.Errorf("path does not exist: %s", absPath)
			return m, nil
		}
		m.cfg.Repo = absPath
		m.state = stateMode

	case stateMode:
		m.cfg.Mode = m.modes[m.modeIndex]
		if m.cfg.Mode == "feature" {
			m.state = stateMission
			m.textInput.Reset()
			m.textInput.Placeholder = "Mission (e.g., add dark mode toggle)"
			m.textInput.Focus()
		} else if m.cfg.Mode == "issue" {
			m.state = stateIssueDetails
			m.textInput.Reset()
			m.textInput.Placeholder = "Issue number or URL"
			m.textInput.Focus()
		} else if m.cfg.Mode == "queue" {
			m.state = stateQueueFilters
			m.textInput.Reset()
			m.textInput.Placeholder = "Label filter (leave blank for none)"
			m.textInput.Focus()
		} else { // repair
			m.state = stateMission
			m.textInput.Reset()
			m.textInput.Placeholder = "Mission (optional guidance)"
			m.textInput.Focus()
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
			m.textInput.Reset()
			m.textInput.Placeholder = "Author filter (leave blank for none)"
		} else if m.queueAuthor == "" && m.queueLimit == "" {
			m.queueAuthor = val
			m.textInput.Reset()
			m.textInput.Placeholder = "Max issues limit (default 20)"
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

	case stateAgents:
		// Map autodetected agents
		m.cfg.Discover.Agent = m.stepToAgent["discover"]
		m.cfg.Execute.Agent = m.stepToAgent["execute"]
		m.cfg.Verify.Agent = m.stepToAgent["verify"]
		m.cfg.Commit.Agent = m.stepToAgent["commit"]

		m.state = stateSteps

	case stateSteps:
		m.cfg.Steps = m.stepsOptions[m.stepsIndex]
		if m.cfg.Steps == 3 {
			m.cfg.Commit.Enabled = false
		} else {
			m.cfg.Commit.Enabled = true
		}
		m.state = stateOptions

	case stateOptions:
		m.cfg.Yolo = m.optionsValues[0]
		m.cfg.Interactive = m.optionsValues[1]
		m.cfg.FixUnrelatedTests = m.optionsValues[2]
		m.cfg.Fresh = m.optionsValues[3]

		m.state = stateMaxIters
		m.textInput.Reset()
		m.textInput.Placeholder = "Max iterations per task (default 10)"
		m.textInput.SetValue("10")
		m.textInput.Focus()

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

func (m *WizardModel) prevStep() (tea.Model, tea.Cmd) {
	m.err = nil
	switch m.state {
	case stateMode:
		m.state = stateRepo
		m.textInput.SetValue(m.cfg.Repo)
		m.textInput.Focus()
	case stateMission:
		m.state = stateMode
	case stateIssueDetails:
		m.state = stateMode
	case stateQueueFilters:
		m.state = stateMode
	case stateAgents:
		if m.cfg.Mode == "feature" || m.cfg.Mode == "repair" {
			m.state = stateMission
			m.textInput.SetValue(m.cfg.Mission)
		} else if m.cfg.Mode == "issue" {
			m.state = stateIssueDetails
			m.textInput.SetValue(m.cfg.Issue)
		} else {
			m.state = stateQueueFilters
			m.textInput.SetValue(m.queueLabel)
			m.queueLabel = ""
			m.queueAuthor = ""
			m.queueLimit = ""
		}
		m.textInput.Focus()
	case stateSteps:
		m.state = stateAgents
	case stateOptions:
		m.state = stateSteps
	case stateMaxIters:
		m.state = stateOptions
	case stateConfirm:
		m.state = stateMaxIters
		m.textInput.SetValue(strconv.Itoa(m.cfg.MaxIterations))
		m.textInput.Focus()
	}
	return m, nil
}

func (m *WizardModel) cycleAgent(stepIdx int, dir int) {
	steps := []string{"discover", "execute", "verify", "commit"}
	step := steps[stepIdx]
	current := m.stepToAgent[step]
	if current == "" {
		current = "grok"
	}
	
	// Find index in AgentNames
	idx := -1
	for i, name := range agents.AgentNames {
		if name == current {
			idx = i
			break
		}
	}
	if idx == -1 {
		idx = 0
	}
	
	idx = (idx + dir + len(agents.AgentNames)) % len(agents.AgentNames)
	m.stepToAgent[step] = agents.AgentNames[idx]
}

func (m *WizardModel) View() tea.View {
	if m.quitting {
		return tea.NewView("Wizard aborted.\n")
	}

	s := titleStyle.Render(" middle-manager TUI Config Wizard ") + "\n\n"

	if m.err != nil {
		s += redStyle.Render(fmt.Sprintf(" ✗ %v", m.err)) + "\n\n"
	}

	switch m.state {
	case stateRepo:
		s += boldStyle.Render("Step 1: Specify repository directory") + "\n"
		s += "Where is the codebase we are working on?\n\n"
		s += m.textInput.View() + "\n\n"
		s += greyStyle.Render("Press Enter to continue, Esc to go back")

	case stateMode:
		s += boldStyle.Render("Step 2: Choose run mode") + "\n"
		s += "What do you want to accomplish?\n\n"
		for i, mode := range m.modes {
			cursor := " "
			style := normalStyle
			if i == m.modeIndex {
				cursor = ">"
				style = accentStyle
			}
			s += style.Render(fmt.Sprintf(" %s [%s] %s", cursor, string(mode[0]), m.modeLabels[i])) + "\n"
		}
		s += "\n" + greyStyle.Render("Use Up/Down or j/k to select, Enter to confirm, Esc to go back")

	case stateMission:
		s += boldStyle.Render("Step 3: Define mission prompt") + "\n"
		s += "What should the agents do? (e.g. \"add feature X\", \"fix test failure Y\")\n\n"
		s += m.textInput.View() + "\n\n"
		s += greyStyle.Render("Press Enter to continue, Esc to go back")

	case stateIssueDetails:
		s += boldStyle.Render("Step 3: Specify GitHub Issue") + "\n"
		s += "Enter the issue number (e.g. 42) or URL:\n\n"
		s += m.textInput.View() + "\n\n"
		s += greyStyle.Render("Press Enter to continue, Esc to go back")

	case stateQueueFilters:
		s += boldStyle.Render("Step 3: Configure Issue Queue Filters") + "\n"
		if m.queueLabel == "" {
			s += "Filter issues by label (optional):\n\n"
		} else if m.queueAuthor == "" {
			s += "Filter issues by author login (optional):\n\n"
		} else {
			s += "Set maximum issues limit (default 20):\n\n"
		}
		s += m.textInput.View() + "\n\n"
		s += greyStyle.Render("Press Enter to continue, Esc to go back")

	case stateAgents:
		s += boldStyle.Render("Step 4: Configure Steps & Agents") + "\n"
		if m.customAgents {
			s += "Customize the agent assigned to each loop step:\n\n"
			steps := []string{"discover", "execute", "verify", "commit"}
			for i, step := range steps {
				cursor := " "
				style := normalStyle
				if i == m.agentIndex {
					cursor = ">"
					style = accentStyle
				}
				s += style.Render(fmt.Sprintf(" %s %-12s < %s >", cursor, step+":", m.stepToAgent[step])) + "\n"
			}
			s += "\n"
			s += greyStyle.Render("Use Up/Down to navigate, Left/Right to change agent, [c] to finish, Enter to confirm, Esc to go back")
		} else {
			s += "The following agents were autodetected and assigned to steps:\n\n"
			s += fmt.Sprintf("  %-12s %s\n", "discover:", greenStyle.Render(m.stepToAgent["discover"]))
			s += fmt.Sprintf("  %-12s %s\n", "execute:", greenStyle.Render(m.stepToAgent["execute"]))
			s += fmt.Sprintf("  %-12s %s\n", "verify:", greenStyle.Render(m.stepToAgent["verify"]))
			s += fmt.Sprintf("  %-12s %s\n", "commit:", greenStyle.Render(m.stepToAgent["commit"]))
			s += "\n"
			s += greyStyle.Render("Press [c] to customize agents, Enter to accept, Esc to go back")
		}

	case stateSteps:
		s += boldStyle.Render("Step 5: Select loop steps count") + "\n"
		s += "Choose the number of agent steps in the loop:\n\n"
		options := []string{
			"4-step loop (discover → execute → verify → commit) - recommended",
			"3-step loop (discover → execute → verify) - skip git commit agent",
		}
		for i, label := range options {
			cursor := " "
			style := normalStyle
			if i == m.stepsIndex {
				cursor = ">"
				style = accentStyle
			}
			s += style.Render(fmt.Sprintf(" %s %s", cursor, label)) + "\n"
		}
		s += "\n" + greyStyle.Render("Use Up/Down or j/k to select, Enter to confirm, Esc to go back")

	case stateOptions:
		s += boldStyle.Render("Step 6: Configure loop options") + "\n"
		s += "Toggle loop behavior flags:\n\n"
		for i, name := range m.optionsList {
			cursor := " "
			box := "[ ]"
			style := normalStyle
			if i == m.optionsIndex {
				cursor = ">"
				style = accentStyle
			}
			if m.optionsValues[i] {
				box = greenStyle.Render("[x]")
			}
			s += style.Render(fmt.Sprintf(" %s %s %s", cursor, box, name)) + "\n"
		}
		s += "\n" + greyStyle.Render("Use Up/Down or j/k to navigate, Space to toggle, Enter to confirm, Esc to go back")

	case stateMaxIters:
		s += boldStyle.Render("Step 7: Specify iteration budget") + "\n"
		s += "What is the maximum number of loop iterations allowed per task?\n\n"
		s += m.textInput.View() + "\n\n"
		s += greyStyle.Render("Press Enter to continue, Esc to go back")

	case stateConfirm:
		s += boldStyle.Render("Step 8: Review & Confirm") + "\n"
		s += "Ready to launch the multi-agent loop with the following settings:\n\n"
		s += fmt.Sprintf("  Repository: %s\n", greenStyle.Render(m.cfg.Repo))
		s += fmt.Sprintf("  Run Mode:   %s\n", greenStyle.Render(m.cfg.Mode))
		if m.cfg.Mission != "" {
			s += fmt.Sprintf("  Mission:    %s\n", yellowStyle.Render(m.cfg.Mission))
		}
		s += fmt.Sprintf("  Steps Count:%d (%s)\n", m.cfg.Steps, strings.Join(m.cfg.ActiveSteps(), ", "))
		s += fmt.Sprintf("  YOLO Mode:  %t\n", m.cfg.Yolo)
		s += fmt.Sprintf("  Interactive:%t\n", m.cfg.Interactive)
		s += fmt.Sprintf("  Fresh Mode: %t\n", m.cfg.Fresh)
		s += fmt.Sprintf("  Max Iters:  %d\n", m.cfg.MaxIterations)
		s += "\n"
		s += greenStyle.Render(" Press Enter to START middle-manager loop, Esc to go back")
	}

	s += "\n"
	return tea.NewView(s)
}

func RunWizardTUI(initialCfg *config.LoopConfig) (*config.LoopConfig, error) {
	m := NewWizardModel(initialCfg)
	p := tea.NewProgram(m)
	_, err := p.Run()
	if err != nil {
		return nil, err
	}
	if !m.confirmed {
		return nil, nil // Aborted
	}
	return m.cfg, nil
}

// ----------------------------------------------------
// MONITOR TUI MODEL & IMPLEMENTATION
// ----------------------------------------------------

type TUIUpdateMsg struct {
	Text      string
	IsThought bool
}

type TUIStatusMsg struct {
	Iteration int
	Step      string
	Agent     string
	State     string // "running", "paused", "waiting", "completed", "failed"
	Branch    string
	Duration  time.Duration
}

type TUIStatsMsg struct {
	Descendants int
	Sockets     int
	CPUPercent  float64
}

type TUIPlanMsg struct {
	PlanText string
}

type MonitorModel struct {
	cfg           *config.LoopConfig
	iteration     int
	currentStep   string
	currentAgent  string
	state         string // "running", "paused", "waiting", "completed", "failed"
	branch        string
	duration      time.Duration
	descendants   int
	sockets       int
	cpuPercent    float64
	planTasks     []string
	planChecked   []bool
	logViewport   viewport.Model
	logs          []string
	mu            sync.Mutex
	quitting      bool
	pauseChan     chan struct{}
	paused        bool
	skipStep      bool
	interjectText string
	promptInter   bool
	textInput     textinput.Model
}

func NewMonitorModel(cfg *config.LoopConfig) *MonitorModel {
	vp := viewport.New(viewport.WithWidth(100), viewport.WithHeight(15))
	vp.Style = logBorder
	vp.SetContent("Waiting for loop to start...")

	ti := textinput.New()
	ti.Placeholder = "Type instruction to interject, or /pause, /skip, /quit (Press Enter to send)"
	ti.Focus()
	ti.CharLimit = 250
	ti.SetWidth(100)

	return &MonitorModel{
		cfg:         cfg,
		state:       "waiting",
		logViewport: vp,
		logs:        []string{},
		pauseChan:   make(chan struct{}),
		textInput:   ti,
	}
}

func (m *MonitorModel) Init() tea.Cmd {
	return nil
}

func (m *MonitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case TUIUpdateMsg:
		styledText := msg.Text
		if msg.IsThought {
			styledText = greyStyle.Render(msg.Text)
		} else {
			styledText = greenStyle.Render(msg.Text)
		}
		m.logs = append(m.logs, styledText)
		// Truncate logs if extremely large
		if len(m.logs) > 1000 {
			m.logs = m.logs[len(m.logs)-1000:]
		}
		m.logViewport.SetContent(strings.Join(m.logs, ""))
		m.logViewport.GotoBottom()

	case TUIStatusMsg:
		m.iteration = msg.Iteration
		m.currentStep = msg.Step
		m.currentAgent = msg.Agent
		m.state = msg.State
		m.branch = msg.Branch
		m.duration = msg.Duration

	case TUIStatsMsg:
		m.descendants = msg.Descendants
		m.sockets = msg.Sockets
		m.cpuPercent = msg.CPUPercent

	case TUIPlanMsg:
		m.planTasks = nil
		m.planChecked = nil
		lines := strings.Split(msg.PlanText, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- [ ]") {
				m.planTasks = append(m.planTasks, strings.TrimPrefix(line, "- [ ]"))
				m.planChecked = append(m.planChecked, false)
			} else if strings.HasPrefix(line, "- [x]") {
				m.planTasks = append(m.planTasks, strings.TrimPrefix(line, "- [x]"))
				m.planChecked = append(m.planChecked, true)
			}
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "pageup":
			m.logViewport.HalfPageUp()
			return m, nil
		case "pagedown":
			m.logViewport.HalfPageDown()
			return m, nil
		case "enter":
			val := strings.TrimSpace(m.textInput.Value())
			if val != "" {
				m.textInput.Reset()
				if strings.HasPrefix(val, "/") {
					// Handle slash commands
					switch val {
					case "/pause":
						m.paused = !m.paused
						if m.paused {
							m.state = "paused"
							m.logs = append(m.logs, yellowStyle.Render("\n[TUI] Paused by user command.\n"))
						} else {
							m.state = "running"
							m.logs = append(m.logs, greenStyle.Render("\n[TUI] Resumed by user command.\n"))
						}
					case "/skip":
						m.skipStep = true
						m.logs = append(m.logs, yellowStyle.Render("\n[TUI] Skipping current step by user command.\n"))
					case "/quit":
						m.quitting = true
						return m, tea.Quit
					default:
						m.logs = append(m.logs, redStyle.Render(fmt.Sprintf("\n[TUI] Unknown command: %s\n", val)))
					}
				} else {
					// Interject instruction
					m.interjectText = val
					m.logs = append(m.logs, greenStyle.Render(fmt.Sprintf("\n[TUI Interjection] %s\n", val)))
				}
				m.logViewport.SetContent(strings.Join(m.logs, ""))
				m.logViewport.GotoBottom()
			}
			return m, nil
		}
	}

	var tiCmd tea.Cmd
	m.textInput, tiCmd = m.textInput.Update(msg)
	cmds = append(cmds, tiCmd)

	return m, tea.Batch(cmds...)
}

func (m *MonitorModel) View() tea.View {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := titleStyle.Render(" MIDDLE-MANAGER TUI Loop Monitor ") + "\n"

	// Dashboard Panel
	stateStr := strings.ToUpper(m.state)
	stateColor := greenStyle
	switch m.state {
	case "paused":
		stateColor = yellowStyle
	case "completed":
		stateColor = greenStyle
	case "failed":
		stateColor = redStyle
	}

	dashboard := fmt.Sprintf(
		"Repo:     %s\nBranch:   %s\nIteration:%d\nStep:     %s (%s)\nState:    %s\nElapsed:  %s",
		greenStyle.Render(m.cfg.Repo),
		accentStyle.Render(m.branch),
		m.iteration,
		boldStyle.Render(strings.ToUpper(m.currentStep)),
		greenStyle.Render(m.currentAgent),
		stateColor.Render(stateStr),
		m.duration.Round(time.Second).String(),
	)

	// Resource Statistics
	stats := fmt.Sprintf(
		"CPU Usage:  %.1f%%\nSockets:    %d\nDescendants:%d",
		m.cpuPercent,
		m.sockets,
		m.descendants,
	)

	// Side-by-side Layout
	dashPanel := panelBorder.Render(boldStyle.Render(" DASHBOARD \n\n") + dashboard)
	statsPanel := panelBorder.Render(boldStyle.Render(" RESOURCES \n\n") + stats)
	s += lipgloss.JoinHorizontal(lipgloss.Top, dashPanel, statsPanel) + "\n\n"

	// Plan / Task list
	s += boldStyle.Render("Current Plan Tasks:") + "\n"
	if len(m.planTasks) == 0 {
		s += greyStyle.Render("  (no active plan tasks)") + "\n"
	} else {
		for i, task := range m.planTasks {
			box := "[ ]"
			style := yellowStyle
			if m.planChecked[i] {
				box = "[x]"
				style = greenStyle
			}
			s += style.Render(fmt.Sprintf("  %s %s", box, task)) + "\n"
		}
	}
	s += "\n"

	// Logs / Viewport
	s += boldStyle.Render("Agent Real-time Console output:") + "\n"
	s += m.logViewport.View() + "\n\n"

	// Text input for interjection / slash commands
	s += boldStyle.Render("Interject / Command Input:") + "\n"
	s += m.textInput.View() + "\n\n"

	// Keyboard controls legend
	s += greyStyle.Render("Commands: /pause | /skip | /quit | Or type instructions to interject directly (PageUp/PageDown to scroll)") + "\n"

	return tea.NewView(s)
}

// Global TUI program reference to push updates asynchronously
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
		GlobalProgram.Send(TUIStatusMsg{
			Iteration: iter,
			Step:      step,
			Agent:     agent,
			State:     state,
			Branch:    branch,
			Duration:  dur,
		})
	}
}

func NotifyTUIStats(descendants, sockets int, cpu float64) {
	if GlobalProgram != nil {
		GlobalProgram.Send(TUIStatsMsg{
			Descendants: descendants,
			Sockets:     sockets,
			CPUPercent:  cpu,
		})
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

func IsTUISkipStep() bool {
	if GlobalModel == nil {
		return false
	}
	GlobalModel.mu.Lock()
	defer GlobalModel.mu.Unlock()
	if GlobalModel.skipStep {
		GlobalModel.skipStep = false // Reset
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
	res := GlobalModel.interjectText
	GlobalModel.interjectText = ""
	return res
}


