package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	// failReason records why the loop stopped without success (e.g. commit/PR
	// failure) so RunUntilComplete reports it instead of a generic message.
	failReason string

	// prefetchedIssue, when set by the queue runner, carries the issue's
	// title/body already fetched by ListIssues so the loop need not re-fetch it
	// (avoiding a per-issue gh failure window mid-drain).
	prefetchedIssue map[string]string

	// iterationAgent holds the agent rolled for the current iteration when any
	// step is configured as "random". Resolved ONCE per iteration and reused by
	// every random step that iteration, so one agent owns the whole issue attempt
	// (the "new random agent per iteration" contract) rather than thrashing
	// between agents mid-issue.
	iterationAgent string

	// verifierAgent is the separate roll for the verify step when
	// DistinctVerifier is on — the critic must not grade its own homework.
	verifierAgent string

	// lastStepAgent remembers which agent last ran each step, so an escalated
	// agent's prompt can name its predecessor in the handoff notice.
	lastStepAgent map[string]string

	// failedIters counts iterations whose verdict failed; it drives each step's
	// escalation ladder (tier = failedIters / EscalateAfter, capped per step).
	failedIters int
}

// SetPrefetchedIssue lets the queue runner hand the loop the issue metadata it
// already fetched, so RunUntilComplete skips the redundant per-issue FetchIssue.
func (l *MiddleManagerLoop) SetPrefetchedIssue(data map[string]string) {
	l.prefetchedIssue = data
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
		lastStepAgent: make(map[string]string),
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
	// Route to the TUI only when one actually owns the terminal; otherwise
	// (stream mode, --dry-run, tests) print to stdout — a dry run that shows
	// nothing is worse than no dry run at all.
	if l.cfg.StreamOutput || tui.GlobalProgram == nil {
		fmt.Println(msg)
	} else {
		tui.NotifyTUIUpdate(msg+"\n", false)
	}
}

// EnsureStateExcluded keeps orchestrator state invisible to the repo's git
// without ever editing tracked files. The default state dir lives outside the
// repo entirely, so there is usually nothing to do; only a custom --state-dir
// placed inside the working tree needs excluding, and that entry goes to
// .git/info/exclude (local-only, never committed) — NOT to the repo's
// .gitignore, which mm used to append to and thereby polluted diffs.
func (l *MiddleManagerLoop) EnsureStateExcluded() {
	if !gitops.RepoIsGit(l.cfg.Repo) {
		return
	}
	state, err := filepath.Abs(l.cfg.StatePath())
	if err != nil {
		return
	}
	repo, err := filepath.Abs(l.cfg.Repo)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(repo, state)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return // state dir is outside the repo — nothing can leak into git
	}
	if rel == "." {
		return // state dir IS the repo root; excluding "/" would hide everything
	}
	pattern := "/" + filepath.ToSlash(rel) + "/"
	// Resolve info/exclude via git so worktrees (where .git is a file) work too.
	excludePath, _, code, err := gitops.RunGit(l.cfg.Repo, "rev-parse", "--git-path", "info/exclude")
	if err != nil || code != 0 || excludePath == "" {
		return
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(repo, excludePath)
	}
	if b, err := os.ReadFile(excludePath); err == nil && strings.Contains(string(b), pattern) {
		return
	}
	_ = os.MkdirAll(filepath.Dir(excludePath), 0755)
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString("\n# middle-manager state directory (local-only exclude)\n" + pattern + "\n")
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
	} else if step == "discover" && l.cfg.Mode == "repair" {
		// Repair has no issue to analyze — the auditor prompt hunts for the
		// highest-value defect itself instead of staring at empty issue fields.
		templateName = "discover_repair"
	} else if sc.PromptFile != "" {
		templateName = sc.PromptFile
	}

	// Custom prompt overrides may live in the state root (outside the repo) or,
	// deliberately committed, in <repo>/.middle-manager/prompts.
	stateRoot := filepath.Dir(l.cfg.NotesPath())
	template := prompts.LoadPrompt(l.cfg.Repo, stateRoot, strings.TrimSuffix(templateName, ".md"))
	if step == "execute" && l.cfg.FixUnrelatedTests {
		ruleAddition := "\n**Additional rule — fix unrelated test failures:** If the test suite is failing due to unrelated test failures or environment-specific issues that block verification of your changes, you are allowed and encouraged to modify the test files or unrelated files directly to fix the test failures so that they pass.\n"
		template += ruleAddition
	}

	// Cross-step handoffs: the planner's report feeds the programmer, the
	// programmer's report feeds the verifier, and every step sees the real git
	// change surface instead of trusting agent summaries. The programmer/solo
	// agent needs it too: on retries (and especially escalations) the tree
	// still holds the previous attempt's uncommitted work, and an agent that
	// can't see it will duplicate or fight it.
	discoverOutput := l.ReadText(filepath.Join(l.state, "discover_output.txt"), "")
	executeOutput := l.ReadText(filepath.Join(l.state, "execute_output.txt"), "")
	diffSummary := gitops.DiffSummary(l.cfg.Repo)

	ctx := prompts.BuildContext(prompts.Context{
		Repo:           l.cfg.Repo,
		Issue:          l.cfg.Issue,
		DiscoverOutput: discoverOutput,
		ExecuteOutput:  executeOutput,
		AgentMemory:    l.AgentMemory(),
		TestOutput:     l.ReadText(l.verifyLogPath, ""),
		ErrorLog:       l.ReadText(l.errorLogPath, ""),
		DiffSummary:    diffSummary,
		Notes:          l.ReadText(l.cfg.NotesPath(), ""),
		NotesFile:      l.cfg.NotesPath(),
		StateDir:       l.state,
		Iteration:      iteration,
		Mission:        l.cfg.Mission,
	})

	ctx["issue_title"] = issueData["title"]
	ctx["issue_body"] = issueData["body"]
	ctx["issue_number"] = issueData["number"]

	return prompts.RenderPrompt(template, ctx)
}

