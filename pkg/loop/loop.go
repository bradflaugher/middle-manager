package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/prompts"
	"github.com/bradflaugher/middle-manager/pkg/tui"
)

type LoopResult struct {
	Success    bool
	Reason     string
	PRURL      string
	Iterations int
}

type MiddleManagerLoop struct {
	cfg           *config.LoopConfig
	state         string
	fixPlanPath   string
	errorLogPath  string
	verifyLogPath string
	iterationPath string
	runID         string
	lastPRURL     string
	startTime     time.Time
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewMiddleManagerLoop(cfg *config.LoopConfig) *MiddleManagerLoop {
	state := cfg.StatePath()
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	return &MiddleManagerLoop{
		cfg:           cfg,
		state:         state,
		fixPlanPath:   filepath.Join(state, "fix_plan.md"),
		errorLogPath:  filepath.Join(state, "error_log.txt"),
		verifyLogPath: filepath.Join(state, "verify_log.txt"),
		iterationPath: filepath.Join(state, "iteration.txt"),
		runID:         runID,
		startTime:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (l *MiddleManagerLoop) Log(msg string, color string) {
	if color != "" {
		msg = colors.Colored(msg, color)
	}
	// If TUI is active, notify log update. Otherwise print directly.
	if l.cfg.StreamOutput {
		fmt.Println(msg)
	} else {
		tui.NotifyTUIUpdate(msg+"\n", false)
	}
}

func (l *MiddleManagerLoop) EnsureGitignore() {
	gitignore := filepath.Join(l.cfg.Repo, ".gitignore")
	b, err := os.ReadFile(gitignore)
	content := ""
	if err == nil {
		content = string(b)
	}
	if !strings.Contains(content, ".middle-manager") {
		f, err := os.OpenFile(gitignore, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			defer f.Close()
			_, _ = f.WriteString("\n# middle-manager state directory\n.middle-manager/\n")
		}
	}
}

func (l *MiddleManagerLoop) ReadIteration() int {
	b, err := os.ReadFile(l.iterationPath)
	if err != nil {
		return 1
	}
	n, err := strconvAtoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 1
	}
	return n
}

func (l *MiddleManagerLoop) WriteIteration(n int) {
	_ = os.WriteFile(l.iterationPath, []byte(fmt.Sprintf("%d\n", n)), 0644)
}

func (l *MiddleManagerLoop) ReadText(path string, defaultValue string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return defaultValue
	}
	return string(b)
}

func (l *MiddleManagerLoop) WriteText(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0644)
}

func (l *MiddleManagerLoop) TopPlanItem() string {
	items := l.TopPlanItems(1)
	if len(items) > 0 {
		return items[0]
	}
	return ""
}

func (l *MiddleManagerLoop) TopPlanItems(count int) []string {
	text := l.ReadText(l.fixPlanPath, "")
	var items []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") {
			task := strings.TrimSpace(strings.TrimPrefix(trimmed, "- [ ]"))
			if task != "" {
				items = append(items, task)
				if len(items) >= count {
					break
				}
			}
		}
	}
	return items
}

func (l *MiddleManagerLoop) AgentMemory() string {
	memFile := filepath.Join(l.cfg.Repo, l.cfg.AgentMemoryFile)
	if _, err := os.Stat(memFile); err == nil {
		return l.ReadText(memFile, "")
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		fallback := filepath.Join(l.cfg.Repo, name)
		if _, err := os.Stat(fallback); err == nil {
			return l.ReadText(fallback, "")
		}
	}
	return fmt.Sprintf("(no %s or CLAUDE.md found — create one with repo rules)", l.cfg.AgentMemoryFile)
}

func (l *MiddleManagerLoop) EnsureFixPlanSeed(issueData map[string]string) {
	if l.cfg.Mode == "feature" && l.cfg.Mission != "" {
		if l.cfg.Fresh || !fileExists(l.fixPlanPath) {
			l.SeedFeaturePlan()
		}
		return
	}
	if fileExists(l.fixPlanPath) {
		return
	}

	seed := "# fix_plan.md\n\n"
	if l.cfg.Mission != "" {
		seed += fmt.Sprintf("## Mission\n\n%s\n\n", l.cfg.Mission)
	}
	if title, ok := issueData["title"]; ok && title != "" {
		seed += fmt.Sprintf("## Issue #%s: %s\n\n", issueData["number"], title)
		if body, ok := issueData["body"]; ok && body != "" {
			seed += body + "\n\n"
		}
	}
	task := l.cfg.Mission
	if task == "" {
		task = "Investigate and scope the top priority item"
	}
	seed += fmt.Sprintf("## Tasks\n\n- [ ] %s\n", task)
	l.WriteText(l.fixPlanPath, seed)
}

