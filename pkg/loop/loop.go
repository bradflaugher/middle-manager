package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	success       bool
	errorLogPath  string
	verifyLogPath string
	iterationPath string
	runID         string
	lastPRURL     string
	startTime     time.Time
	ctx           context.Context
	cancel        context.CancelFunc

	// stall detection — bail when the loop stops making progress.
	lastSignature string
	stallCount    int
	stalled       bool
	stallReason   string
}

func NewMiddleManagerLoop(cfg *config.LoopConfig) *MiddleManagerLoop {
	state := cfg.StatePath()
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	return &MiddleManagerLoop{
		cfg:           cfg,
		state:         state,
		errorLogPath:  filepath.Join(state, "error_log.txt"),
		verifyLogPath: filepath.Join(state, "verify_log.txt"),
		iterationPath: filepath.Join(state, "iteration.txt"),
		runID:         runID,
		startTime:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Cancel aborts the loop's context, terminating any in-flight agent process
// group. Called when the operator quits the TUI so control returns immediately
// instead of blocking on a long-running agent step.
func (l *MiddleManagerLoop) Cancel() {
	if l.cancel != nil {
		l.cancel()
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
	return l.cfg.Mission
}

func (l *MiddleManagerLoop) TopPlanItems(count int) []string {
	return []string{l.cfg.Mission}
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

func (l *MiddleManagerLoop) EnsureFixPlanSeed(issueData map[string]string) {}

func (l *MiddleManagerLoop) SeedFeaturePlan() {}

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

	discoverOutput := ""
	discoverOutputFile := filepath.Join(l.state, "discover_output.txt")
	if fileExists(discoverOutputFile) {
		discoverOutput = l.ReadText(discoverOutputFile, "")
	}

	ctx := prompts.BuildContext(
		l.cfg.Repo,
		l.cfg.Issue,
		discoverOutput,
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

	stdout, exitCode, err := agents.RunAgent(l.ctx, run, l.cfg.DryRun, step, onUpdate)

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
			subject := l.TopPlanItem()
			if subject == "" && issueData["number"] != "" {
				subject = "issue #" + issueData["number"]
			}
			msg := fmt.Sprintf("middle-manager: iteration %d — %s", iteration, subject)
			if len(msg) > 72 {
				msg = msg[:72]
			}
			committed, err := gitops.CommitAllWithError(l.cfg.Repo, msg)
			if err != nil {
				l.Log(fmt.Sprintf("Commit failed: %v", err), colors.Yellow)
				return
			}
			if committed {
				l.Log("Committed changes (3-step mode, no PR agent)", colors.Green)
				if gitops.RepoIsGit(l.cfg.Repo) {
					branch, _ := gitops.CurrentBranch(l.cfg.Repo)
					if err := gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun); err != nil {
						l.Log(fmt.Sprintf("Could not push branch %q: %v", branch, err), colors.Yellow)
					} else {
						l.Log(fmt.Sprintf("Pushed branch %q to origin", branch), colors.Green)
					}
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
		if err := gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun); err != nil {
			l.Log(fmt.Sprintf("Could not push branch %q; skipping PR creation: %v", branch, err), colors.Yellow)
			return
		}

		title := fmt.Sprintf("middle-manager: %s", l.cfg.Mission)
		if len(title) > 60 {
			title = title[:60]
		}
		body := fmt.Sprintf(
			"Automated PR from middle-manager loop iteration %d.\n\n**Do not merge without human review.**",
			iteration,
		)
		baseBranch := l.cfg.BaseBranch
		if baseBranch == "" {
			baseBranch = gitops.DetectBaseBranch(l.cfg.Repo)
		}
		prURL, err := gitops.CreatePR(
			l.cfg.Repo,
			title,
			body,
			branch,
			baseBranch,
			issueData["number"],
			l.cfg.DryRun,
		)
		if err != nil {
			l.Log(fmt.Sprintf("⚠️ PR creation failed (branch %q is pushed; open the PR manually): %v", branch, err), colors.Yellow)
		} else if prURL != "" {
			l.lastPRURL = prURL
			l.Log(fmt.Sprintf("PR created: %s", prURL), colors.Green)

			if !l.cfg.NoMerge {
				parts := strings.Split(strings.TrimSpace(prURL), "/")
				if len(parts) > 0 {
					prNumStr := parts[len(parts)-1]
					prNum, err := strconv.Atoi(prNumStr)
					if err == nil {
						l.Log(fmt.Sprintf("Enabling GitHub auto-merge on PR #%d...", prNum), colors.Cyan)
						out, err := gitops.EnableAutoMerge(l.cfg.Repo, prNum, "squash", true, l.cfg.DryRun)
						if err != nil {
							l.Log(fmt.Sprintf("⚠️ Could not enable auto-merge: %v", err), colors.Yellow)
						} else {
							if out != "" {
								l.Log(fmt.Sprintf("Auto-merge enabled: %s", out), colors.Green)
							} else {
								l.Log("Auto-merge enabled.", colors.Green)
							}
						}
					}
				}
			}
		}
	}
}

// ParseVerifierUpdates extracts the verifier's verdict from its output. If both
// PASS and FAIL appear it returns FAIL (conservative). No verdict line → UNKNOWN.
func (l *MiddleManagerLoop) ParseVerifierUpdates(stdout string) string {
	reVerdict := regexp.MustCompile(`(?i)VERDICT:\s*(PASS|FAIL)`)
	matches := reVerdict.FindAllStringSubmatch(stdout, -1)
	verdict := "UNKNOWN"
	for _, m := range matches {
		v := strings.ToUpper(m[1])
		if v == "FAIL" {
			return "FAIL" // any explicit FAIL wins
		}
		verdict = v // PASS
	}
	return verdict
}

func (l *MiddleManagerLoop) RunOnce(iteration int, issueData map[string]string) bool {
	l.Log(fmt.Sprintf("\n🔄 ITERATION %d branch: %s", iteration, l.branchName()), colors.Cyan+colors.Bold)

	if !l.cfg.StreamOutput {
		tui.NotifyTUIStatus(iteration, "discover", "", "running", l.branchName(), time.Since(l.startTime))
	}

	verifierPassed := true
	verifierStdout := ""

	activeSteps := l.cfg.ActiveSteps()

	for _, step := range activeSteps {
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

		// If the operator queued a note, make its application visible: announce
		// it at the boundary and pause briefly so they see it land on this step
		// (RunStep then folds it into the prompt). Skipped in stream mode.
		if note := tui.PendingInterjection(); note != "" && !l.cfg.StreamOutput {
			shown := note
			if len(shown) > 80 {
				shown = shown[:79] + "…"
			}
			l.Log(fmt.Sprintf("📨 Applying your queued note to the %s step → %q", strings.ToUpper(step), shown), colors.Magenta+colors.Bold)
			time.Sleep(1500 * time.Millisecond)
		}

		stdout, exitCode, err := l.RunStep(step, iteration, issueData)
		if err != nil {
			l.Log(fmt.Sprintf("Step %s failed with error: %v", step, err), colors.Red)
			existingErr := l.ReadText(l.errorLogPath, "")
			l.WriteText(l.errorLogPath, fmt.Sprintf("Step %s failed with error: %v\n\n%s", step, err, existingErr))

			errStr := strings.ToLower(err.Error())
			stepAgent := sc.Agent
			if strings.Contains(errStr, "auth") || strings.Contains(errStr, "login") || strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "api key") {
				authMsg := []string{
					"----------------------------------------------------------------",
					fmt.Sprintf("🔑 %s may not be authenticated.", strings.ToUpper(stepAgent)),
					"----------------------------------------------------------------",
					"middle-manager runs each agent as its own CLI. Make sure that CLI",
					"is logged in / has credentials when run directly, e.g.:",
					fmt.Sprintf("   %s   (then complete its login flow)", stepAgent),
					"middle-manager uses whatever auth the agent CLI already has —",
					"OAuth logins and API keys both work, no extra keys required.",
					"----------------------------------------------------------------",
				}
				for _, line := range authMsg {
					l.Log(line, colors.Red)
				}
			}
		}

		if step == "verify" {
			verifierStdout = stdout
			if exitCode != 0 {
				l.Log("❌ Verifier reported CLI error/failure", colors.Red)
				verifierPassed = false
			}
		}

		// Interactive mode: pause after each step so the operator can inspect /
		// interject before the next one runs.
		if l.cfg.Interactive && !l.cfg.StreamOutput {
			l.Log("⏸️  Interactive pause — type /resume (or press p) in the input box to continue.", colors.Yellow)
			tui.RequestPause()
			l.checkTUIPause()
		}
	}

	if contains(activeSteps, "verify") && verifierPassed {
		verdict := l.ParseVerifierUpdates(verifierStdout)
		l.Log(fmt.Sprintf("🔍 Verifier Verdict: %s", verdict), colors.Green)
		// Fail closed: only an explicit PASS ships. A FAIL or a missing/garbled
		// verdict (UNKNOWN) loops back rather than silently committing unverified
		// work. The iteration cap + stall detector bound the retries.
		if verdict != "PASS" {
			verifierPassed = false
			if verdict == "FAIL" {
				l.Log("⚠️ Verifier reported FAIL — will loop back", colors.Yellow)
			} else {
				l.Log("⚠️ No explicit 'VERDICT: PASS' from verifier — failing closed, will loop back", colors.Yellow)
			}
			existingErr := l.ReadText(l.errorLogPath, "")
			header := fmt.Sprintf("\n=== VERIFIER FEEDBACK (Iteration %d, verdict=%s) ===\n", iteration, verdict)
			l.WriteText(l.errorLogPath, header+verifierStdout+"\n"+existingErr)
		}
	}

	if !verifierPassed {
		// No-progress detection: if the working diff AND the verifier feedback are
		// identical to the previous failing iteration, the loop is spinning. Bail
		// instead of burning iterations (and tokens) on a fixed point.
		sig := l.iterationSignature(verifierStdout)
		if sig != "" && sig == l.lastSignature {
			l.stallCount++
			if l.stallCount >= 1 {
				l.stalled = true
				l.stallReason = "no progress — working tree and verifier feedback unchanged across iterations"
				l.Log("🛑 No progress detected (identical diff + verifier feedback). Stopping loop.", colors.Red+colors.Bold)
				return false
			}
		} else {
			l.stallCount = 0
		}
		l.lastSignature = sig
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

	l.success = true
	return false // We succeeded, exit the loop!
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

	issueData, issueErr := gitops.FetchIssue(l.cfg.Repo, l.cfg.Issue)
	if issueErr != nil && l.cfg.Issue != "" {
		l.Log(fmt.Sprintf("⚠️ Could not fetch issue %q: %v — proceeding with the issue number only", l.cfg.Issue, issueErr), colors.Yellow)
	}
	// In issue/queue mode the operator gives no --mission; derive an effective one
	// from the issue so the PR title, commit message, summary, and the {mission}
	// rendered into every step prompt are meaningful instead of blank.
	if l.cfg.Mission == "" && issueData["title"] != "" {
		if issueData["number"] != "" {
			l.cfg.Mission = fmt.Sprintf("#%s %s", issueData["number"], issueData["title"])
		} else {
			l.cfg.Mission = issueData["title"]
		}
	}

	// Start resources tracking goroutine
	if !l.cfg.StreamOutput {
		go l.trackResourcesBackground()
	}

	iteration := l.ReadIteration()
	ran := 0

	for i := 0; i < l.cfg.MaxIterations; i++ {
		if !l.RunOnce(iteration, issueData) {
			if l.success {
				l.Log("Loop finished successfully.", colors.Green)
				return &LoopResult{Success: true, Reason: "completed successfully", PRURL: l.lastPRURL, Iterations: ran}, nil
			}
			if l.stalled {
				return &LoopResult{Success: false, Reason: l.stallReason, PRURL: l.lastPRURL, Iterations: ran}, nil
			}
			return &LoopResult{Success: false, Reason: "Stopped by user", PRURL: l.lastPRURL, Iterations: ran}, nil
		}

		ran++
		iteration++
		l.WriteIteration(iteration)
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
		gitops.CheckoutDefaultBranch(l.cfg.Repo, l.cfg.BaseBranch)
		// In issue/queue mode every issue gets its own mm/issue-<n> branch and a
		// queue drains many of them in one run (each with Fresh=true). Deleting by
		// prefix here would nuke sibling issues' branches mid-drain, so only sweep
		// stale branches for the single-mission feature/repair flows.
		if l.cfg.Mode != "issue" && l.cfg.Mode != "queue" {
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
}

// iterationSignature fingerprints the current working tree diff plus the
// verifier's feedback, so the loop can detect when it is no longer making
// progress (same diff + same critique twice running).
func (l *MiddleManagerLoop) iterationSignature(verifierStdout string) string {
	diff := ""
	if gitops.RepoIsGit(l.cfg.Repo) {
		diff, _, _, _ = gitops.RunGit(l.cfg.Repo, "diff", "HEAD")
	}
	combined := diff + "\x00" + strings.TrimSpace(verifierStdout)
	if strings.TrimSpace(combined) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(sum[:])
}

func (l *MiddleManagerLoop) branchName() string {
	if gitops.RepoIsGit(l.cfg.Repo) {
		b, _ := gitops.CurrentBranch(l.cfg.Repo)
		return b
	}
	return "non-git"
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

func (l *MiddleManagerLoop) NotifyStatus(state string) {
	tui.NotifyTUIStatus(l.ReadIteration(), "", "", state, l.branchName(), time.Since(l.startTime))
}