// tierFor returns the step's current escalation rung: 0 is the configured
// base agent, 1..len(Escalate) index into the ladder. Every EscalateAfter
// failed iterations advance one rung, capped at the top of this step's ladder.
func (l *MiddleManagerLoop) tierFor(sc *config.StepConfig) int {
	if sc == nil || len(sc.Escalate) == 0 {
		return 0
	}
	after := l.cfg.EscalateAfter
	if after < 1 {
		after = 1
	}
	tier := l.failedIters / after
	if tier > len(sc.Escalate) {
		tier = len(sc.Escalate)
	}
	return tier
}

// escalationHeadroom reports whether any active step still has a higher rung
// to climb to — used by the stall detector to escalate instead of giving up.
func (l *MiddleManagerLoop) escalationHeadroom() bool {
	for _, step := range l.cfg.ActiveSteps() {
		sc := l.cfg.StepFor(step)
		if sc != nil && l.tierFor(sc) < len(sc.Escalate) {
			return true
		}
	}
	return false
}

// bumpTier force-advances every ladder by one rung (each capped individually)
// by jumping failedIters to the next escalation boundary.
func (l *MiddleManagerLoop) bumpTier() {
	after := l.cfg.EscalateAfter
	if after < 1 {
		after = 1
	}
	l.failedIters = ((l.failedIters / after) + 1) * after
}

// resolveStepAgentModel maps a step to the concrete agent+model to run,
// honoring the escalation tier, the per-iteration random roll, and the
// distinct-verifier policy. Returns ("", "", tier) when nothing is installed.
func (l *MiddleManagerLoop) resolveStepAgentModel(step string, sc *config.StepConfig) (string, string, int) {
	tier := l.tierFor(sc)
	agent := sc.Agent
	model := sc.Model
	if tier > 0 {
		ref := sc.Escalate[tier-1]
		agent, model = ref.Agent, ref.Model
	}
	if agents.IsRandom(agent) {
		agent = l.iterationAgent
		model = "" // a rolled agent has no business inheriting the sentinel's model
	}
	if step == "verify" && l.cfg.DistinctVerifier {
		if swapped, ok := l.distinctVerifier(agent); ok {
			agent = swapped
			model = "" // the configured model belonged to the displaced agent
		}
	}
	return agent, model, tier
}

// distinctVerifier returns a different installed agent when the verifier would
// otherwise be the same agent that executed this iteration's change. Research
// on verifier reliability is consistent: an independent critic (fresh process,
// different model) catches failures a self-review rubber-stamps.
func (l *MiddleManagerLoop) distinctVerifier(verifyAgent string) (string, bool) {
	execSC := l.cfg.StepFor("execute")
	execAgent := execSC.Agent
	if tier := l.tierFor(execSC); tier > 0 {
		execAgent = execSC.Escalate[tier-1].Agent
	}
	if agents.IsRandom(execAgent) {
		execAgent = l.iterationAgent
	}
	if verifyAgent == "" || execAgent == "" || verifyAgent != execAgent {
		return verifyAgent, false
	}
	if l.verifierAgent != "" && l.verifierAgent != execAgent {
		return l.verifierAgent, true
	}
	// Prefer the operator's own strength ranking, then the verify-step priority
	// order, then any other installed agent (covers custom agents in neither).
	candidates := append(append([]string{}, l.cfg.StrengthOrder...), agents.StepAgentPriority["verify"]...)
	for _, name := range candidates {
		if name != execAgent && agents.AgentAvailable(name, l.cfg.BinaryOverrides[name]) {
			l.verifierAgent = name
			return name, true
		}
	}
	for _, name := range agents.AvailableAgents(l.cfg.BinaryOverrides) {
		if name != execAgent {
			l.verifierAgent = name
			return name, true
		}
	}
	return verifyAgent, false // only one agent installed — keep it, better than nothing
}

