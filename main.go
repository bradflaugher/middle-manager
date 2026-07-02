package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/loop"
	"github.com/bradflaugher/middle-manager/pkg/queue"
	"github.com/bradflaugher/middle-manager/pkg/tui"
)

// version is the build version. It defaults to "dev" for local `go build` and
// is overridden at release time via -ldflags "-X main.version=<datestring>"
// (see .github/workflows/release.yml), so there is no version to bump by hand.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("middle-manager %s\n", version)
			return
		}
	}

	// Parse CLI Arguments
	cmdName, cfg, err := config.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	registerCustomAgents(cfg)

	switch cmdName {
	case "install-path":
		cmdInstallPath()
	case "agents":
		cmdAgents(cfg)
	case "init":
		cmdInit(cfg)
	case "status":
		cmdStatus(cfg)
	case "issues":
		cmdIssues(cfg)
	case "run", "quick":
		os.Exit(cmdRun(cfg))
	case "seed":
		os.Exit(loop.RunSeed(cfg))
	case "models":
		cmdModels(cfg)
	case "merge":
		cmdMerge(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmdName)
		os.Exit(1)
	}
}

// registerCustomAgents merges operator-declared agent CLIs (config key
// "agents") into the roster before any command runs, so custom agents show up
// in `mm agents`, the wizard's pickers, random rolls, and escalation ladders
// exactly like built-ins. Names are sorted so the roster order is stable.
func registerCustomAgents(cfg *config.LoopConfig) {
	if len(cfg.CustomAgents) == 0 {
		return
	}
	names := make([]string, 0, len(cfg.CustomAgents))
	for name := range cfg.CustomAgents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		def := cfg.CustomAgents[name]
		notes := def.Notes
		if notes == "" {
			notes = "custom agent (from config)"
		}
		spec := agents.AgentSpec{
			Binary:     def.Binary,
			Subcommand: def.Subcommand,
			PrintFlag:  def.PrintFlag,
			YoloFlags:  def.YoloFlags,
			ModelFlag:  def.ModelFlag,
			CwdFlag:    def.CwdFlag,
			ExtraArgs:  def.ExtraArgs,
			ModelsArgs: def.ModelsArgs,
			Notes:      notes,
		}
		if err := agents.RegisterAgent(name, spec); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping custom agent %q: %v\n", name, err)
		}
	}
}

func cmdInstallPath() {
	home, _ := os.UserHomeDir()
	binDir := filepath.Join(home, ".local", "bin")
	installDir := filepath.Join(home, ".local", "share", "middle-manager")
	fmt.Printf("export PATH=\"%s:$PATH\"\n", binDir)
	fmt.Printf("# mm installed at %s\n", installDir)
}

func cmdAgents(cfg *config.LoopConfig) {
	fmt.Println(tui.RenderBanner(version))
	fmt.Println()
	rows := agents.ListAgentsStatus(cfg.BinaryOverrides)
	fmt.Println(colors.Colored(fmt.Sprintf("%-10s %-10s %s", "AGENT", "AVAILABLE", "BINARY"), colors.Cyan+colors.Bold))
	fmt.Println(colors.Colored(strings.Repeat("-", 72), colors.Cyan))
	for _, row := range rows {
		agentPad := colors.Colored(fmt.Sprintf("%-10s", row["agent"]), colors.Bold)
		availColor := colors.Red
		if row["available"] == "yes" {
			availColor = colors.Green
		}
		availPad := colors.Colored(fmt.Sprintf("%-10s", row["available"]), availColor)
		fmt.Printf("%s %s %s\n", agentPad, availPad, row["binary"])
		if row["notes"] != "" {
			fmt.Println(colors.Colored("           "+row["notes"], colors.Yellow))
		}
	}
}

