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

	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/coder/acp-go-sdk"
)

var AgentNames = []string{"grok", "claude", "codex", "opencode", "agy"}

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
		Notes:        "Claude Code via @agentclientprotocol/claude-agent-acp (https://github.com/agentclientprotocol/agent-client-protocol)",
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
		Notes:        "OpenAI Codex CLI via acp-adapter (https://github.com/beyond5959/acp-adapter)",
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
	"agy": {
		Name:         "agy",
		Binary:       "agy",
		YoloFlag:     "--always-approve",
		YoloPosition: "before_prompt",
		PromptMode:   "arg",
		PrintFlag:    "-p",
		ModelFlag:    "-m",
		CwdFlag:      "--cwd",
		Notes:        "Google Antigravity CLI via agy-acp (https://github.com/hicder/agy-acp)",
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
	}, nil
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
		return []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp"}
	} else if agent == "codex" {
		binName := "acp-adapter"
		if binary != "" && !strings.Contains(binary, "codex") {
			binName = binary
		}
		return []string{binName, "--adapter", "codex"}
	} else if agent == "agy" {
		binName := "agy-acp"
		if binary != "" && !strings.Contains(binary, "agy") {
			binName = binary
		}
		return []string{binName}
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
		if agent == "grok" {
			cmdArgs = append(cmdArgs, "-m", model)
		}
	}

	// Spawn agent process
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", -1, err
	}
	defer stdin.Close()
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
		ClientInfo: &acp.Implementation{
			Name:    "middle-manager",
			Version: "1.0.0",
		},
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

	// Close stdin pipe to signal EOF to the agent process and avoid deadlock
	_ = stdin.Close()

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
	"discover": {"grok", "claude", "opencode", "codex", "agy"},
	"execute":  {"opencode", "claude", "grok", "codex", "agy"},
	"verify":   {"claude", "grok", "opencode", "codex", "agy"},
	"commit":   {"grok", "opencode", "claude", "codex", "agy"},
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
			"verify":   "grok",
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
