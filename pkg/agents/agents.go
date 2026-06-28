package agents

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
)

var AgentNames = []string{"grok", "claude", "codex", "opencode"}

type AgentSpec struct {
	Name         string
	Binary       string
	YoloFlag     string
	YoloPosition string // "before_prompt" | "after_binary" | "extra" | "before_subcommand"
	PromptMode   string // "arg" | "stdin" | "print_flag"
	PrintFlag    string
	ModelFlag    string
	CwdFlag      string
	Subcommand   []string
	ExtraYolo    []string
	Notes        string
}

var AgentSpecs = map[string]AgentSpec{
	"grok": {
		Name:         "grok",
		Binary:       "grok",
		YoloFlag:     "--yolo",
		YoloPosition: "before_prompt",
		PromptMode:   "arg",
		PrintFlag:    "-p",
		ModelFlag:    "-m",
		CwdFlag:      "--cwd",
		ExtraYolo:    []string{"--always-approve"},
		Notes:        "Headless: grok -p PROMPT --yolo --cwd DIR. Alias: --always-approve",
	},
	"claude": {
		Name:         "claude",
		Binary:       "claude",
		YoloFlag:     "--dangerously-skip-permissions",
		YoloPosition: "before_prompt",
		PromptMode:   "arg",
		PrintFlag:    "-p",
		ModelFlag:    "--model",
		CwdFlag:      "",
		Notes:        "Run from target repo cwd. Also: --permission-mode bypassPermissions",
	},
	"codex": {
		Name:         "codex",
		Binary:       "codex",
		YoloFlag:     "--yolo",
		YoloPosition: "before_prompt",
		Subcommand:   []string{"exec"},
		PromptMode:   "arg",
		ModelFlag:    "-m",
		CwdFlag:      "",
		Notes:        "OpenAI Codex CLI: codex exec PROMPT --yolo. Also: --full-auto",
	},
	"opencode": {
		Name:         "opencode",
		Binary:       "opencode",
		YoloFlag:     "--dangerously-skip-permissions",
		YoloPosition: "before_prompt",
		Subcommand:   []string{"run"},
		PromptMode:   "arg",
		ModelFlag:    "-m",
		CwdFlag:      "--dir",
		Notes:        "opencode run PROMPT --dangerously-skip-permissions --dir DIR",
	},
}

type AgentRun struct {
	Agent       string
	Command     []string
	Prompt      string
	Cwd         string
	Model       string
	Yolo        bool
	ExtraArgs   []string
	Env         []string
	Interactive bool
	Tmux        bool
}

func ResolveBinary(name string, override string) string {
	if override != "" {
		if _, err := exec.LookPath(override); err == nil {
			return override
		}
		if _, err := os.Stat(override); err == nil {
			return override
		}
		return ""
	}
	spec, ok := AgentSpecs[name]
	if !ok {
		return ""
	}
	path, err := exec.LookPath(spec.Binary)
	if err == nil {
		return path
	}
	return ""
}

func AgentAvailable(name string, binaryOverride string) bool {
	return ResolveBinary(name, binaryOverride) != ""
}