func (l *MiddleManagerLoop) RunStep(step string, iteration int, issueData map[string]string) (string, int, error) {
	sc := l.cfg.StepFor(step)
	if !sc.Enabled {
		l.Log(fmt.Sprintf("Skipping disabled step: %s", step), "")
		return "", 0, nil
	}

	agent, model, tier := l.resolveStepAgentModel(step, sc)
	if agent == "" {
		// Configured "random" but nothing is installed to roll from.
		l.Log(fmt.Sprintf("No installed agents available to run step %s — install one of: %s", step, strings.Join(agents.AgentNames, ", ")), colors.Red)
		return "", 127, nil
	}
	if tier > 0 {
		l.Log(fmt.Sprintf("⬆ Escalation tier %d/%d for %s → %s %s", tier, len(sc.Escalate), strings.ToUpper(step), strings.ToUpper(agent), model), colors.Magenta+colors.Bold)
	}
	binary := l.cfg.BinaryOverrides[agent]
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
	prompt += escalationNotice(step, tier, len(sc.Escalate), agent, l.lastStepAgent[step])
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

	// Per-step wall-clock bound: a hung CLI must never stall the factory. The
	// per-step setting overrides the global default; negative disables.
	timeout := time.Duration(0)
	switch {
	case sc.TimeoutMinutes > 0:
		timeout = time.Duration(sc.TimeoutMinutes) * time.Minute
	case sc.TimeoutMinutes == 0 && l.cfg.StepTimeoutMinutes > 0:
		timeout = time.Duration(l.cfg.StepTimeoutMinutes) * time.Minute
	}

	// Failure taxonomy: an agent CLI that exits nonzero is an INFRASTRUCTURE
	// failure (crash, rate limit, auth blip) and gets one same-tier retry —
	// escalation budget is reserved for TASK failures (a verifier FAIL), which
	// the iteration loop handles. Timeouts are not retried: a step that burned
	// its full window would likely burn another.
	var (
		stdout   string
		exitCode int
	)
	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		runCtx := l.ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			runCtx, cancel = context.WithTimeout(l.ctx, timeout)
		}
		start := time.Now()
		stdout, exitCode, err = agents.RunAgent(runCtx, run, l.cfg.DryRun, step, onUpdate)
		timedOut := runCtx.Err() == context.DeadlineExceeded && l.ctx.Err() == nil
		if cancel != nil {
			cancel()
		}
		l.appendLedger(map[string]interface{}{
			"type": "step", "iteration": iteration, "step": step,
			"agent": agent, "model": model, "tier": tier, "attempt": attempt,
			"duration_s": round1(time.Since(start).Seconds()),
			"exit_code":  exitCode, "timed_out": timedOut,
			"output_bytes": len(stdout),
		})
		if timedOut {
			exitCode = 124
			err = fmt.Errorf("step %s (%s) timed out after %s", step, agent, timeout)
			l.Log(fmt.Sprintf("⏱ Step %s (%s) hit its %s timeout — treating as a failed attempt.", strings.ToUpper(step), strings.ToUpper(agent), timeout), colors.Red)
			break
		}
		if exitCode == 0 || l.ctx.Err() != nil || l.cfg.DryRun {
			break
		}
		if attempt < maxAttempts {
			l.Log(fmt.Sprintf("♻️  Step %s (%s) exited %d — retrying once (infrastructure failure, same tier).", strings.ToUpper(step), strings.ToUpper(agent), exitCode), colors.Yellow)
		}
	}

	outputFile := filepath.Join(l.state, fmt.Sprintf("%s_output.txt", step))
	l.WriteText(outputFile, stdout)
	l.lastStepAgent[step] = agent

	if exitCode == 0 {
		l.Log(fmt.Sprintf("✅ Step %s (%s) finished successfully (exit code 0).", strings.ToUpper(step), strings.ToUpper(agent)), colors.Green)
	} else {
		l.Log(fmt.Sprintf("❌ Step %s (%s) failed (exit code %d).", strings.ToUpper(step), strings.ToUpper(agent), exitCode), colors.Red)
		// Capture the failing output's tail for the next iteration's prompts —
		// a retry must add information, never repeat blind.
		if strings.TrimSpace(stdout) != "" && step != "verify" && step != "solo" {
			existingErr := l.ReadText(l.errorLogPath, "")
			header := fmt.Sprintf("=== Step %s (%s) exited %d (iteration %d) — output tail ===\n", step, agent, exitCode, iteration)
			l.WriteText(l.errorLogPath, header+prompts.Clip(stdout, 4000, true)+"\n"+existingErr)
		}
	}

	return stdout, exitCode, err
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

