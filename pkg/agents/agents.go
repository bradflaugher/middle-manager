package agents

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bradflaugher/middle-manager/pkg/gitops"
)

// AgentNames is the ordered roster of coding-agent CLIs middle-manager can stack.
var AgentNames = []string{"grok", "claude", "codex", "opencode", "agy", "crush"}

// AgentSpec describes how to invoke one coding-agent CLI in plain headless mode.
//
// middle-manager runs every agent as an ordinary subprocess — "the stack" — and
// streams its stdout/stderr live. No Agent Client Protocol, no third-party
// adapters, no API-key-only auth paths: just the CLI each agent already ships,
// driven the way you'd drive it by hand.
type AgentSpec struct {
	Name string
	// Binary is the executable name resolved on PATH.
	Binary string
	// Subcommand is inserted right after the binary (e.g. {"run"}, {"exec"}).
	Subcommand []string
	// PrintFlag, when set, carries the prompt as its value (grok/claude/agy: "-p").
	// When empty, the prompt is appended as the trailing positional argument
	// (opencode, codex).
	PrintFlag string
	// YoloFlags are appended when auto-approve / unattended execution is on.
	YoloFlags []string
	// ModelFlag selects a model when one is configured.
	ModelFlag string
	// CwdFlag passes the working directory as a flag. cmd.Dir is set regardless,
	// so this is only for agents that also want it spelled out explicitly.
	CwdFlag string
	// ExtraArgs are always-on flags for this agent.
	ExtraArgs []string
	Notes     string
}

var AgentSpecs = map[string]AgentSpec{
	"grok": {
		Name:      "grok",
		Binary:    "grok",
		PrintFlag: "-p",
		YoloFlags: []string{"--always-approve"},
		ModelFlag: "-m",
		CwdFlag:   "--cwd",
		Notes:     "xAI Grok — grok -p PROMPT --cwd DIR --always-approve",
	},
	"claude": {
		Name:      "claude",
		Binary:    "claude",
		PrintFlag: "-p",
		YoloFlags: []string{"--dangerously-skip-permissions"},
		ModelFlag: "--model",
		Notes:     "Claude Code (headless, OAuth login) — claude -p PROMPT --dangerously-skip-permissions",
	},
	"opencode": {
		Name:       "opencode",
		Binary:     "opencode",
		Subcommand: []string{"run"},
		YoloFlags:  []string{"--dangerously-skip-permissions"},
		ModelFlag:  "-m",
		CwdFlag:    "--dir",
		Notes:      "opencode run PROMPT --dir DIR --dangerously-skip-permissions",
	},
	"codex": {
		Name:       "codex",
		Binary:     "codex",
		Subcommand: []string{"exec"},
		YoloFlags:  []string{"--dangerously-bypass-approvals-and-sandbox"},
		ModelFlag:  "-m",
		CwdFlag:    "-C",
		Notes:      "OpenAI Codex — codex exec PROMPT -C DIR --dangerously-bypass-approvals-and-sandbox",
	},
	"agy": {
		Name:      "agy",
		Binary:    "agy",
		PrintFlag: "-p",
		YoloFlags: []string{"--dangerously-skip-permissions"},
		ModelFlag: "--model",
		Notes:     "Google Antigravity (agy) — agy -p PROMPT --dangerously-skip-permissions",
	},
	"crush": {
		Name:       "crush",
		Binary:     "crush",
		Subcommand: []string{"run"},
		// `crush run` is non-interactive and auto-applies tool calls — there is
		// no permission prompt to bypass (and --yolo is rejected on `run`).
		ModelFlag: "-m",
		CwdFlag:   "-c",
		Notes:     "Charmbracelet Crush — crush run PROMPT -c DIR",
	},
}