func BuildCommand(
	agent string,
	prompt string,
	cwd string,
	model string,
	yolo bool,
	extraArgs []string,
	binaryOverride string,
	promptFile string,
	interactive bool,
	tmux bool,
) (*AgentRun, error) {
	spec, ok := AgentSpecs[agent]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", agent)
	}

	binary := binaryOverride
	if binary == "" {
		binary = spec.Binary
	}

	cmd := []string{binary}
	extras := append([]string{}, extraArgs...)

	if yolo && spec.YoloPosition == "before_subcommand" {
		if spec.YoloFlag != "" {
			cmd = append(cmd, spec.YoloFlag)
		}
	}

	cmd = append(cmd, spec.Subcommand...)

	if yolo && (spec.YoloPosition == "after_binary" || spec.YoloPosition == "before_prompt") {
		if spec.YoloFlag != "" {
			cmd = append(cmd, spec.YoloFlag)
		}
	}

	if yolo && spec.YoloPosition == "extra" {
		cmd = append(cmd, spec.ExtraYolo...)
	}

	if interactive && (agent == "grok" || agent == "claude" || agent == "opencode") {
		if spec.PromptMode == "arg" {
			cmd = append(cmd, prompt)
		}
		cmd = append(cmd, extras...)
		if model != "" && spec.ModelFlag != "" {
			cmd = append(cmd, spec.ModelFlag, model)
		}
		if spec.CwdFlag != "" {
			cmd = append(cmd, spec.CwdFlag, cwd)
		}
		return &AgentRun{
			Agent:       agent,
			Command:     cmd,
			Prompt:      prompt,
			Cwd:         cwd,
			Model:       model,
			Yolo:        yolo,
			ExtraArgs:   extras,
			Env:         os.Environ(),
			Interactive: true,
			Tmux:        tmux,
		}, nil
	}

	usePromptFile := promptFile != "" // Python did check: prompt_file and spec.prompt_file_flag and spec.prompt_mode != "print_flag"
	// Wait, does Python specify a prompt_file_flag for grok?
	// Yes! Grok spec: prompt_file_flag = "--prompt-file"
	promptFileFlag := ""
	if agent == "grok" {
		promptFileFlag = "--prompt-file"
	}

	if usePromptFile && promptFileFlag != "" {
		cmd = append(cmd, promptFileFlag, promptFile)
	} else if spec.PromptMode == "print_flag" && spec.PrintFlag != "" {
		cmd = append(cmd, spec.PrintFlag, prompt)
	} else if spec.PromptMode == "arg" {
		if spec.PrintFlag != "" {
			cmd = append(cmd, spec.PrintFlag)
		}
		cmd = append(cmd, prompt)
	}

	if model != "" && spec.ModelFlag != "" {
		cmd = append(cmd, spec.ModelFlag, model)
	}

	if spec.CwdFlag != "" {
		cmd = append(cmd, spec.CwdFlag, cwd)
	}

	if yolo && spec.YoloPosition != "before_subcommand" && spec.YoloPosition != "after_binary" && spec.YoloPosition != "before_prompt" && spec.YoloPosition != "extra" {
		if spec.YoloFlag != "" {
			cmd = append(cmd, spec.YoloFlag)
		}
	}

	// opencode: yolo often works as trailing flag too
	if yolo && agent == "opencode" && spec.YoloFlag != "" {
		found := false
		for _, arg := range cmd {
			if arg == spec.YoloFlag {
				found = true
				break
			}
		}
		if !found {
			cmd = append(cmd, spec.YoloFlag)
		}
	}

	cmd = append(cmd, extras...)

	if agent == "claude" && yolo && spec.YoloFlag != "" {
		found := false
		for _, arg := range cmd {
			if arg == spec.YoloFlag {
				found = true
				break
			}
		}
		if !found {
			cmd = append(cmd, spec.YoloFlag)
		}
	}

	return &AgentRun{
		Agent:       agent,
		Command:     cmd,
		Prompt:      prompt,
		Cwd:         cwd,
		Model:       model,
		Yolo:        yolo,
		ExtraArgs:   extras,
		Env:         os.Environ(),
		Interactive: false,
		Tmux:        tmux,
	}, nil
}

var (
	ActiveTmuxPIDs   []int
	ActiveTmuxPIDsMu sync.Mutex
)

func addActiveTmuxPID(pid int) {
	ActiveTmuxPIDsMu.Lock()
	defer ActiveTmuxPIDsMu.Unlock()
	for _, p := range ActiveTmuxPIDs {
		if p == pid {
			return
		}
	}
	ActiveTmuxPIDs = append(ActiveTmuxPIDs, pid)
}