// cmdModels lists each installed agent's available models via the CLI's own
// listing command (`mm models`, or `mm models opencode` for one agent). With
// --check it also probes whether each CLI actually honors its model flag —
// automating the sanity check the playbook tells operators to run by hand.
func cmdModels(cfg *config.LoopConfig) {
	only := strings.TrimSpace(cfg.Mission) // `mm models opencode` lands here
	names := agents.AvailableAgents(cfg.BinaryOverrides)
	if only != "" {
		names = []string{only}
	}
	if len(names) == 0 {
		fmt.Println("No agent CLIs installed.")
		return
	}
	for _, name := range names {
		fmt.Println(colors.Colored("── "+name+" ", colors.Cyan+colors.Bold))
		out, err := agents.ListModels(name, cfg.BinaryOverrides[name])
		switch {
		case err != nil:
			fmt.Println(colors.Colored("   "+err.Error(), colors.Yellow))
		case out == "":
			fmt.Println(colors.Colored("   (no models reported)", colors.Dim))
		default:
			for _, line := range strings.Split(out, "\n") {
				fmt.Println("   " + line)
			}
		}
		if cfg.CheckModels {
			verdict := agents.CheckModelFlag(name, cfg.BinaryOverrides[name], cfg.Repo)
			style := colors.Green
			if strings.Contains(verdict, "IGNORES") {
				style = colors.Red
			}
			fmt.Println(colors.Colored("   model flag: "+verdict, style))
		}
		fmt.Println()
	}
	if !cfg.CheckModels {
		fmt.Println(colors.Colored("Tip: `mm models --check` probes whether each CLI actually honors its model flag.", colors.Dim))
	}
}

func cmdInit(cfg *config.LoopConfig) {
	dest := filepath.Join(cfg.Repo, "AGENTS.md")
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("exists: %s\n", dest)
	} else {
		_ = os.WriteFile(dest, []byte("# AGENTS.md\n\nRepository memory for middle-manager loops.\nAdd build commands, conventions, and things agents keep forgetting.\n"), 0644)
		fmt.Println(colors.Colored(fmt.Sprintf("created: %s", dest), colors.Green))
	}

	// A GitHub issue template that produces issues agents can actually verify:
	// context with exact files, mechanically checkable acceptance criteria, and
	// explicit verifier instructions. Operator-invoked, skipped if present.
	tmpl := filepath.Join(cfg.Repo, ".github", "ISSUE_TEMPLATE", "mm-task.md")
	if _, err := os.Stat(tmpl); err == nil {
		fmt.Printf("exists: %s\n", tmpl)
	} else if err := os.MkdirAll(filepath.Dir(tmpl), 0755); err == nil {
		_ = os.WriteFile(tmpl, []byte(`---
name: mm task
about: A task scoped for autonomous agents (middle-manager)
labels: mm-todo
---

## What & why
<!-- 2-6 sentences. Name the exact files/modules involved if you know them.
     Keep it small: one agent, one sitting, one clean PR. -->

## Acceptance criteria
<!-- Each criterion should be mechanically checkable — a command to run,
     a fact about a file, a count. "Make X better" is unverifiable. -->
- [ ] ...

## Verifier notes
<!-- The exact check(s) the verifier must run; it FAILS the change otherwise. -->
`), 0644)
		fmt.Println(colors.Colored(fmt.Sprintf("created: %s", tmpl), colors.Green))
	}
	fmt.Printf("State dir: %s\n", cfg.StatePath())
	fmt.Println(colors.Colored("Tip: no backlog yet? `mm seed --count 5` proposes verifiable issues from the codebase.", colors.Cyan))
}

func cmdStatus(cfg *config.LoopConfig) {
	state := cfg.StatePath()
	fmt.Println(colors.Colored("Repo:  "+cfg.Repo, colors.Bold))
	gitStatus := "no"
	if gitops.RepoIsGit(cfg.Repo) {
		gitStatus = "yes"
	}
	fmt.Printf("Git:   %s\n", gitStatus)
	fmt.Printf("Mode:  %s\n", cfg.Mode)
	fmt.Printf("State: %s\n\n", state)

	fmt.Println(colors.Colored("Logs & State Files:", colors.Bold+colors.Cyan))
	for _, name := range []string{"error_log.txt", "verify_log.txt", "iteration.txt", "queue.log", "ledger.jsonl"} {
		p := filepath.Join(state, name)
		status := colors.Colored("missing", colors.Yellow)
		if _, err := os.Stat(p); err == nil {
			status = colors.Colored("exists", colors.Green)
		}
		fmt.Printf("  %-16s: %s\n", name, status)
	}

	printLedgerSummary(state, cfg.SpendRates)
}

