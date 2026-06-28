package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/loop"
	"github.com/bradflaugher/middle-manager/pkg/queue"
	"github.com/bradflaugher/middle-manager/pkg/tui"
)

func main() {
	// Parse CLI Arguments
	cmdName, cfg, err := config.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

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
		cmdRun(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmdName)
		os.Exit(1)
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
	rows := agents.ListAgentsStatus(cfg.BinaryOverrides)
	fmt.Println(colors.Colored(fmt.Sprintf("%-10s %-10s %-24s %s", "AGENT", "AVAILABLE", "BINARY", "YOLO FLAG"), colors.Cyan+colors.Bold))
	fmt.Println(colors.Colored(strings.Repeat("-", 72), colors.Cyan))
	for _, row := range rows {
		agentPad := colors.Colored(fmt.Sprintf("%-10s", row["agent"]), colors.Bold)
		availColor := colors.Red
		if row["available"] == "yes" {
			availColor = colors.Green
		}
		availPad := colors.Colored(fmt.Sprintf("%-10s", row["available"]), availColor)
		fmt.Printf("%s %s %-24s %s\n", agentPad, availPad, row["binary"], row["yolo"])
		if row["notes"] != "" {
			fmt.Println(colors.Colored("           "+row["notes"], colors.Yellow))
		}
	}
}

func cmdInit(cfg *config.LoopConfig) {
	dest := filepath.Join(cfg.Repo, "AGENTS.md")

	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("exists: %s\n", dest)
		return
	}

	_ = os.WriteFile(dest, []byte("# AGENTS.md\n\nRepository memory for middle-manager loops.\nAdd build commands, conventions, and things agents keep forgetting.\n"), 0644)
	fmt.Println(colors.Colored(fmt.Sprintf("created: %s", dest), colors.Green))
	fmt.Printf("State dir: %s\n", cfg.StatePath())
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
	for _, name := range []string{"error_log.txt", "verify_log.txt", "iteration.txt", "queue.log"} {
		p := filepath.Join(state, name)
		status := colors.Colored("missing", colors.Yellow)
		if _, err := os.Stat(p); err == nil {
			status = colors.Colored("exists", colors.Green)
		}
		fmt.Printf("  %-16s: %s\n", name, status)
	}
}

func cmdIssues(cfg *config.LoopConfig) {
	if cfg.IssueQueue == nil {
		fmt.Fprintln(os.Stderr, "Issue queue requires --label, --author, and/or --mode queue")
		os.Exit(1)
	}
	cfg.Mode = "queue"
	runner, err := queue.NewIssueQueueRunner(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating issue queue runner: %v\n", err)
		os.Exit(1)
	}
	os.Exit(runner.Run())
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

func cmdRun(cfg *config.LoopConfig) {
	if shouldWizard(cfg) {
		wizardCfg, err := tui.RunWizardTUI(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Wizard error: %v\n", err)
			os.Exit(1)
		}
		if wizardCfg == nil {
			fmt.Println("Aborted.")
			os.Exit(0)
		}
		cfg = wizardCfg
	}

	if cfg.Mode == "queue" && cfg.IssueQueue != nil {
		runner, err := queue.NewIssueQueueRunner(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(runner.Run())
	}

	if cfg.Mode == "feature" && cfg.Mission == "" {
		fmt.Println("Quick/feature mode needs a mission. Examples:")
		fmt.Println("  mm quick \"add feature XYZ\"")
		fmt.Println("  mm \"add dark mode toggle\"")
		os.Exit(1)
	}

	// Execute loop
	l := loop.NewMiddleManagerLoop(cfg)

	if cfg.StreamOutput || cfg.DryRun {
		// Run loop directly on standard stdout
		result, err := l.RunUntilComplete()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Loop error: %v\n", err)
			os.Exit(1)
		}
		printSummaryPanel(cfg, l, result)
		if result.Success {
			os.Exit(0)
		} else {
			os.Exit(1)
		}
	} else {
		// Run loop in background goroutine and start Bubble Tea Monitor Dashboard
		tui.StartMonitorTUI(cfg)

		var result *loop.LoopResult
		var loopErr error
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			result, loopErr = l.RunUntilComplete()
			// Shuts down bubbletea TUI view once background loop completes
			if tui.GlobalProgram != nil {
				tui.GlobalProgram.Quit()
			}
		}()

		// Start Bubble Tea Program
		if _, err := tui.GlobalProgram.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error running monitor TUI: %v\n", err)
			os.Exit(1)
		}

		wg.Wait()

		if loopErr != nil {
			fmt.Fprintf(os.Stderr, "Loop error: %v\n", loopErr)
			os.Exit(1)
		}

		printSummaryPanel(cfg, l, result)
		if result.Success {
			os.Exit(0)
		} else {
			os.Exit(1)
		}
	}
}

func printSummaryPanel(cfg *config.LoopConfig, l *loop.MiddleManagerLoop, result *loop.LoopResult) {
	fmt.Println()
	border := strings.Repeat("=", 60)
	if result.Success {
		fmt.Println(colors.Colored(border, colors.Green))
		fmt.Println(colors.Colored("🎉 SUCCESS: middle-manager loop finished successfully", colors.Green+colors.Bold))
		fmt.Println(colors.Colored(border, colors.Green))
		fmt.Printf("  All tasks in plan checked off successfully.\n")
		fmt.Printf("  Total loop iterations: %d\n", result.Iterations)
		if result.PRURL != "" {
			fmt.Printf("  PR URL: %s\n", colors.Colored(result.PRURL, colors.Green+colors.Bold))
		}
		fmt.Println(colors.Colored(border, colors.Green))
	} else {
		fmt.Println(colors.Colored(border, colors.Yellow))
		fmt.Println(colors.Colored("⚠️ LOOP ABANDONED / FAILED", colors.Yellow+colors.Bold))
		fmt.Println(colors.Colored(border, colors.Yellow))
		fmt.Printf("  Reason: %s\n", colors.Colored(result.Reason, colors.Bold))
		fmt.Printf("  Total loop iterations: %d\n\n", result.Iterations)

		sc := cfg.StepFor("execute")
		promptMsg := fmt.Sprintf("The last task %q failed verification. Please debug and fix.", l.TopPlanItem())

		// Build interactive command suggestion
		agentCmd := ""
		switch sc.Agent {
		case "grok":
			agentCmd = fmt.Sprintf("grok --cwd %s %q", cfg.Repo, promptMsg)
		case "claude":
			agentCmd = fmt.Sprintf("claude %q", promptMsg)
		case "opencode":
			agentCmd = fmt.Sprintf("opencode run %q --dir %s", promptMsg, cfg.Repo)
		default:
			agentCmd = fmt.Sprintf("%s %q", sc.Agent, promptMsg)
		}

		fmt.Println(colors.Colored("💻 To launch an interactive session with your programmer agent, run:", colors.Cyan))
		fmt.Println("   " + colors.Colored(agentCmd, colors.Green+colors.Bold))
		fmt.Println(colors.Colored(border, colors.Yellow))
	}
}