func removeActiveTmuxPID(pid int) {
	ActiveTmuxPIDsMu.Lock()
	defer ActiveTmuxPIDsMu.Unlock()
	for i, p := range ActiveTmuxPIDs {
		if p == pid {
			ActiveTmuxPIDs = append(ActiveTmuxPIDs[:i], ActiveTmuxPIDs[i+1:]...)
			return
		}
	}
}

// GetProcessTreeCPUTicks retrieves cpu ticks for pid and all its descendants on Linux.
func GetProcessTreeCPUTicks(parentPid int) (float64, error) {
	pids, err := listPids()
	if err != nil {
		return 0, err
	}

	ppidMap := make(map[int]int)
	pidStats := make(map[int]float64)

	for _, pid := range pids {
		statPath := fmt.Sprintf("/proc/%d/stat", pid)
		b, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		content := string(b)
		rparIdx := strings.LastIndex(content, ")")
		if rparIdx == -1 {
			continue
		}
		fields := strings.Fields(content[rparIdx+2:])
		if len(fields) < 13 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		utime, _ := strconv.ParseFloat(fields[11], 64)
		stime, _ := strconv.ParseFloat(fields[12], 64)
		ppidMap[pid] = ppid
		pidStats[pid] = utime + stime
	}

	descendants := make(map[int]bool)
	descendants[parentPid] = true

	ActiveTmuxPIDsMu.Lock()
	for _, pid := range ActiveTmuxPIDs {
		descendants[pid] = true
	}
	ActiveTmuxPIDsMu.Unlock()
	changed := true
	for changed {
		changed = false
		for pid, ppid := range ppidMap {
			if descendants[ppid] && !descendants[pid] {
				descendants[pid] = true
				changed = true
			}
		}
	}

	var total float64
	for pid := range descendants {
		total += pidStats[pid]
	}
	return total, nil
}

// GetProcessTreeStats returns (descendantCount, socketCount) for pid and descendants.
func GetProcessTreeStats(parentPid int) (int, int) {
	pids, err := listPids()
	if err != nil {
		return 1, 0
	}

	ppidMap := make(map[int]int)
	for _, pid := range pids {
		statPath := fmt.Sprintf("/proc/%d/stat", pid)
		b, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		content := string(b)
		rparIdx := strings.LastIndex(content, ")")
		if rparIdx == -1 {
			continue
		}
		fields := strings.Fields(content[rparIdx+2:])
		if len(fields) < 2 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		ppidMap[pid] = ppid
	}

	descendants := make(map[int]bool)
	descendants[parentPid] = true

	ActiveTmuxPIDsMu.Lock()
	for _, pid := range ActiveTmuxPIDs {
		descendants[pid] = true
	}
	ActiveTmuxPIDsMu.Unlock()

	changed := true
	for changed {
		changed = false
		for pid, ppid := range ppidMap {
			if descendants[ppid] && !descendants[pid] {
				descendants[pid] = true
				changed = true
			}
		}
	}

	totalSockets := 0
	for pid := range descendants {
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if err == nil && strings.HasPrefix(link, "socket:[") {
				totalSockets++
			}
		}
	}

	return len(descendants), totalSockets
}

