package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
)

// Preflight validates a run's external dependencies BEFORE any agent burns
// tokens: agents on PATH, gh present and authenticated when a PR will be
// opened, a writable state dir. It returns human-readable warnings for
// degraded-but-runnable conditions and a fatal error for true blockers.
func Preflight(cfg *config.LoopConfig) (warnings []string, fatal error) {
	// State dir must be writable — every step writes prompts/outputs/ledger.
	state := cfg.StatePath()
	probe := filepath.Join(state, ".preflight")
	if err := os.WriteFile(probe, []byte("ok"), 0644); err != nil {
		return warnings, fmt.Errorf("state dir %s is not writable: %w", state, err)
	}
	_ = os.Remove(probe)

	installed := agents.AvailableAgents(cfg.BinaryOverrides)
	if len(installed) == 0 && !cfg.DryRun {
		return warnings, fmt.Errorf("no agent CLIs installed — install one of %s, or declare a custom agent in %s",
			strings.Join(agents.AgentNames, ", "), config.DefaultConfigPath())
	}

	// Every configured seat (base agent + every ladder rung) must at least be a
	// KNOWN agent; unknown names are typos that would otherwise surface as a
	// mid-run fallback. Known-but-missing agents degrade to a fallback — warn.
	installedSet := make(map[string]bool, len(installed))
	for _, name := range installed {
		installedSet[name] = true
	}
	checkRef := func(where, name string) {
		if name == "" || agents.IsRandom(name) {
			return
		}
		if _, known := agents.AgentSpecs[name]; !known {
			warnings = append(warnings, fmt.Sprintf("%s names unknown agent %q (known: %s) — it will fall back at runtime", where, name, strings.Join(agents.AgentNames, ", ")))
			return
		}
		if !installedSet[name] && !cfg.DryRun {
			warnings = append(warnings, fmt.Sprintf("%s agent %q is not installed — mm will fall back to an installed one", where, name))
		}
	}
	for _, step := range cfg.ActiveSteps() {
		sc := cfg.StepFor(step)
		if sc == nil {
			continue
		}
		checkRef("step "+step, sc.Agent)
		for i, ref := range sc.Escalate {
			checkRef(fmt.Sprintf("step %s escalation rung %d", step, i+1), ref.Agent)
		}
	}

	if gitops.RepoIsGit(cfg.Repo) {
		// A dirty tree on a single run is the operator's uncommitted WIP — the
		// loop's commit sweep would carry it into the mm branch and PR.
		if cfg.Mode != "queue" && !cfg.DryRun && gitops.HasChanges(cfg.Repo) {
			warnings = append(warnings, "working tree has uncommitted changes — they may be swept into the loop's commit; commit or stash them first")
		}

		// Solo/serialized runs BLOCK until the PR merges. With auto-merge off
		// (the default) nothing merges it except a human or `mm merge` — say so
		// up front instead of letting the run sit silently against the timeout.
		if cfg.WaitForMerge && cfg.NoMerge && !cfg.NoPR && !cfg.DryRun {
			warnings = append(warnings, fmt.Sprintf("this run waits for its PR to merge but auto-merge is OFF — merge the PR yourself (or run `mm merge`) within the %d-minute timeout, or enable auto-merge", cfg.MergeTimeoutMinutes))
		}

		// PR flows need gh, authenticated. Solo additionally BLOCKS on the merge,
		// so a broken gh would strand it for the full merge timeout.
		needsPR := !cfg.NoPR && (cfg.IsSolo() || cfg.Steps >= 4 || cfg.Mode == "queue")
		if needsPR && !cfg.DryRun {
			if _, err := exec.LookPath("gh"); err != nil {
				return warnings, fmt.Errorf("this run opens PRs but `gh` is not installed — install the GitHub CLI or pass --no-pr")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			auth := exec.CommandContext(ctx, "gh", "auth", "status")
			auth.Dir = cfg.Repo
			if err := auth.Run(); err != nil {
				return warnings, fmt.Errorf("this run opens PRs but `gh` is not authenticated (`gh auth status` failed) — run `gh auth login` or pass --no-pr")
			}
		}
	}

	return warnings, nil
}

// AcquireRepoLock takes a PID lockfile in the state dir so two mm runs can
// never work the same repo at once (they would fight over branches and the
// working tree). A lockfile whose PID is no longer alive — a crashed run — is
// taken over silently. Returns a release func to call on every exit path.
func AcquireRepoLock(stateDir string) (release func(), err error) {
	path := filepath.Join(stateDir, "mm.lock")
	if b, readErr := os.ReadFile(path); readErr == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(b))); convErr == nil && pid != os.Getpid() && pidAlive(pid) {
			return nil, fmt.Errorf("another mm run (pid %d) is already working this repo — wait for it to finish, or delete %s if it is truly dead", pid, path)
		}
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return nil, fmt.Errorf("could not take repo lock %s: %w", path, err)
	}
	return func() { _ = os.Remove(path) }, nil
}