type AgentRun struct {
	Agent     string
	Command   []string
	Prompt    string
	Cwd       string
	Model     string
	Yolo      bool
	ExtraArgs []string
	Env       []string
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

// BuildCommand assembles the argv for a single headless agent invocation.
// It never builds a shell string — every element is a discrete argv entry, so a
// prompt containing shell metacharacters cannot escape into the shell.
func BuildCommand(
	agent string,
	prompt string,
	cwd string,
	model string,
	yolo bool,
	extraArgs []string,
	binaryOverride string,
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
	cmd = append(cmd, spec.Subcommand...)

	promptPlaced := false
	if spec.PrintFlag != "" {
		cmd = append(cmd, spec.PrintFlag, prompt)
		promptPlaced = true
	}

	if yolo {
		cmd = append(cmd, spec.YoloFlags...)
	}
	if model != "" && spec.ModelFlag != "" {
		cmd = append(cmd, spec.ModelFlag, model)
	}
	if spec.CwdFlag != "" {
		cmd = append(cmd, spec.CwdFlag, cwd)
	}
	cmd = append(cmd, spec.ExtraArgs...)
	cmd = append(cmd, extraArgs...)

	if !promptPlaced {
		cmd = append(cmd, prompt)
	}

	return &AgentRun{
		Agent:     agent,
		Command:   cmd,
		Prompt:    prompt,
		Cwd:       cwd,
		Model:     model,
		Yolo:      yolo,
		ExtraArgs: extraArgs,
		Env:       os.Environ(),
	}, nil
}

// cleanAgentEnv strips the host Claude Code session variables so a nested
// `claude` (or any agent that respects them) starts a fresh session with the
// user's own OAuth login rather than inheriting middle-manager's parent session.
func cleanAgentEnv(env []string) []string {
	var cleaned []string
	for _, envVar := range env {
		if strings.HasPrefix(envVar, "CLAUDECODE=") ||
			strings.HasPrefix(envVar, "CLAUDE_CODE_ENTRYPOINT=") ||
			strings.HasPrefix(envVar, "CLAUDE_CODE_SSE_PORT=") ||
			strings.HasPrefix(envVar, "CLAUDE_CODE_SESSION_ID=") ||
			strings.HasPrefix(envVar, "CLAUDE_CODE_CHILD_SESSION=") ||
			strings.HasPrefix(envVar, "CLAUDE_CODE_EXECPATH=") ||
			strings.HasPrefix(envVar, "CLAUDE_AGENT_SDK_VERSION=") {
			continue
		}
		cleaned = append(cleaned, envVar)
	}
	return cleaned
}

// withRootSandbox appends IS_SANDBOX=1 when running as root (euid 0) and it is
// not already set. Claude Code refuses --dangerously-skip-permissions under
// root/sudo unless IS_SANDBOX is set; middle-manager's YOLO mode is already an
// explicit "I accept the risk" context, so we mirror the common
// `IS_SANDBOX=1 claude …` workaround. It's harmless for agents that don't read
// the variable. No-op for non-root users (and on Windows, where Geteuid is -1).
func withRootSandbox(env []string, euid int) []string {
	if euid != 0 {
		return env
	}
	for _, e := range env {
		if strings.HasPrefix(e, "IS_SANDBOX=") {
			return env // respect an explicit user setting
		}
	}
	return append(env, "IS_SANDBOX=1")
}

// RunAgentCLI runs one agent step as a plain subprocess in its own process
// group, streaming stdout (foreground) and stderr (greyed "thinking") into
// onUpdate line by line with ANSI stripped. Cancelling ctx terminates the whole
// group. It returns the accumulated output and the process exit code.
//
// Agents spawn their own subprocesses (shells, language servers, test runners);
// signalling the whole group prevents orphaned, token-burning children from
// outliving an aborted step.
func RunAgentCLI(
	ctx context.Context,
	run *AgentRun,
	onUpdate func(text string, isThought bool),
) (string, int, error) {
	if len(run.Command) == 0 {
		return "", -1, fmt.Errorf("empty command for agent %q", run.Agent)
	}

	cmd := exec.Command(run.Command[0], run.Command[1:]...)
	cmd.Dir = run.Cwd
	cmd.Env = withRootSandbox(cleanAgentEnv(run.Env), os.Geteuid())
	setProcGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", -1, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return "", -1, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return "", -1, fmt.Errorf("start agent %q: %w", run.Agent, err)
	}

	// On cancellation, SIGTERM the group; escalate to SIGKILL only if the
	// process hasn't exited within the grace window. The stop channel lets a
	// clean exit cancel the escalation, so we never signal a reaped (possibly
	// recycled) PID.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcGroup(cmd, false)
			select {
			case <-stop:
			case <-time.After(4 * time.Second):
				killProcGroup(cmd, true)
			}
		case <-stop:
		}
	}()

	var (
		mu  sync.Mutex
		buf strings.Builder
		wg  sync.WaitGroup
	)

	stream := func(r io.Reader, isThought bool) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := StripAnsi(sc.Text())
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
			if onUpdate != nil && strings.TrimSpace(line) != "" {
				onUpdate(line+"\n", isThought)
			}
		}
	}

	wg.Add(2)
	go stream(stdoutPipe, false)
	go stream(stderrPipe, true)
	wg.Wait()

	waitErr := cmd.Wait()
	close(stop)

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	if ctx.Err() != nil {
		// Step was cancelled/aborted by the operator.
		return buf.String(), 130, ctx.Err()
	}

	return buf.String(), exitCode, nil
}