func listPids() ([]int, error) {
	procDir, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	defer procDir.Close()
	names, err := procDir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, name := range names {
		if pid, err := strconv.Atoi(name); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func CalculateCPUPercent(pid int, lastTicks *float64, lastTime time.Time) (float64, float64, time.Time) {
	currentTime := time.Now()
	dt := currentTime.Sub(lastTime).Seconds()
	if dt <= 0 {
		if lastTicks != nil {
			return 0, *lastTicks, lastTime
		}
		return 0, 0, lastTime
	}

	currentTicks, err := GetProcessTreeCPUTicks(pid)
	if err != nil || lastTicks == nil {
		return 0, currentTicks, currentTime
	}

	dTicks := currentTicks - *lastTicks
	clkTck := 100.0 // Default fallback
	// We can check SC_CLK_TCK on Unix/Linux, but standard is 100
	cpuPercent := (dTicks / (clkTck * dt)) * 100.0
	return cpuPercent, currentTicks, currentTime
}

func StripAnsi(text string) string {
	re := regexp.MustCompile(`\x1B(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~]|[@-Z\\-_])`)
	return re.ReplaceAllString(text, "")
}

func FormatCmdForDisplay(command []string) string {
	quoted := make([]string, 0, len(command))
	for _, arg := range command {
		if strings.Contains(arg, "\n") || len(arg) > 150 {
			lines := strings.Split(arg, "\n")
			first := lines[0]
			if len(first) > 120 {
				first = first[:120] + "..."
			}
			quoted = append(quoted, fmt.Sprintf("%q... [truncated prompt]", first))
		} else {
			if strings.Contains(arg, " ") || strings.Contains(arg, "\"") || strings.Contains(arg, "'") {
				quoted = append(quoted, fmt.Sprintf("%q", arg))
			} else {
				quoted = append(quoted, arg)
			}
		}
	}
	return strings.Join(quoted, " ")
}

func GetACPCommand(agent string, binaryOverride string) []string {
	binary := binaryOverride
	if agent == "grok" {
		binName := binary
		if binName == "" {
			binName = "grok"
		}
		_, err := exec.LookPath(binName)
		if err != nil {
			if _, err2 := exec.LookPath("agent"); err2 == nil {
				binName = "agent"
			}
		}
		return []string{binName, "agent", "stdio"}
	} else if agent == "opencode" {
		binName := binary
		if binName == "" {
			binName = "opencode"
		}
		return []string{binName, "acp"}
	} else if agent == "claude" {
		binName := binary
		if binName == "" {
			binName = "claude"
		}
		if _, err := exec.LookPath(binName); err == nil {
			return []string{binName, "agent", "stdio"}
		}
		return []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp"}
	} else if agent == "codex" {
		binName := binary
		if binName == "" {
			binName = "codex"
		}
		return []string{binName, "app-server"}
	}
	binName := binary
	if binName == "" {
		binName = agent
	}
	return []string{binName}
}

// middleManagerClient implements acp.Client to run agents headlessly and track progress.
type middleManagerClient struct {
	cwd       string
	onUpdate  func(text string, isThought bool)
	terminals map[string]*terminalInfo
	mu        sync.Mutex
}

type terminalInfo struct {
	cmd      *exec.Cmd
	output   *bytes.Buffer
	mu       sync.Mutex
	exitCode int
	done     chan struct{}
}

func (c *middleManagerClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cwd, path)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return acp.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", path, err)
	}
	content := string(b)

	// Apply line and limit if specified
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = *params.Line - 1
			if start > len(lines) {
				start = len(lines)
			}
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 {
			end = start + *params.Limit
			if end > len(lines) {
				end = len(lines)
			}
		}
		content = strings.Join(lines[start:end], "\n")
	}

	return acp.ReadTextFileResponse{Content: content}, nil
}

func (c *middleManagerClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cwd, path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return acp.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	if err := os.WriteFile(path, []byte(params.Content), 0644); err != nil {
		return acp.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", path, err)
	}
	return acp.WriteTextFileResponse{}, nil
}

func (c *middleManagerClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Auto-approve all permissions for YOLO execution
	var optionId acp.PermissionOptionId
	if len(params.Options) > 0 {
		optionId = params.Options[0].OptionId
	}
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{
				OptionId: optionId,
			},
		},
	}, nil
}

func (c *middleManagerClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	if u.AgentThoughtChunk != nil {
		text := ""
		if u.AgentThoughtChunk.Content.Text != nil {
			text = u.AgentThoughtChunk.Content.Text.Text
		}
		if text != "" && c.onUpdate != nil {
			c.onUpdate(text, true)
		}
	} else if u.AgentMessageChunk != nil {
		content := u.AgentMessageChunk.Content
		text := ""
		if content.Text != nil {
			text = content.Text.Text
		}
		if text != "" && c.onUpdate != nil {
			c.onUpdate(text, false)
		}
	}
	return nil
}