func (l *MiddleManagerLoop) SeedFeaturePlan() {
	mission := strings.TrimSpace(l.cfg.Mission)
	body := fmt.Sprintf("# fix_plan.md\n\n## Feature\n\n%s\n\n## Tasks\n\n- [ ] %s\n", mission, mission)
	_ = os.MkdirAll(filepath.Dir(l.fixPlanPath), 0755)
	l.WriteText(l.fixPlanPath, body)
}

func (l *MiddleManagerLoop) PromptForStep(step string, iteration int, issueData map[string]string) string {
	sc := l.cfg.StepFor(step)
	templateName := step
	if step == "discover" && l.cfg.Mode == "feature" {
		templateName = "discover_feature"
	} else if sc.PromptFile != "" {
		templateName = sc.PromptFile
	}

	template := prompts.LoadPrompt(l.cfg.Repo, strings.TrimSuffix(templateName, ".md"))
	if step == "execute" && l.cfg.FixUnrelatedTests {
		ruleAddition := "\n6. **Fix unrelated test failures:** If the test suite is failing due to unrelated test failures or environment-specific issues that block verification of your changes, you are allowed and encouraged to modify the test files or unrelated files directly to fix the test failures so that they pass.\n"
		template += ruleAddition
	}

	topItems := l.TopPlanItems(l.cfg.BatchSize)
	topItemStr := ""
	if len(topItems) == 1 {
		topItemStr = topItems[0]
	} else if len(topItems) > 1 {
		var list []string
		for _, item := range topItems {
			list = append(list, fmt.Sprintf("- [ ] %s", item))
		}
		topItemStr = strings.Join(list, "\n")
	} else {
		topItemStr = "No actionable item in fix_plan.md — add `- [ ] task` lines."
	}

	ctx := prompts.BuildContext(
		l.cfg.Repo,
		l.cfg.Issue,
		l.ReadText(l.fixPlanPath, ""),
		topItemStr,
		l.AgentMemory(),
		l.ReadText(l.verifyLogPath, ""),
		l.ReadText(l.errorLogPath, ""),
		iteration,
		l.cfg.Mission,
	)

	ctx["issue_title"] = issueData["title"]
	ctx["issue_body"] = issueData["body"]
	ctx["issue_number"] = issueData["number"]

	return prompts.RenderPrompt(template, ctx)
}

func (l *MiddleManagerLoop) RunStep(step string, iteration int, issueData map[string]string) (string, int, error) {
	sc := l.cfg.StepFor(step)
	if !sc.Enabled {
		l.Log(fmt.Sprintf("Skipping disabled step: %s", step), "")
		return "", 0, nil
	}

	agent := sc.Agent
	binary := l.cfg.BinaryOverrides[agent]
	model := sc.Model
	if !agents.AgentAvailable(agent, binary) && !l.cfg.DryRun {
		fallback := agents.AutodetectAgent(step, l.cfg.BinaryOverrides, "")
		if fallback != "" && fallback != agent {
			l.Log(fmt.Sprintf("⚠️ Agent %s not found on PATH — falling back to %s for step %s", agent, fallback, step), colors.Yellow)
			agent = fallback
			binary = l.cfg.BinaryOverrides[agent]
			model = "" // Use fallback agent's default model
		} else {
			l.Log(fmt.Sprintf("Agent %s not found on PATH and no fallback available — skipping %s", agent, step), "")
			return "", 127, nil
		}
	}

	prompt := l.PromptForStep(step, iteration, issueData)
	interjection := tui.GetTUIInterjection()
	if interjection != "" {
		prompt += fmt.Sprintf("\n\n## Custom User Interjection / Direction\n%s\n", interjection)
		l.Log(fmt.Sprintf("Injected instruction into step %s: %q", step, interjection), colors.Green)
	}
	promptFile := filepath.Join(l.state, fmt.Sprintf("%s_prompt.md", step))
	l.WriteText(promptFile, prompt)

	run, err := agents.BuildCommand(
		agent,
		prompt,
		l.cfg.Repo,
		model,
		l.cfg.Yolo,
		sc.ExtraArgs,
		binary,
		promptFile,
		l.cfg.Interactive && step == "execute",
	)
	if err != nil {
		return "", -1, err
	}

	// Update TUI Status to Running Step
	if !l.cfg.StreamOutput {
		tui.NotifyTUIStatus(iteration, step, agent, "running", l.branchName(), time.Since(l.startTime))
	}

	// Callback to pipe output directly to stdout or monitor viewport
	onUpdate := func(text string, isThought bool) {
		if l.cfg.StreamOutput {
			os.Stdout.WriteString(text)
		} else {
			tui.NotifyTUIUpdate(text, isThought)
		}
	}

	stdout, exitCode, err := agents.RunAgent(l.ctx, run, l.cfg.DryRun, l.cfg.StreamOutput, step, onUpdate)

	outputFile := filepath.Join(l.state, fmt.Sprintf("%s_output.txt", step))
	l.WriteText(outputFile, stdout)

	if exitCode == 0 {
		l.Log(fmt.Sprintf("✅ Step %s (%s) finished successfully (exit code 0).", strings.ToUpper(step), strings.ToUpper(agent)), colors.Green)
	} else {
		l.Log(fmt.Sprintf("❌ Step %s (%s) failed (exit code %d).", strings.ToUpper(step), strings.ToUpper(agent), exitCode), colors.Red)
	}

	return stdout, exitCode, err
}