// printLedgerSummary aggregates run ledgers into a per-agent scoreboard —
// wall-clock time is the cost proxy for plain headless CLIs — plus the last
// run's outcome, so `mm status` answers "where is my time/money going". A
// queue drain writes one ledger per issue under issues/<n>/, so the whole
// drain (and the base dir's single runs) roll up into one table.
func printLedgerSummary(stateDir string, spendRates map[string]float64) {
	ledgers := []string{filepath.Join(stateDir, "ledger.jsonl")}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "issues")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				ledgers = append(ledgers, filepath.Join(stateDir, "issues", e.Name(), "ledger.jsonl"))
			}
		}
	}

	type agg struct {
		steps    int
		retries  int
		timeouts int
		escals   int
		seconds  float64
	}
	perAgent := map[string]*agg{}
	var lastRun map[string]interface{}
	var lastRunTS string

	for _, path := range ledgers {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			var rec map[string]interface{}
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				continue
			}
			switch rec["type"] {
			case "step":
				agent, _ := rec["agent"].(string)
				if agent == "" {
					continue
				}
				a := perAgent[agent]
				if a == nil {
					a = &agg{}
					perAgent[agent] = a
				}
				a.steps++
				if d, ok := rec["duration_s"].(float64); ok {
					a.seconds += d
				}
				if att, ok := rec["attempt"].(float64); ok && att > 1 {
					a.retries++
				}
				if to, ok := rec["timed_out"].(bool); ok && to {
					a.timeouts++
				}
				if tier, ok := rec["tier"].(float64); ok && tier > 0 {
					a.escals++
				}
			case "run":
				// "Last run" across many ledger files = latest timestamp wins.
				if ts, _ := rec["ts"].(string); ts >= lastRunTS {
					lastRunTS = ts
					lastRun = rec
				}
			}
		}
		f.Close()
	}
	if len(perAgent) == 0 {
		return
	}

	hasRates := len(spendRates) > 0
	fmt.Println()
	fmt.Println(colors.Colored("Ledger (all runs in this state dir):", colors.Bold+colors.Cyan))
	header := fmt.Sprintf("  %-12s %6s %9s %8s %9s %6s", "AGENT", "STEPS", "TIME", "RETRIES", "TIMEOUTS", "ESCAL")
	if hasRates {
		header += fmt.Sprintf(" %11s", "EST SPEND")
	}
	fmt.Println(colors.Colored(header, colors.Cyan))
	names := make([]string, 0, len(perAgent))
	for name := range perAgent {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return perAgent[names[i]].seconds > perAgent[names[j]].seconds })
	total := 0.0
	for _, name := range names {
		a := perAgent[name]
		row := fmt.Sprintf("  %-12s %6d %9s %8d %9d %6d", name, a.steps, (time.Duration(a.seconds) * time.Second).Round(time.Second), a.retries, a.timeouts, a.escals)
		if hasRates {
			spend := (a.seconds / 60) * spendRates[name]
			total += spend
			row += fmt.Sprintf(" %10s", fmt.Sprintf("~$%.2f", spend))
		}
		fmt.Println(row)
	}
	if hasRates {
		fmt.Printf("  %-12s %s\n", "", colors.Colored(fmt.Sprintf("estimated total ~$%.2f (your spend_rates × ledger minutes — calibrate against your provider dashboard)", total), colors.Dim))
	}
	if lastRun != nil {
		outcome := "failed"
		if ok, _ := lastRun["success"].(bool); ok {
			outcome = "succeeded"
		}
		reason, _ := lastRun["reason"].(string)
		fmt.Printf("  Last run: %s (%s)\n", outcome, reason)
	}
}

func cmdIssues(cfg *config.LoopConfig) {
	if cfg.IssueQueue == nil {
		fmt.Fprintln(os.Stderr, "Issue queue requires --label, --author, and/or --mode queue")
		os.Exit(1)
	}
	cfg.Mode = "queue"
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(runQueue(cfg))
}

// runPreflight surfaces missing dependencies BEFORE any agent burns tokens.
// Warnings print and the run proceeds; a fatal problem stops it with exit 1.
func runPreflight(cfg *config.LoopConfig) bool {
	warnings, fatal := loop.Preflight(cfg)
	for _, w := range warnings {
		fmt.Println(colors.Colored("⚠ preflight: "+w, colors.Yellow))
	}
	if fatal != nil {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ preflight: "+fatal.Error(), colors.Red))
		return false
	}
	return true
}

// lockRepo takes the per-repo run lock (skipped for dry runs, which touch
// nothing). Returns a no-op release and false when another run holds it.
func lockRepo(cfg *config.LoopConfig) (func(), bool) {
	if cfg.DryRun {
		return func() {}, true
	}
	release, err := loop.AcquireRepoLock(cfg.StatePath())
	if err != nil {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ "+err.Error(), colors.Red))
		return func() {}, false
	}
	return release, true
}