func (c *middleManagerClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	termId := fmt.Sprintf("term_%d", len(c.terminals)+1)

	cmdCwd := c.cwd
	if params.Cwd != nil && *params.Cwd != "" {
		cmdCwd = *params.Cwd
	}

	// Shell invocation for command
	var cmd *exec.Cmd
	if len(params.Args) > 0 {
		cmd = exec.Command(params.Command, params.Args...)
	} else {
		cmd = exec.Command("sh", "-c", params.Command)
	}
	cmd.Dir = cmdCwd

	// Build environment variables
	if len(params.Env) > 0 {
		cmd.Env = os.Environ()
		for _, e := range params.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", e.Name, e.Value))
		}
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	tInfo := &terminalInfo{
		cmd:    cmd,
		output: &buf,
		done:   make(chan struct{}),
	}
	c.terminals[termId] = tInfo

	err := cmd.Start()
	if err != nil {
		return acp.CreateTerminalResponse{}, fmt.Errorf("start terminal command: %w", err)
	}

	go func() {
		err := cmd.Wait()
		tInfo.mu.Lock()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				tInfo.exitCode = exitErr.ExitCode()
			} else {
				tInfo.exitCode = -1
			}
		} else {
			tInfo.exitCode = 0
		}
		tInfo.mu.Unlock()
		close(tInfo.done)
	}()

	return acp.CreateTerminalResponse{TerminalId: termId}, nil
}

func (c *middleManagerClient) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	c.mu.Lock()
	tInfo, ok := c.terminals[string(params.TerminalId)]
	c.mu.Unlock()
	if !ok {
		return acp.KillTerminalResponse{}, fmt.Errorf("terminal not found: %s", params.TerminalId)
	}
	if tInfo.cmd.Process != nil {
		_ = tInfo.cmd.Process.Kill()
	}
	return acp.KillTerminalResponse{}, nil
}

func (c *middleManagerClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	c.mu.Lock()
	tInfo, ok := c.terminals[string(params.TerminalId)]
	c.mu.Unlock()
	if !ok {
		return acp.TerminalOutputResponse{}, fmt.Errorf("terminal not found: %s", params.TerminalId)
	}

	tInfo.mu.Lock()
	outStr := tInfo.output.String()
	tInfo.mu.Unlock()

	select {
	case <-tInfo.done:
		exitCode := tInfo.exitCode
		return acp.TerminalOutputResponse{
			Output:    outStr,
			Truncated: false,
			ExitStatus: &acp.TerminalExitStatus{
				ExitCode: acp.Ptr(exitCode),
			},
		}, nil
	default:
		return acp.TerminalOutputResponse{
			Output:    outStr,
			Truncated: false,
		}, nil
	}
}

func (c *middleManagerClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	c.mu.Lock()
	tInfo, ok := c.terminals[string(params.TerminalId)]
	if ok {
		delete(c.terminals, string(params.TerminalId))
	}
	c.mu.Unlock()

	if ok && tInfo.cmd.Process != nil {
		_ = tInfo.cmd.Process.Kill()
	}
	return acp.ReleaseTerminalResponse{}, nil
}

func (c *middleManagerClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	c.mu.Lock()
	tInfo, ok := c.terminals[string(params.TerminalId)]
	c.mu.Unlock()
	if !ok {
		return acp.WaitForTerminalExitResponse{}, fmt.Errorf("terminal not found: %s", params.TerminalId)
	}

	select {
	case <-tInfo.done:
		exitCode := tInfo.exitCode
		return acp.WaitForTerminalExitResponse{
			ExitCode: acp.Ptr(exitCode),
		}, nil
	case <-ctx.Done():
		return acp.WaitForTerminalExitResponse{}, ctx.Err()
	}
}