func (l *MiddleManagerLoop) MaybeCommitAndPR(iteration int, issueData map[string]string) {
	if l.cfg.Steps < 4 || !l.cfg.Commit.Enabled {
		if gitops.HasChanges(l.cfg.Repo) && !l.cfg.DryRun {
			msg := fmt.Sprintf("middle-manager: iteration %d — %s", iteration, l.TopPlanItem())
			if len(msg) > 72 {
				msg = msg[:72]
			}
			if gitops.CommitAll(l.cfg.Repo, msg) {
				l.Log("Committed changes (3-step mode, no PR agent)", colors.Green)
				if gitops.RepoIsGit(l.cfg.Repo) {
					branch, _ := gitops.CurrentBranch(l.cfg.Repo)
					gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun)
					l.Log(fmt.Sprintf("Pushed branch '%s' to origin", branch), colors.Green)
				}
			}
		}
		return
	}

	_, exitCode, err := l.RunStep("commit", iteration, issueData)
	if err != nil || exitCode != 0 {
		l.Log("Commit step failed; leaving working tree as-is", colors.Yellow)
		return
	}

	if !gitops.RepoIsGit(l.cfg.Repo) {
		return
	}

	branch, _ := gitops.CurrentBranch(l.cfg.Repo)
	if !l.cfg.NoPR {
		gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun)

		title := fmt.Sprintf("middle-manager: %s", l.TopPlanItem())
		if len(title) > 60 {
			title = title[:60]
		}
		body := fmt.Sprintf(
			"Automated PR from middle-manager loop iteration %d.\n\n**Do not merge without human review.**\n\nPlan: `%s`",
			iteration,
			l.fixPlanPath,
		)
		prURL, err := gitops.CreatePR(
			l.cfg.Repo,
			title,
			body,
			branch,
			issueData["number"],
			l.cfg.DryRun,
		)
		if err == nil && prURL != "" {
			l.lastPRURL = prURL
			l.Log(fmt.Sprintf("PR created: %s", prURL), colors.Green)
		}
	}
}

func (l *MiddleManagerLoop) ParseVerifierUpdates(stdout string) (string, []string) {
	verdict := "UNKNOWN"
	reVerdict := regexp.MustCompile(`(?i)VERDICT:\s*(PASS|FAIL)`)
	m := reVerdict.FindStringSubmatch(stdout)
	if len(m) > 1 {
		verdict = strings.ToUpper(m[1])
	}

	var updates []string
	lines := strings.Split(stdout, "\n")
	inUpdates := false
	reHeader := regexp.MustCompile(`(?i)^(FIX[-_]PLAN[-_]UPDATES|PLAN[-_]UPDATES):`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if reHeader.MatchString(trimmed) {
			inUpdates = true
			continue
		}
		if inUpdates {
			if strings.HasPrefix(trimmed, "-") {
				task := trimmed
				if !strings.HasPrefix(task, "- [ ]") && !strings.HasPrefix(task, "- [x]") {
					task = "- [ ] " + strings.TrimSpace(strings.TrimPrefix(task, "-"))
				}
				updates = append(updates, task)
			} else if strings.HasPrefix(trimmed, "VERDICT:") || strings.HasPrefix(trimmed, "SUMMARY:") || strings.HasPrefix(trimmed, "ISSUES:") || strings.HasPrefix(trimmed, "```") {
				inUpdates = false
			} else {
				if len(updates) > 0 {
					inUpdates = false
				}
			}
		}
	}
	return verdict, updates
}