// RunAgent executes an AgentRun (or prints what it would run in dry-run mode).
func RunAgent(
	ctx context.Context,
	run *AgentRun,
	dryRun bool,
	step string,
	onUpdate func(text string, isThought bool),
) (string, int, error) {
	if dryRun {
		msg := fmt.Sprintf("[DRY RUN] %s → %s\n", strings.ToUpper(run.Agent), FormatCmdForDisplay(run.Command))
		if onUpdate != nil {
			onUpdate(msg, false)
		}
		return msg, 0, nil
	}
	return RunAgentCLI(ctx, run, onUpdate)
}

// ---------------------------------------------------------------------------
// Process-tree resource accounting (Linux /proc) — feeds the TUI resource panel
// ---------------------------------------------------------------------------

// GetProcessTreeCPUTicks retrieves cpu ticks for pid and all its descendants.
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

	descendants := descendantsOf(parentPid, ppidMap)

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

	descendants := descendantsOf(parentPid, ppidMap)

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

func descendantsOf(parentPid int, ppidMap map[int]int) map[int]bool {
	descendants := map[int]bool{parentPid: true}
	for changed := true; changed; {
		changed = false
		for pid, ppid := range ppidMap {
			if descendants[ppid] && !descendants[pid] {
				descendants[pid] = true
				changed = true
			}
		}
	}
	return descendants
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
	const clkTck = 100.0 // SC_CLK_TCK is 100 on Linux
	cpuPercent := (dTicks / (clkTck * dt)) * 100.0
	return cpuPercent, currentTicks, currentTime
}

var ansiRe = regexp.MustCompile(`\x1B(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~]|[@-Z\\-_])`)

func StripAnsi(text string) string {
	return ansiRe.ReplaceAllString(text, "")
}

func FormatCmdForDisplay(command []string) string {
	quoted := make([]string, 0, len(command))
	for _, arg := range command {
		if strings.Contains(arg, "\n") || len(arg) > 150 {
			first := strings.SplitN(arg, "\n", 2)[0]
			if len(first) > 80 {
				first = first[:80]
			}
			quoted = append(quoted, fmt.Sprintf("%q…[prompt]", first))
		} else if strings.ContainsAny(arg, " \"'") {
			quoted = append(quoted, fmt.Sprintf("%q", arg))
		} else {
			quoted = append(quoted, arg)
		}
	}
	return strings.Join(quoted, " ")
}

// ---------------------------------------------------------------------------
// Agent roster / autodetection
// ---------------------------------------------------------------------------

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

// StepAgentPriority encodes a sensible default agent per loop step, ordered by
// preference. Each agent is run in plain headless mode, so any of them is a
// valid choice for any step — these are just opinionated defaults.
var StepAgentPriority = map[string][]string{
	"discover": {"claude", "grok", "opencode", "crush", "codex", "agy"},
	"execute":  {"opencode", "codex", "claude", "crush", "grok", "agy"},
	"verify":   {"claude", "grok", "crush", "codex", "opencode", "agy"},
	"commit":   {"grok", "opencode", "crush", "claude", "codex", "agy"},
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

// AutodetectStepAgents assigns an installed agent to each loop step, preferring
// to diversify (a different agent per step) before reusing one.
func AutodetectStepAgents(binaryOverrides map[string]string) map[string]string {
	installed := AvailableAgents(binaryOverrides)
	steps := []string{"discover", "execute", "verify", "commit"}

	if len(installed) == 0 {
		return map[string]string{
			"discover": "claude", "execute": "opencode", "verify": "claude", "commit": "grok",
		}
	}

	assigned := make(map[string]string)
	installedSet := make(map[string]bool, len(installed))
	for _, n := range installed {
		installedSet[n] = true
	}

	for _, step := range steps {
		priority := StepAgentPriority[step]
		if priority == nil {
			priority = AgentNames
		}
		chosen := ""
		// Prefer an installed agent not yet assigned to another step.
		for _, name := range priority {
			if installedSet[name] && !isAssigned(assigned, name) {
				chosen = name
				break
			}
		}
		// Otherwise reuse the highest-priority installed agent.
		if chosen == "" {
			for _, name := range priority {
				if installedSet[name] {
					chosen = name
					break
				}
			}
		}
		if chosen == "" {
			chosen = priority[0]
		}
		assigned[step] = chosen
	}
	return assigned
}

func isAssigned(assigned map[string]string, name string) bool {
	for _, v := range assigned {
		if v == name {
			return true
		}
	}
	return false
}

func GetChangedFilesWithStatus(repo string) []string {
	if !gitops.RepoIsGit(repo) {
		return nil
	}
	stdout, _, code, err := gitops.RunGit(repo, "status", "--porcelain")
	if err != nil || code != 0 || stdout == "" {
		return nil
	}

	var files []string
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) <= 3 {
			continue
		}
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
	return files
}