func RunAgentACP(
	ctx context.Context,
	agent string,
	prompt string,
	cwd string,
	model string,
	env []string,
	extraArgs []string,
	binaryOverride string,
	step string,
	onUpdate func(text string, isThought bool),
) (string, int, error) {
	cmdArgs := GetACPCommand(agent, binaryOverride)
	if model != "" {
		if agent == "grok" || agent == "opencode" {
			cmdArgs = append(cmdArgs, "-m", model)
		}
	}

	// Spawn agent process
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = cwd
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", -1, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", -1, err
	}

	if err := cmd.Start(); err != nil {
		return "", -1, fmt.Errorf("start agent process: %w", err)
	}

	var accumulatedText []string
	var mu sync.Mutex

	updateHandler := func(text string, isThought bool) {
		mu.Lock()
		accumulatedText = append(accumulatedText, text)
		mu.Unlock()
		if onUpdate != nil {
			onUpdate(text, isThought)
		}
	}

	client := &middleManagerClient{
		cwd:       cwd,
		onUpdate:  updateHandler,
		terminals: make(map[string]*terminalInfo),
	}

	conn := acp.NewClientSideConnection(client, stdin, stdout)
	conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))) // suppress internal ACP log spamming

	// Initialize negotiation
	_, err = conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs:       acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return "", -1, fmt.Errorf("ACP initialize failed: %w", err)
	}

	// Create session
	sessionResp, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return "", -1, fmt.Errorf("ACP session/new failed: %w", err)
	}

	// Prompt agent
	_, promptErr := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	})

	// Wait for process to exit
	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// Return error if prompt failed or process crashed
	if promptErr != nil && exitCode == 0 {
		return strings.Join(accumulatedText, ""), 1, promptErr
	}

	return strings.Join(accumulatedText, ""), exitCode, nil
}

func RunAgent(
	ctx context.Context,
	run *AgentRun,
	dryRun bool,
	stream bool,
	step string,
	onUpdate func(text string, isThought bool),
) (string, int, error) {
	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would run agent %s with prompt: %s\n", run.Agent, run.Prompt), 0, nil
	}

	if run.Tmux {
		return RunAgentTmux(ctx, run, step, onUpdate)
	}

	binaryOverride := ""
	if len(run.Command) > 0 {
		binaryOverride = run.Command[0]
	}

	stdout, exitCode, err := RunAgentACP(
		ctx,
		run.Agent,
		run.Prompt,
		run.Cwd,
		run.Model,
		run.Env,
		run.ExtraArgs,
		binaryOverride,
		step,
		onUpdate,
	)
	return stdout, exitCode, err
}