func (l *MiddleManagerLoop) AddTasksToPlan(newTasks []string) {
	if len(newTasks) == 0 {
		return
	}
	text := l.ReadText(l.fixPlanPath, "")
	lines := strings.Split(text, "\n")

	tasksIndex := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Tasks") || strings.HasPrefix(trimmed, "## Task") {
			tasksIndex = i
			break
		}
	}

	if tasksIndex != -1 {
		insertIndex := tasksIndex + 1
		for insertIndex < len(lines) {
			trimmed := strings.TrimSpace(lines[insertIndex])
			if trimmed != "" && !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "#") {
				break
			}
			insertIndex++
		}

		// Insert in reverse order to maintain order
		for i := len(newTasks) - 1; i >= 0; i-- {
			lines = append(lines[:insertIndex], append([]string{newTasks[i]}, lines[insertIndex:]...)...)
		}
		l.Log(fmt.Sprintf("Added %d task(s) suggested by verifier to fix_plan.md", len(newTasks)), colors.Green)
	} else {
		lines = append(lines, "\n## Tasks")
		lines = append(lines, newTasks...)
		l.Log(fmt.Sprintf("Appended %d task(s) suggested by verifier to end of fix_plan.md", len(newTasks)), colors.Green)
	}

	l.WriteText(l.fixPlanPath, strings.Join(lines, "\n")+"\n")
}

func (l *MiddleManagerLoop) RunOnce(iteration int, issueData map[string]string) bool {
	l.Log(fmt.Sprintf("\n🔄 ITERATION %d branch: %s", iteration, l.branchName()), colors.Cyan+colors.Bold)

	if !l.cfg.StreamOutput {
		tui.NotifyTUIPlan(l.ReadText(l.fixPlanPath, ""))
		tui.NotifyTUIStatus(iteration, "discover", "", "running", l.branchName(), time.Since(l.startTime))
	}

	verifierPassed := true
	verifierStdout := ""
	tasksBefore := -1

	activeSteps := l.cfg.ActiveSteps()

	for _, step := range activeSteps {
		if step == "execute" {
			tasksBefore = l.countPendingTasks()
		}

		if step == "commit" {
			continue // Handled separately
		}

		sc := l.cfg.StepFor(step)
		l.Log(fmt.Sprintf("\n[Step: %s] Starting step with agent '%s'...", strings.ToUpper(step), strings.ToUpper(sc.Agent)), colors.Cyan)

		// Wait/Pause check if TUI is paused
		l.checkTUIPause()

		// Skip step check
		if tui.IsTUISkipStep() {
			l.Log(fmt.Sprintf("Skipped step: %s", step), colors.Yellow)
			continue
		}

		// Quit check
		if tui.IsTUIQuitting() {
			return false
		}

		stdout, exitCode, err := l.RunStep(step, iteration, issueData)
		if err != nil {
			l.Log(fmt.Sprintf("Step %s failed with error: %v", step, err), colors.Red)
		}

		if step == "verify" {
			verifierStdout = stdout
			if exitCode != 0 {
				l.Log("❌ Verifier reported CLI error/failure", colors.Red)
				verifierPassed = false
			}
		}
	}

	if contains(activeSteps, "verify") && verifierPassed {
		verdict, planUpdates := l.ParseVerifierUpdates(verifierStdout)
		l.Log(fmt.Sprintf("🔍 Verifier Verdict: %s", verdict), colors.Green)
		if verdict == "FAIL" {
			verifierPassed = false
			l.Log("⚠️ Verifier reported failure — will loop back", colors.Yellow)
			existingErr := l.ReadText(l.errorLogPath, "")
			header := fmt.Sprintf("\n=== VERIFIER FEEDBACK (Iteration %d) ===\n", iteration)
			l.WriteText(l.errorLogPath, header+verifierStdout+"\n"+existingErr)
		}
		if len(planUpdates) > 0 {
			l.AddTasksToPlan(planUpdates)
		}
	}

	if !verifierPassed {
		return true // Continue loop
	}

	if contains(activeSteps, "commit") {
		if gitops.RepoIsGit(l.cfg.Repo) {
			if l.cfg.Issue != "" && isDigit(l.cfg.Issue) {
				_, _ = gitops.EnsureIssueBranch(l.cfg.Repo, l.cfg.BranchPrefix, l.cfg.Issue, l.cfg.BaseBranch)
			} else {
				_, _ = gitops.EnsureBranch(l.cfg.Repo, l.cfg.BranchPrefix, iteration, l.cfg.BaseBranch)
			}
		}
		l.MaybeCommitAndPR(iteration, issueData)
	} else {
		l.MaybeCommitAndPR(iteration, issueData)
	}

	// Mark top tasks done if passed
	if tasksBefore != -1 {
		tasksAfter := l.countPendingTasks()
		if tasksAfter >= tasksBefore {
			l.CheckOffTopItems(l.cfg.BatchSize)
		}
	} else {
		l.CheckOffTopItems(l.cfg.BatchSize)
	}

	return true
}