// escalationNotice is the handoff banner appended to an escalated
// executor/solo prompt. The escalated agent inherits its predecessor's
// uncommitted work in the tree, so it must be told explicitly that it IS an
// escalation, who failed before it, and that reviewing (not redoing) that
// work is the job. Other steps don't get the banner — the verifier must stay
// an unbiased auditor and the planner reads the tree fresh anyway.
func escalationNotice(step string, tier, ladderLen int, agent, prevAgent string) string {
	if tier <= 0 || (step != "execute" && step != "solo") {
		return ""
	}
	prev := "a previous agent"
	if prevAgent != "" && prevAgent != agent {
		prev = strings.ToUpper(prevAgent)
	}
	return fmt.Sprintf(`

## Escalation notice — you were brought in because earlier attempts failed
You are the tier-%d escalation agent (%s, rung %d of %d) on this task. %s
already attempted it and failed verification; their UNCOMMITTED work may still
be in the working tree (see the change summary above and `+"`git status`"+`).
Review that work critically before writing anything: keep what is correct,
rewrite or revert what is not, and address the verifier feedback directly —
do not repeat the failed approach, and do not duplicate changes that already
exist in the tree.`,
		tier, strings.ToUpper(agent), tier, ladderLen, prev)
}

// appendLedger writes one JSONL record to the run ledger (<state>/ledger.jsonl):
// per-attempt step telemetry, per-iteration verdicts, and the run outcome. Plain
// headless CLIs don't report token spend uniformly, so wall-clock duration per
// agent/tier is the cost proxy; `mm status` aggregates it.
func (l *MiddleManagerLoop) appendLedger(entry map[string]interface{}) {
	entry["ts"] = time.Now().UTC().Format(time.RFC3339)
	entry["run_id"] = l.runID
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(l.state, "ledger.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// MaybeCommitAndPR commits the verified work, pushes the branch, and opens the
// PR. It returns a non-nil error when an expected step actually fails (commit,
// push, or PR creation) so the caller does NOT report success — critical for the
// autonomous queue, which must not close an issue when no PR was opened.
func (l *MiddleManagerLoop) MaybeCommitAndPR(iteration int, issueData map[string]string) error {
	commitMsg := func() string {
		subject := l.TopPlanItem()
		if subject == "" && issueData["number"] != "" {
			subject = "issue #" + issueData["number"]
		}
		msg := fmt.Sprintf("middle-manager: iteration %d — %s", iteration, subject)
		if len(msg) > 72 {
			msg = msg[:72]
		}
		return msg
	}

	// Solo mode (issue #2): one agent already did everything and self-certified
	// with a VERDICT. mm still owns git deterministically — it commits the work,
	// opens exactly one PR, enables auto-merge, then (WaitForMerge) blocks until
	// the PR actually lands so a queue serializes and never conflicts. Keyed on
	// IsSolo() (Solo || Steps==1) so a Steps==1 config never falls through to the
	// 3-step path and opens no PR.
	if l.cfg.IsSolo() {
		if gitops.RepoIsGit(l.cfg.Repo) && gitops.HasChanges(l.cfg.Repo) && !l.cfg.DryRun {
			committed, err := gitops.CommitAllWithError(l.cfg.Repo, commitMsg())
			if err != nil {
				return fmt.Errorf("solo commit failed: %w", err)
			}
			if committed {
				l.Log("Committed solo agent's verified work", colors.Green)
			}
		}
		if !gitops.RepoIsGit(l.cfg.Repo) {
			return nil
		}
		if l.cfg.NoPR {
			branch, _ := gitops.CurrentBranch(l.cfg.Repo)
			if err := gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun); err != nil {
				return fmt.Errorf("push of branch %q failed: %w", branch, err)
			}
			return nil
		}
		return l.pushAndOpenPR(iteration, issueData)
	}

	if l.cfg.Steps < 4 || !l.cfg.Commit.Enabled {
		if gitops.HasChanges(l.cfg.Repo) && !l.cfg.DryRun {
			committed, err := gitops.CommitAllWithError(l.cfg.Repo, commitMsg())
			if err != nil {
				return fmt.Errorf("commit failed: %w", err)
			}
			if committed {
				l.Log("Committed changes (3-step mode, no PR agent)", colors.Green)
				// NoPR keeps the commit purely local — used by worktree mode, where
				// the collapse step merges these local branches and pushes only the
				// single integration branch (pushing each issue branch would clutter
				// the remote for no benefit).
				if gitops.RepoIsGit(l.cfg.Repo) && !l.cfg.NoPR {
					branch, _ := gitops.CurrentBranch(l.cfg.Repo)
					if err := gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun); err != nil {
						return fmt.Errorf("push of branch %q failed: %w", branch, err)
					}
					l.Log(fmt.Sprintf("Pushed branch %q to origin", branch), colors.Green)
				}
			}
		}
		return nil
	}

	// 4-step: the commit agent updates repo memory and commits. It is explicitly
	// told NOT to push or open a PR — the orchestrator does that deterministically
	// below so there's exactly one PR creator (no agent/orchestrator collision).
	_, exitCode, err := l.RunStep("commit", iteration, issueData)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("commit step failed (exit %d): %v", exitCode, err)
	}

	if !gitops.RepoIsGit(l.cfg.Repo) {
		return nil
	}

	// Safety net: if the commit agent left the verified work uncommitted, commit
	// it ourselves so a flaky agent can't silently drop a green change.
	if gitops.HasChanges(l.cfg.Repo) && !l.cfg.DryRun {
		l.Log("Commit agent left changes uncommitted — committing them to preserve verified work", colors.Yellow)
		if _, err := gitops.CommitAllWithError(l.cfg.Repo, commitMsg()); err != nil {
			return fmt.Errorf("fallback commit failed: %w", err)
		}
	}

	if l.cfg.NoPR {
		return nil
	}

	return l.pushAndOpenPR(iteration, issueData)
}