func RunAgentTmux(
	ctx context.Context,
	run *AgentRun,
	step string,
	onUpdate func(text string, isThought bool),
) (string, int, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		onUpdate("⚠ tmux not found on PATH — running agent normally without tmux\n", false)
		run.Tmux = false
		binaryOverride := ""
		if len(run.Command) > 0 {
			binaryOverride = run.Command[0]
		}
		return RunAgentACP(ctx, run.Agent, run.Prompt, run.Cwd, run.Model, run.Env, run.ExtraArgs, binaryOverride, step, onUpdate)
	}

	sessionName := fmt.Sprintf("mm-%s", step)
	// Kill existing session if any
	_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	mmDir := filepath.Join(run.Cwd, ".middle-manager")
	_ = os.MkdirAll(mmDir, 0755)

	logPath := filepath.Join(mmDir, fmt.Sprintf("tmux_%s.log", sessionName))
	exitPath := filepath.Join(mmDir, fmt.Sprintf("tmux_%s.exit", sessionName))
	scriptPath := filepath.Join(mmDir, fmt.Sprintf("tmux_%s.sh", sessionName))

	// Remove old files
	_ = os.Remove(logPath)
	_ = os.Remove(exitPath)
	_ = os.Remove(scriptPath)

	// Write execution script
	var scriptContent strings.Builder
	scriptContent.WriteString("#!/usr/bin/env bash\n")
	scriptContent.WriteString("set -e\n")
	for _, envVar := range run.Env {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			escapedVal := strings.ReplaceAll(parts[1], "'", "'\\''")
			scriptContent.WriteString(fmt.Sprintf("export %s='%s'\n", parts[0], escapedVal))
		}
	}
	var cmdStr strings.Builder
	for _, arg := range run.Command {
		escapedArg := "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
		cmdStr.WriteString(escapedArg + " ")
	}
	scriptContent.WriteString(cmdStr.String() + "\n")

	err := os.WriteFile(scriptPath, []byte(scriptContent.String()), 0755)
	if err != nil {
		return "", -1, fmt.Errorf("write tmux script: %w", err)
	}

	// Start tmux session
	tmuxCmd := exec.Command("tmux", "new-session", "-d",
		"-s", sessionName,
		"-x", "120",
		"-y", "40",
		"-c", run.Cwd,
		fmt.Sprintf("/bin/bash -c 'sleep 0.1; %s; echo $? > %s'", scriptPath, exitPath),
	)
	if err := tmuxCmd.Run(); err != nil {
		return "", -1, fmt.Errorf("start tmux session: %w", err)
	}

	// Set remain-on-exit so the pane doesn't close and TUI scrollback is preserved
	_ = exec.Command("tmux", "set-option", "-t", sessionName, "remain-on-exit", "on").Run()

	// Start piping pane output to log_path
	_ = exec.Command("tmux", "pipe-pane", "-t", sessionName, fmt.Sprintf("cat > %s", logPath)).Run()

	// Find pane_pid of the newly created session.
	panePid := -1
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		res, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_pid}").Output()
		if err == nil {
			val := strings.TrimSpace(string(res))
			if pid, err := strconv.Atoi(val); err == nil {
				panePid = pid
				break
			}
		}
	}

	if panePid != -1 {
		addActiveTmuxPID(panePid)
		defer removeActiveTmuxPID(panePid)
	}

	// Monitor and tail log file
	var logFile *os.File
	for i := 0; i < 20; i++ {
		lf, err := os.Open(logPath)
		if err == nil {
			logFile = lf
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
		_ = os.Remove(scriptPath)
		_ = os.Remove(exitPath)
	}()

	buf := make([]byte, 8192)
	var logOffset int64 = 0

	for {
		if logFile != nil {
			// Read new content from log file
			fi, err := logFile.Stat()
			if err == nil && fi.Size() > logOffset {
				_, _ = logFile.Seek(logOffset, io.SeekStart)
				n, err := logFile.Read(buf)
				if n > 0 {
					onUpdate(string(buf[:n]), false)
					logOffset += int64(n)
				}
				_ = err
			}
		}

		// Check if exit file exists
		if _, err := os.Stat(exitPath); err == nil {
			exitB, err := os.ReadFile(exitPath)
			if err == nil {
				exitStr := strings.TrimSpace(string(exitB))
				if exitCode, err := strconv.Atoi(exitStr); err == nil {
					// Read any remaining logs
					if logFile != nil {
						fi, err := logFile.Stat()
						if err == nil && fi.Size() > logOffset {
							_, _ = logFile.Seek(logOffset, io.SeekStart)
							for {
								n, _ := logFile.Read(buf)
								if n == 0 {
									break
								}
								onUpdate(string(buf[:n]), false)
							}
						}
					}
					stdoutB, _ := os.ReadFile(logPath)
					return string(stdoutB), exitCode, nil
				}
			}
		}

		select {
		case <-ctx.Done():
			_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()
			return "", -1, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func ListAgentsStatus(binaryOverrides map[string]string) []map[string]string {
	rows := []map[string]string{}
	for _, name := range AgentNames {
		spec := AgentSpecs[name]
		override := ""
		if binaryOverrides != nil {
			override = binaryOverrides[name]
		}
		path := ResolveBinary(name, override)
		available := "no"
		if path != "" {
			available = "yes"
		}
		binaryPath := path
		if binaryPath == "" {
			binaryPath = spec.Binary
		}
		rows = append(rows, map[string]string{
			"agent":     name,
			"binary":    binaryPath,
			"available": available,
			"yolo":      spec.YoloFlag,
			"notes":     spec.Notes,
		})
	}
	return rows
}

func AvailableAgents(binaryOverrides map[string]string) []string {
	res := []string{}
	for _, name := range AgentNames {
		override := ""
		if binaryOverrides != nil {
			override = binaryOverrides[name]
		}
		if AgentAvailable(name, override) {
			res = append(res, name)
		}
	}
	return res
}

var StepAgentPriority = map[string][]string{
	"discover": {"grok", "claude", "opencode", "codex"},
	"execute":  {"claude", "grok", "opencode", "codex"},
	"verify":   {"codex", "grok", "claude", "opencode"},
	"commit":   {"grok", "claude", "opencode", "codex"},
}

func AutodetectAgent(step string, binaryOverrides map[string]string, fallback string) string {
	priority := StepAgentPriority[step]
	if priority == nil {
		priority = AgentNames
	}
	for _, name := range priority {
		override := ""
		if binaryOverrides != nil {
			override = binaryOverrides[name]
		}
		if AgentAvailable(name, override) {
			return name
		}
	}
	return fallback
}

func AutodetectStepAgents(binaryOverrides map[string]string) map[string]string {
	installed := AvailableAgents(binaryOverrides)
	if len(installed) == 0 {
		return map[string]string{
			"discover": "grok",
			"execute":  "claude",
			"verify":   "codex",
			"commit":   "grok",
		}
	}

	assigned := make(map[string]string)
	steps := []string{"discover", "execute", "verify", "commit"}

	for _, step := range steps {
		priorityList := StepAgentPriority[step]
		if priorityList == nil {
			priorityList = AgentNames
		}
		chosen := ""
		// First pass: try to pick an installed agent that has NOT been assigned to any other step yet
		for _, name := range priorityList {
			alreadyAssigned := false
			for _, assignedAgent := range assigned {
				if assignedAgent == name {
					alreadyAssigned = true
					break
				}
			}
			isInstalled := false
			for _, inst := range installed {
				if inst == name {
					isInstalled = true
					break
				}
			}
			if isInstalled && !alreadyAssigned {
				chosen = name
				break
			}
		}

		// Second pass: pick the highest priority installed agent regardless of duplicate assignment
		if chosen == "" {
			for _, name := range priorityList {
				isInstalled := false
				for _, inst := range installed {
					if inst == name {
						isInstalled = true
						break
					}
				}
				if isInstalled {
					chosen = name
					break
				}
			}
		}

		// Third pass: absolute fallback
		if chosen == "" {
			chosen = priorityList[0]
		}

		assigned[step] = chosen
	}

	return assigned
}

func GetChangedFilesWithStatus(repo string) []string {
	if !gitops.RepoIsGit(repo) {
		return nil
	}
	stdout, _, code, err := gitops.RunGit(repo, "status", "--porcelain")
	if err != nil || code != 0 {
		return nil
	}
	if stdout == "" {
		return nil
	}

	var files []string
	lines := strings.Split(stdout, "\n")
	for _, line := range lines {
		if len(line) > 3 {
			status := strings.TrimSpace(line[:2])
			filename := strings.TrimSpace(line[3:])
			if strings.Contains(filename, " -> ") {
				parts := strings.Split(filename, " -> ")
				filename = strings.TrimSpace(parts[len(parts)-1])
			}
			statusDesc := "changed"
			switch status {
			case "M":
				statusDesc = "modified"
			case "A", "??":
				statusDesc = "new"
			case "D":
				statusDesc = "deleted"
			case "R":
				statusDesc = "renamed"
			}
			files = append(files, fmt.Sprintf("%s (%s)", filename, statusDesc))
		}
	}
	return files
}