func (l *MiddleManagerLoop) IsComplete() bool {
	return gitops.PlanIsComplete(l.ReadText(l.fixPlanPath, ""))
}

func (l *MiddleManagerLoop) CheckOffTopItems(count int) {
	text := l.ReadText(l.fixPlanPath, "")
	lines := strings.Split(text, "\n")
	checked := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") {
			lines[i] = strings.Replace(line, "- [ ]", "- [x]", 1)
			checked++
			if checked >= count {
				break
			}
		}
	}
	if checked > 0 {
		l.WriteText(l.fixPlanPath, strings.Join(lines, "\n")+"\n")
	}
}

func (l *MiddleManagerLoop) ResolveStepAgents() {
	activeSteps := l.cfg.ActiveSteps()
	installed := agents.AvailableAgents(l.cfg.BinaryOverrides)
	if len(installed) == 0 {
		return
	}

	assigned := make(map[string]string)
	
	// First pass: keep explicitly requested agents if they are installed
	for _, step := range activeSteps {
		sc := l.cfg.StepFor(step)
		binary := l.cfg.BinaryOverrides[sc.Agent]
		if agents.AgentAvailable(sc.Agent, binary) {
			assigned[step] = sc.Agent
		}
	}

	// Second pass: for missing agents, assign available agents trying to diversify
	for _, step := range activeSteps {
		if _, ok := assigned[step]; ok {
			continue
		}

		sc := l.cfg.StepFor(step)
		priorityList := agents.StepAgentPriority[step]
		if priorityList == nil {
			priorityList = agents.AgentNames
		}

		chosen := ""
		// Try to find an installed agent that is not yet assigned to any step
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

		// Fallback: pick the highest priority installed agent (allowing duplicate assignment)
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

		if chosen != "" {
			l.Log(fmt.Sprintf("⚠️ Agent %s for step %s not found on PATH — falling back to %s to diversify agents", sc.Agent, step, chosen), colors.Yellow)
			sc.Agent = chosen
		}
	}
}