// runQueue drains a filtered issue queue. Streaming/dry-run drains print plain
// log lines on stdout; otherwise the whole drain runs behind one persistent
// Bubble Tea monitor dashboard — the same one a single loop uses — with a
// queue-position indicator spanning every issue. Mirrors cmdRun's monitor path:
// the work runs in a goroutine while GlobalProgram.Run() owns the main thread,
// and operator quit cancels the in-flight issue and stops the drain.
func runQueue(cfg *config.LoopConfig) int {
	if !runPreflight(cfg) {
		return 1
	}
	release, ok := lockRepo(cfg)
	if !ok {
		return 1
	}
	defer release()

	runner, err := queue.NewIssueQueueRunner(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating issue queue runner: %v\n", err)
		return 1
	}

	if cfg.StreamOutput || cfg.DryRun {
		return runner.Run()
	}

	tui.StartMonitorTUI(cfg)

	var code int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		code = runner.Run()
		state := "completed"
		if code != 0 {
			state = "failed"
		}
		tui.NotifyTUIDone(state)
	}()

	if _, err := tui.GlobalProgram.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running monitor TUI: %v\n", err)
		runner.Cancel()
		wg.Wait()
		return 1
	}

	// TUI exited (drain finished, or operator hit /quit). Cancel any in-flight
	// issue so a long agent step is torn down and the drain stops advancing.
	runner.Cancel()
	wg.Wait()

	return code
}

func shouldWizard(cfg *config.LoopConfig) bool {
	if cfg.NoWizard {
		return false
	}
	if cfg.Wizard {
		return true
	}
	if cfg.Mode == "feature" && cfg.Mission != "" {
		return false // Quick features don't prompt wizard
	}
	// If CLI arguments specify significant flags, skip wizard
	// We check if significant fields are custom
	if cfg.Issue != "" || cfg.Mission != "" || cfg.Mode == "queue" || cfg.DryRun {
		return false
	}

	// Verify stdin is a TTY
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func cmdRun(cfg *config.LoopConfig) int {
	if shouldWizard(cfg) {
		wizardCfg, err := tui.RunWizardTUI(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Wizard error: %v\n", err)
			return 1
		}
		if wizardCfg == nil {
			fmt.Println("Aborted.")
			return 0
		}
		cfg = wizardCfg
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if cfg.Mode == "queue" && cfg.IssueQueue != nil {
		return runQueue(cfg)
	}

	if cfg.Mode == "feature" && cfg.Mission == "" {
		fmt.Println("Quick/feature mode needs a mission. Examples:")
		fmt.Println("  mm quick \"add feature XYZ\"")
		fmt.Println("  mm \"add dark mode toggle\"")
		return 1
	}

	if !runPreflight(cfg) {
		return 1
	}
	release, ok := lockRepo(cfg)
	if !ok {
		return 1
	}
	defer release()

	// Execute loop
	l := loop.NewMiddleManagerLoop(cfg)

	if cfg.StreamOutput || cfg.DryRun {
		// Run loop directly on standard stdout
		result, err := l.RunUntilComplete()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Loop error: %v\n", err)
			return 1
		}
		printSummaryPanel(cfg, l, result)
		if result.Success {
			return 0
		}
		return 1
	}

	// Run loop in background goroutine and start Bubble Tea Monitor Dashboard
	tui.StartMonitorTUI(cfg)

	var result *loop.LoopResult
	var loopErr error
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		result, loopErr = l.RunUntilComplete()
		if tui.GlobalProgram != nil {
			state := "completed"
			if loopErr != nil || (result != nil && !result.Success) {
				state = "failed"
			}
			l.NotifyStatus(state)
		}
	}()

	// Start Bubble Tea Program
	if _, err := tui.GlobalProgram.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running monitor TUI: %v\n", err)
		l.Cancel()
		wg.Wait()
		return 1
	}

	// The TUI has exited (normal completion or operator quit). Cancel the
	// loop so any in-flight agent process group is terminated and control
	// returns immediately rather than blocking on a long agent step.
	l.Cancel()
	wg.Wait()

	if loopErr != nil {
		fmt.Fprintf(os.Stderr, "Loop error: %v\n", loopErr)
		return 1
	}

	if result != nil && result.Success {
		return 0
	}
	return 1
}