// pushAndOpenPR pushes the current branch, opens exactly one PR linking the
// issue, enables auto-merge, and — when WaitForMerge is set — blocks until the
// PR actually merges (bounded by MergeTimeoutMinutes). A wait that ends without
// a merge returns an error so the caller fails closed and a queue stops rather
// than starting the next issue off a base the PR never landed on.
func (l *MiddleManagerLoop) pushAndOpenPR(iteration int, issueData map[string]string) error {
	branch, _ := gitops.CurrentBranch(l.cfg.Repo)
	if err := gitops.PushBranch(l.cfg.Repo, branch, l.cfg.DryRun); err != nil {
		return fmt.Errorf("push of branch %q failed: %w", branch, err)
	}

	title := fmt.Sprintf("middle-manager: %s", l.cfg.Mission)
	if len(title) > 60 {
		title = title[:60]
	}
	body := prBody(iteration, !l.cfg.NoMerge)
	baseBranch := l.cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = gitops.DetectBaseBranch(l.cfg.Repo)
	}
	prURL, err := gitops.CreatePR(l.cfg.Repo, title, body, branch, baseBranch, issueData["number"], l.cfg.DryRun)
	if err != nil {
		return fmt.Errorf("PR creation failed (branch %q is pushed; open the PR manually): %w", branch, err)
	}
	if prURL == "" {
		return nil // dry-run
	}
	l.lastPRURL = prURL
	l.Log(fmt.Sprintf("PR created: %s", prURL), colors.Green)

	prNum := prNumberFromURL(prURL)
	autoMergeArmed := false
	if !l.cfg.NoMerge && prNum > 0 {
		l.Log(fmt.Sprintf("Enabling GitHub auto-merge on PR #%d...", prNum), colors.Cyan)
		out, err := gitops.EnableAutoMerge(l.cfg.Repo, prNum, "squash", true, l.cfg.DryRun)
		if err != nil {
			// Typical on repos without branch protection, where GitHub refuses to
			// arm auto-merge. Not a dead end: mm merges deterministically instead.
			l.Log(fmt.Sprintf("⚠️ Could not enable GitHub auto-merge (%v) — falling back to deterministic merge by middle-manager.", err), colors.Yellow)
		} else {
			autoMergeArmed = true
			if out != "" {
				l.Log(fmt.Sprintf("Auto-merge enabled: %s", out), colors.Green)
			} else {
				l.Log("Auto-merge enabled.", colors.Green)
			}
		}
	}

	if l.cfg.WaitForMerge && prNum > 0 && !l.cfg.DryRun {
		timeout := time.Duration(l.cfg.MergeTimeoutMinutes) * time.Minute
		if timeout <= 0 {
			timeout = 60 * time.Minute
		}
		l.Log(fmt.Sprintf("⏳ Waiting for PR #%d to merge (bounded by %s). CI must pass.", prNum, timeout), colors.Cyan)
		merged, reason := gitops.WaitForPRMerge(l.ctx, l.cfg.Repo, prNum, timeout, !l.cfg.NoMerge, func(m string) { l.Log(m, colors.Dim) })
		if merged {
			l.Log(fmt.Sprintf("✅ PR #%d merged.", prNum), colors.Green)
			return nil
		}
		// Don't leave a half-armed auto-merge that could land after we've moved on.
		gitops.DisableAutoMerge(l.cfg.Repo, prNum)
		return fmt.Errorf("PR #%d did not merge: %s", prNum, reason)
	}

	// Merge requested but native auto-merge unavailable and no long wait asked:
	// give the deterministic merge a short, bounded window (checks on a small
	// change usually settle in seconds; a pending CI just leaves the PR open).
	if !l.cfg.NoMerge && !autoMergeArmed && prNum > 0 && !l.cfg.DryRun {
		merged, reason := gitops.WaitForPRMerge(l.ctx, l.cfg.Repo, prNum, 3*time.Minute, true, func(m string) { l.Log(m, colors.Dim) })
		if merged {
			l.Log(fmt.Sprintf("✅ PR #%d merged deterministically.", prNum), colors.Green)
		} else {
			l.Log(fmt.Sprintf("PR #%d left open (%s) — run `mm merge` to sweep it once green.", prNum, reason), colors.Yellow)
		}
	}
	return nil
}