func (l *MiddleManagerLoop) RunUntilComplete() (*LoopResult, error) {
	if _, err := os.Stat(l.cfg.Repo); os.IsNotExist(err) {
		return &LoopResult{Success: false, Reason: fmt.Sprintf("Repo not found: %s", l.cfg.Repo)}, nil
	}

	l.ResolveStepAgents()

	if l.cfg.Fresh {
		l.ResetLoopState()
	}

	l.EnsureGitignore()

	branch := "non-git"
	baseBranch := "n/a"
	if gitops.RepoIsGit(l.cfg.Repo) {
		baseBranch = l.cfg.BaseBranch
		if baseBranch == "" {
			baseBranch = gitops.DetectBaseBranch(l.cfg.Repo)
		}
		iteration := l.ReadIteration()
		var err error
		if l.cfg.Issue != "" && isDigit(l.cfg.Issue) {
			branch, err = gitops.EnsureIssueBranch(l.cfg.Repo, l.cfg.BranchPrefix, l.cfg.Issue, baseBranch)
		} else {
			branch, err = gitops.EnsureBranch(l.cfg.Repo, l.cfg.BranchPrefix, iteration, baseBranch)
		}
		if err != nil {
			return nil, fmt.Errorf("git branch setup: %w", err)
		}
		l.Log(fmt.Sprintf("Started loop on branch %q off base %q", branch, baseBranch), "")
	}

	issueData := gitops.FetchIssue(l.cfg.Repo, l.cfg.Issue)
	l.EnsureFixPlanSeed(issueData)

	// Start resources tracking goroutine
	if !l.cfg.StreamOutput {
		go l.trackResourcesBackground()
	}

	iteration := l.ReadIteration()
	ran := 0

	for i := 0; i < l.cfg.MaxIterations; i++ {
		// Verify complete check first
		if l.IsComplete() {
			l.Log("Plan complete — all tasks checked off.", colors.Green)
			return &LoopResult{Success: true, Reason: "plan complete", PRURL: l.lastPRURL, Iterations: ran}, nil
		}

		if !l.RunOnce(iteration, issueData) {
			return &LoopResult{Success: false, Reason: "Stopped by user", PRURL: l.lastPRURL, Iterations: ran}, nil
		}

		ran++
		iteration++
		l.WriteIteration(iteration)
	}

	if l.IsComplete() {
		l.Log("Plan complete — all tasks checked off.", colors.Green)
		return &LoopResult{Success: true, Reason: "plan complete", PRURL: l.lastPRURL, Iterations: ran}, nil
	}

	return &LoopResult{Success: false, Reason: fmt.Sprintf("Max iterations (%d) reached", l.cfg.MaxIterations), PRURL: l.lastPRURL, Iterations: ran}, nil
}

func (l *MiddleManagerLoop) ResetLoopState() {
	state := l.cfg.StatePath()
	names := []string{"fix_plan.md", "iteration.txt", "error_log.txt", "verify_log.txt", "discover_prompt.md", "execute_prompt.md", "verify_prompt.md", "session.log"}
	for _, n := range names {
		_ = os.Remove(filepath.Join(state, n))
	}
	_ = os.RemoveAll(filepath.Join(state, "issues"))

	if gitops.RepoIsGit(l.cfg.Repo) {
		gitops.CheckoutDefaultBranch(l.cfg.Repo)
		// Clean up MM loop branches
		branches, _, _, _ := gitops.RunGit(l.cfg.Repo, "branch")
		for _, b := range strings.Split(branches, "\n") {
			b = strings.TrimSpace(b)
			b = strings.TrimPrefix(b, "*")
			b = strings.TrimSpace(b)
			if strings.HasPrefix(b, l.cfg.BranchPrefix+"/loop-") || strings.HasPrefix(b, l.cfg.BranchPrefix+"/issue-") {
				_, _, _, _ = gitops.RunGit(l.cfg.Repo, "branch", "-D", b)
			}
		}
	}
}

func (l *MiddleManagerLoop) branchName() string {
	if gitops.RepoIsGit(l.cfg.Repo) {
		b, _ := gitops.CurrentBranch(l.cfg.Repo)
		return b
	}
	return "non-git"
}

func (l *MiddleManagerLoop) countPendingTasks() int {
	text := l.ReadText(l.fixPlanPath, "")
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- [ ]") {
			count++
		}
	}
	return count
}

func (l *MiddleManagerLoop) checkTUIPause() {
	for tui.IsTUIPaused() {
		time.Sleep(500 * time.Millisecond)
	}
}



func (l *MiddleManagerLoop) trackResourcesBackground() {
	var lastTicks *float64
	lastTime := time.Now()
	myPid := os.Getpid()

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
			// Query descendant stats
			descCount, sockCount := agents.GetProcessTreeStats(myPid)
			cpu, ticks, curTime := agents.CalculateCPUPercent(myPid, lastTicks, lastTime)
			lastTicks = &ticks
			lastTime = curTime

			tui.NotifyTUIStats(descCount, sockCount, cpu)
			tui.NotifyTUIStatus(l.ReadIteration(), "", "", "running", l.branchName(), time.Since(l.startTime))

			time.Sleep(2 * time.Second)
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func contains(arr []string, s string) bool {
	for _, item := range arr {
		if item == s {
			return true
		}
	}
	return false
}

func strconvAtoi(s string) (int, error) {
	res := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not numeric")
		}
		res = res*10 + int(r-'0')
	}
	return res, nil
}