func printSummaryPanel(cfg *config.LoopConfig, l *loop.MiddleManagerLoop, result *loop.LoopResult) {
	if result == nil {
		fmt.Fprintln(os.Stderr, "Loop returned no result.")
		return
	}
	fmt.Println()
	fmt.Println(tui.RenderSummaryPanel(result.Success, result.Reason, result.PRURL, result.Iterations, l.TopPlanItem()))

	if !result.Success {
		sc := cfg.StepFor("execute")
		promptMsg := fmt.Sprintf("The last task %q failed verification. Please debug and fix.", l.TopPlanItem())
		agentCmd := ""
		switch sc.Agent {
		case "grok":
			agentCmd = fmt.Sprintf("grok -p %q --cwd %s", promptMsg, cfg.Repo)
		case "claude":
			agentCmd = fmt.Sprintf("claude %q", promptMsg)
		case "opencode":
			agentCmd = fmt.Sprintf("opencode run %q --dir %s", promptMsg, cfg.Repo)
		case "codex":
			agentCmd = fmt.Sprintf("codex exec %q -C %s", promptMsg, cfg.Repo)
		default:
			agentCmd = fmt.Sprintf("%s %q", sc.Agent, promptMsg)
		}
		fmt.Println(colors.Colored("\n💻 Pick up where it left off — launch your programmer agent directly:", colors.Cyan))
		fmt.Println("   " + colors.Colored(agentCmd, colors.Green+colors.Bold))
	}
}

func cmdMerge(cfg *config.LoopConfig) {
	author := ""
	label := ""
	limit := 30
	if cfg.IssueQueue != nil {
		author = cfg.IssueQueue.Author
		label = cfg.IssueQueue.Label
		limit = cfg.IssueQueue.Limit
	}

	baseBranch := cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = gitops.DetectBaseBranch(cfg.Repo)
	}

	fmt.Println(colors.Colored(fmt.Sprintf("Scanning for open PRs targeting %q to auto-merge...", baseBranch), colors.Cyan))

	for {
		prs, err := gitops.ListOpenPRs(cfg.Repo, author, label, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing PRs: %v\n", err)
			os.Exit(1)
		}

		// Filter to only middle-manager created branches targeting our target base branch
		var mmPrs []gitops.PullRequest
		for _, pr := range prs {
			branchMatch := strings.HasPrefix(pr.HeadRef, cfg.BranchPrefix+"/loop-") || strings.HasPrefix(pr.HeadRef, cfg.BranchPrefix+"/issue-")
			baseMatch := pr.BaseRef == baseBranch
			if branchMatch && baseMatch {
				mmPrs = append(mmPrs, pr)
			}
		}

		if len(mmPrs) == 0 {
			fmt.Printf("No open middle-manager PRs targeting %q found.\n", baseBranch)
			return
		}

		pendingCount := 0
		mergedCount := 0

		for _, pr := range mmPrs {
			// Wait only on the repo's REQUIRED status checks; ignore non-blocking
			// ones (a non-required job that is still running or red must not hold up
			// the merge). If the repo defines no required checks, fall back to the
			// full check rollup so we don't merge over an unprotected repo's CI.
			if reqState := gitops.RequiredChecksState(cfg.Repo, pr.Number); reqState != "none" {
				pr.ChecksState = reqState
			}
			safe, reason := pr.IsSafeToMerge(true) // require checks to pass
			if safe {
				fmt.Printf("PR #%d (%s) targeting %q is green. Merging...\n", pr.Number, pr.Title, pr.BaseRef)
				out, err := gitops.MergePR(cfg.Repo, pr.Number, "squash", true, cfg.DryRun)
				if err != nil {
					fmt.Printf("  Error merging PR #%d: %v\n", pr.Number, err)
				} else {
					if out != "" {
						fmt.Printf("  Merged: %s\n", out)
					}
					mergedCount++
				}
			} else {
				if reason == "checks pending" {
					fmt.Printf("PR #%d (%s) has pending checks: waiting...\n", pr.Number, pr.Title)
					pendingCount++
				} else {
					fmt.Printf("PR #%d (%s) cannot be merged: %s\n", pr.Number, pr.Title, reason)
				}
			}
		}

		if pendingCount == 0 {
			fmt.Println("No more pending middle-manager PRs. Exiting.")
			return
		}

		fmt.Println(colors.Colored("Waiting 30 seconds for CI/CD checks...", colors.Dim))
		time.Sleep(30 * time.Second)
	}
}