// prNumberFromURL extracts the trailing PR number from a gh PR URL.
func prNumberFromURL(prURL string) int {
	parts := strings.Split(strings.TrimSpace(prURL), "/")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return n
}

// prBody builds the PR description. The merge guidance must match what the loop
// actually does next: when auto-merge is enabled, telling humans "do not merge
// without review" is contradictory — the orchestrator turns on GitHub auto-merge
// moments later. So the note is conditional on the effective merge mode.
func prBody(iteration int, autoMerge bool) string {
	const intro = "Automated PR from middle-manager loop iteration %d, opened after the verifier step passed."
	if autoMerge {
		return fmt.Sprintf(
			intro+"\n\n_Auto-merge is enabled — GitHub will merge this automatically once required status checks pass._",
			iteration,
		)
	}
	return fmt.Sprintf(
		intro+"\n\n**Do not merge without human review.**",
		iteration,
	)
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

	// Roll the random agent for THIS iteration (issue #3). One roll fills every
	// "random" step so a single agent owns the iteration; explicit agents are
	// untouched. Re-rolled each iteration so retries vary the agent.
	l.iterationAgent = ""
	for _, step := range activeSteps {
		if agents.IsRandom(l.cfg.StepFor(step).Agent) {
			l.iterationAgent = agents.PickRandomAgent(l.cfg.BinaryOverrides)
			if l.iterationAgent == "" && l.cfg.DryRun {
				l.iterationAgent = agents.AgentNames[0] // so dry-run still prints a command
			}
			if l.iterationAgent != "" {
				l.Log(fmt.Sprintf("🎲 random → %s for iteration %d", l.iterationAgent, iteration), colors.Magenta+colors.Bold)
			}
			break
		}
	}

	for _, step := range activeSteps {
		if step == "commit" {
			continue // Handled separately
		}

		sc := l.cfg.StepFor(step)
		shownAgent, _, _ := l.resolveStepAgentModel(step, sc)
		if shownAgent == "" {
			shownAgent = sc.Agent // nothing rolled (no agents installed); show the sentinel
		}
		l.Log(fmt.Sprintf("\n[Step: %s] Starting step with agent '%s'...", strings.ToUpper(step), strings.ToUpper(shownAgent)), colors.Cyan)

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

		// Both the dedicated verifier and the solo agent emit the VERDICT line that
		// gates the commit; capture whichever ran.
		if step == "verify" || step == "solo" {
			verifierStdout = stdout
			if exitCode != 0 {
				l.Log("❌ Agent reported CLI error/failure on the verdict step", colors.Red)
				verifierPassed = false
			}
		}

		// Mechanical gate: an execute step that crashed AND left no working-tree
		// changes has produced nothing to verify — skip straight to the next
		// iteration instead of paying for a verifier run that can only FAIL.
		if step == "execute" && exitCode != 0 && gitops.RepoIsGit(l.cfg.Repo) && !gitops.HasChanges(l.cfg.Repo) && !l.cfg.DryRun {
			l.Log("⏭ Execute produced no changes and exited nonzero — skipping verify, looping back.", colors.Yellow)
			verifierPassed = false
			break
		}

		// Interactive mode: pause after each step so the operator can inspect /
		// interject before the next one runs.
		if l.cfg.Interactive && !l.cfg.StreamOutput {
			l.Log("⏸️  Interactive pause — type /resume in the input box (then Enter) to continue.", colors.Yellow)
			tui.RequestPause()
			l.checkTUIPause()
		}
	}

	// Persist the verifier's report every iteration (pass or fail) so the next
	// iteration's prompts can reference it as {test_output} — previously this
	// file was read but never written, so that context was always empty.
	if strings.TrimSpace(verifierStdout) != "" {
		l.WriteText(l.verifyLogPath, fmt.Sprintf("=== Verifier report (iteration %d) ===\n%s", iteration, verifierStdout))
	}

	verdict := ""
	if (contains(activeSteps, "verify") || contains(activeSteps, "solo")) && verifierPassed {
		verdict = l.ParseVerifierUpdates(verifierStdout)
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

	// Deterministic pre-commit gates — a verifier PASS is necessary, not
	// sufficient. Placed before the ledger record so `passed` reflects them.
	if verifierPassed && !l.enforcePreCommitGates() {
		verifierPassed = false
	}

	l.appendLedger(map[string]interface{}{
		"type": "iteration", "iteration": iteration,
		"verdict": verdict, "passed": verifierPassed,
		"agent": l.iterationAgent, "failed_iters": l.failedIters,
	})

	if !verifierPassed {
		// A verified task failure is what advances the escalation ladders — the
		// next iteration's steps may resolve to a stronger agent/model.
		l.failedIters++
		// No-progress detection: if the working diff AND the verifier feedback are
		// identical to the previous failing iteration, the loop is spinning. If a
		// ladder still has headroom, force the next rung instead of giving up —
		// retrying identically is the one guaranteed waste of tokens.
		sig := l.iterationSignature(verifierStdout)
		if sig != "" && sig == l.lastSignature {
			l.stallCount++
			if l.stallCount >= 1 {
				if l.escalationHeadroom() {
					l.bumpTier()
					l.stallCount = 0
					l.lastSignature = "" // demand fresh evidence from the new tier
					l.Log("⬆ No progress at this tier — escalating the ladder instead of stopping.", colors.Magenta+colors.Bold)
					return true
				}
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
	}

	// Only report success if the work was actually committed/pushed/PR'd. Without
	// this gate a failed PR (branch protection, no commits, gh error) would still
	// mark the loop successful — and the queue would then close the issue with no
	// PR opened.
	if err := l.MaybeCommitAndPR(iteration, issueData); err != nil {
		l.Log(fmt.Sprintf("❌ %v", err), colors.Red)
		l.failReason = err.Error()
		return false
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

	// First pass: keep explicitly requested agents if they are installed. A
	// "random" sentinel is preserved as-is (resolved per-iteration at runtime) so
	// the diversification pass below never rewrites it to a concrete agent.
	for _, step := range activeSteps {
		sc := l.cfg.StepFor(step)
		if agents.IsRandom(sc.Agent) {
			assigned[step] = sc.Agent
			continue
		}
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

	l.EnsureStateExcluded()
	l.Log(fmt.Sprintf("State dir: %s", l.state), colors.Dim)

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

	var issueData map[string]string
	if l.prefetchedIssue != nil {
		issueData = l.prefetchedIssue // queue already fetched title/body via ListIssues
	} else {
		var issueErr error
		issueData, issueErr = gitops.FetchIssue(l.cfg.Repo, l.cfg.Issue)
		// In issue mode we cannot work an issue we couldn't load — failing closed
		// beats handing the agent an empty task and then closing the issue.
		if issueErr != nil && isDigit(l.cfg.Issue) {
			return &LoopResult{Success: false, Reason: fmt.Sprintf("could not load issue %s: %v", l.cfg.Issue, issueErr)}, nil
		}
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

	finish := func(res *LoopResult) *LoopResult {
		l.appendLedger(map[string]interface{}{
			"type": "run", "success": res.Success, "reason": res.Reason,
			"iterations": res.Iterations, "pr_url": res.PRURL,
			"duration_s": round1(time.Since(l.startTime).Seconds()),
			"mission":    l.cfg.Mission,
		})
		return res
	}

	for i := 0; i < l.cfg.MaxIterations; i++ {
		// Run-level budget: stop before an iteration that starts past the wall
		// clock cap, with a structured reason instead of an open-ended burn.
		if l.cfg.MaxWallMinutes > 0 && time.Since(l.startTime) > time.Duration(l.cfg.MaxWallMinutes)*time.Minute {
			return finish(&LoopResult{Success: false, Reason: fmt.Sprintf("wall-clock budget exhausted (%d min)", l.cfg.MaxWallMinutes), PRURL: l.lastPRURL, Iterations: ran}), nil
		}

		// Count the iteration when it STARTS: a run that succeeds on its first
		// pass has executed 1 iteration, not 0.
		ran++
		if !l.RunOnce(iteration, issueData) {
			if l.success {
				l.Log("Loop finished successfully.", colors.Green)
				return finish(&LoopResult{Success: true, Reason: "completed successfully", PRURL: l.lastPRURL, Iterations: ran}), nil
			}
			if l.stalled {
				return finish(&LoopResult{Success: false, Reason: l.stallReason, PRURL: l.lastPRURL, Iterations: ran}), nil
			}
			if l.failReason != "" {
				return finish(&LoopResult{Success: false, Reason: l.failReason, PRURL: l.lastPRURL, Iterations: ran}), nil
			}
			return finish(&LoopResult{Success: false, Reason: "Stopped by user", PRURL: l.lastPRURL, Iterations: ran}), nil
		}

		iteration++
		l.WriteIteration(iteration)
	}

	return finish(&LoopResult{Success: false, Reason: fmt.Sprintf("Max iterations (%d) reached", l.cfg.MaxIterations), PRURL: l.lastPRURL, Iterations: ran}), nil
}

func (l *MiddleManagerLoop) ResetLoopState() {
	state := l.cfg.StatePath()
	// Step outputs must be swept too: a stale discover/execute report from a
	// previous mission would otherwise be injected into this run's prompts.
	names := []string{
		"fix_plan.md", "iteration.txt", "error_log.txt", "verify_log.txt", "session.log",
		"discover_prompt.md", "execute_prompt.md", "verify_prompt.md", "commit_prompt.md", "solo_prompt.md",
		"discover_output.txt", "execute_output.txt", "verify_output.txt", "commit_output.txt", "solo_output.txt",
	}
	for _, n := range names {
		_ = os.Remove(filepath.Join(state, n))
	}
	// Deliberately do NOT sweep issues/ — those dirs belong to queue drains,
	// which reset each issue's own state when they run it (per-issue Fresh).
	// Nuking them here made any later `mm quick` destroy the whole drain's
	// ledger history, which is exactly the data `mm status` aggregates to
	// answer "what did that 50-issue drain cost me per agent".

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
				// Only sweep this flow's own loop branches. Never delete mm/issue-*
				// here — those belong to issue/queue runs and a feature/repair run
				// must not nuke in-flight issue work.
				if strings.HasPrefix(b, l.cfg.BranchPrefix+"/loop-") {
					_, _, _, _ = gitops.RunGit(l.cfg.Repo, "branch", "-D", b)
				}
			}
		}
	}
}

// iterationSignature fingerprints the current working tree, so the loop can
// detect when it is no longer making progress. Deliberately NOT the verifier's
// prose: two verifier runs never repeat byte-for-byte, so including their text
// meant the detector almost never fired and stalls burned the full iteration
// budget. If a whole failed iteration leaves the tree byte-identical, the
// executor made no progress — escalate or stop.
func (l *MiddleManagerLoop) iterationSignature(verifierStdout string) string {
	_ = verifierStdout // kept in the signature's call contract for custom-prompt debugging
	diff := ""
	status := ""
	if gitops.RepoIsGit(l.cfg.Repo) {
		diff, _, _, _ = gitops.RunGit(l.cfg.Repo, "diff", "HEAD")
		// `diff HEAD` misses untracked files, so an iteration that only ADDS new
		// files would look identical to the previous one and trip the stall
		// detector; porcelain status covers them.
		status, _, _, _ = gitops.RunGit(l.cfg.Repo, "status", "--porcelain")
	}
	combined := diff + "\x00" + status
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
		// A paused loop must stay abortable: bail out if the operator quit
		// (/quit or Ctrl+C, which cancels l.ctx), otherwise the TUI exits while
		// this goroutine spins forever and wg.Wait() hangs the whole process.
		select {
		case <-l.ctx.Done():
			return
		default:
		}
		if tui.IsTUIQuitting() {
			return
		}
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
