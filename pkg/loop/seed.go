package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/prompts"
)

// SeedIssue is one proposed backlog item parsed from the seeder agent's output.
type SeedIssue struct {
	Title    string
	Body     string
	Priority string // "P0".."P3", or "" when the agent omitted/mangled it
	Size     string // "XS","S","M","L","XL" perceived difficulty, or ""
}

var validSizes = map[string]bool{"XS": true, "S": true, "M": true, "L": true, "XL": true}

// ParseSeedIssues extracts ===ISSUE=== blocks from agent output. The format is
// deliberately rigid (see prompts.SeedTemplate) so parsing needs no LLM: a
// block without a TITLE and a BODY is dropped, not guessed at; a malformed
// PRIORITY is dropped to "" rather than inventing a label.
func ParseSeedIssues(output string) []SeedIssue {
	var issues []SeedIssue
	blocks := strings.Split(output, "===ISSUE===")
	for _, block := range blocks[min(1, len(blocks)):] {
		if end := strings.Index(block, "===END==="); end >= 0 {
			block = block[:end]
		}
		title, priority, size := "", "", ""
		bodyIdx := -1
		lines := strings.Split(block, "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if title == "" && strings.HasPrefix(trimmed, "TITLE:") {
				title = strings.TrimSpace(strings.TrimPrefix(trimmed, "TITLE:"))
			}
			if priority == "" && strings.HasPrefix(trimmed, "PRIORITY:") {
				p := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(trimmed, "PRIORITY:")))
				if len(p) >= 2 && p[0] == 'P' && p[1] >= '0' && p[1] <= '3' {
					priority = p[:2]
				}
			}
			if size == "" && strings.HasPrefix(trimmed, "SIZE:") {
				// Take only the leading token — the agent may echo the guidance text.
				s := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(trimmed, "SIZE:")))
				if fields := strings.Fields(s); len(fields) > 0 && validSizes[fields[0]] {
					size = fields[0]
				}
			}
			if bodyIdx == -1 && strings.HasPrefix(trimmed, "BODY:") {
				bodyIdx = i
				break
			}
		}
		if title == "" || bodyIdx == -1 {
			continue
		}
		body := strings.TrimSpace(strings.Join(lines[bodyIdx+1:], "\n"))
		if body == "" {
			continue
		}
		if len(title) > 100 {
			title = title[:100]
		}
		issues = append(issues, SeedIssue{Title: title, Body: body, Priority: priority, Size: size})
	}
	return issues
}

// RunSeed audits the repo with the discover-seat agent and files the proposed
// issues via gh — the agent proposes, mm creates (deterministically). Returns
// an exit code.
func RunSeed(cfg *config.LoopConfig) int {
	logf := func(msg, color string) {
		if color != "" {
			msg = colors.Colored(msg, color)
		}
		fmt.Println(msg)
	}

	if warnings, fatal := Preflight(cfg); fatal != nil {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ preflight: "+fatal.Error(), colors.Red))
		return 1
	} else {
		for _, w := range warnings {
			logf("⚠ preflight: "+w, colors.Yellow)
		}
	}
	if !cfg.DryRun && !gitops.GHAvailable() {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ mm seed files GitHub issues — install and authenticate `gh` (or use --dry-run to preview)", colors.Red))
		return 1
	}

	count := cfg.SeedCount
	if count <= 0 {
		count = 5
	}

	// The seeder sits in the discover seat: proposing work is planning work.
	agent := cfg.Discover.Agent
	binary := cfg.BinaryOverrides[agent]
	if agents.IsRandom(agent) || !agents.AgentAvailable(agent, binary) {
		agent = agents.AutodetectAgent("discover", cfg.BinaryOverrides, "")
		binary = cfg.BinaryOverrides[agent]
	}
	if agent == "" {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ no installed agent available to audit the repo", colors.Red))
		return 1
	}

	l := NewMiddleManagerLoop(cfg)
	template := prompts.LoadPrompt(cfg.Repo, filepath.Dir(cfg.NotesPath()), "seed")
	ctx := prompts.BuildContext(prompts.Context{
		Repo:        cfg.Repo,
		AgentMemory: l.AgentMemory(),
		Notes:       l.ReadText(cfg.NotesPath(), ""),
		Mission:     cfg.Mission,
	})
	ctx["seed_count"] = strconv.Itoa(count)
	prompt := prompts.RenderPrompt(template, ctx)
	l.WriteText(filepath.Join(l.state, "seed_prompt.md"), prompt)

	run, err := agents.BuildCommand(agent, prompt, cfg.Repo, cfg.Discover.Model, cfg.Yolo, nil, binary)
	if err != nil {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ "+err.Error(), colors.Red))
		return 1
	}
	logf(fmt.Sprintf("🌱 Auditing %s with %s to propose %d issue(s)...", cfg.Repo, strings.ToUpper(agent), count), colors.Cyan+colors.Bold)
	stdout, exitCode, err := agents.RunAgent(l.ctx, run, false, "seed", func(text string, isThought bool) {
		os.Stdout.WriteString(text)
	})
	l.WriteText(filepath.Join(l.state, "seed_output.txt"), stdout)
	if err != nil || exitCode != 0 {
		fmt.Fprintln(os.Stderr, colors.Colored(fmt.Sprintf("✗ seeder agent failed (exit %d): %v", exitCode, err), colors.Red))
		return 1
	}

	issues := ParseSeedIssues(stdout)
	if len(issues) == 0 {
		fmt.Fprintln(os.Stderr, colors.Colored("✗ the agent produced no parseable ===ISSUE=== blocks — see "+filepath.Join(l.state, "seed_output.txt"), colors.Red))
		return 1
	}
	if len(issues) > count {
		logf(fmt.Sprintf("Agent proposed %d issues; keeping the first %d.", len(issues), count), colors.Yellow)
		issues = issues[:count]
	}

	label := ""
	if cfg.IssueQueue != nil {
		label = cfg.IssueQueue.Label
	}
	if label == "" {
		label = "mm-todo"
	}

	if cfg.DryRun {
		logf(fmt.Sprintf("\n[dry-run] would create %d issue(s) with label %q:", len(issues), label), colors.Cyan)
		for i, is := range issues {
			logf(fmt.Sprintf("\n--- %d. [%s/%s] %s ---\n%s", i+1, orDash(is.Priority), orDash(is.Size), is.Title, is.Body), "")
		}
		return 0
	}

	gitops.EnsureLabel(cfg.Repo, label, "8B5CF6")
	tagColors := map[string]string{
		"P0": "B91C1C", "P1": "EA580C", "P2": "CA8A04", "P3": "6B7280",
		"XS": "BBF7D0", "S": "86EFAC", "M": "FDE047", "L": "FDBA74", "XL": "FCA5A5",
	}
	ensured := map[string]bool{}
	ensure := func(tag string) {
		if tag != "" && !ensured[tag] {
			gitops.EnsureLabel(cfg.Repo, tag, tagColors[tag])
			ensured[tag] = true
		}
	}
	created := 0
	for _, is := range issues {
		labels := []string{label}
		for _, tag := range []string{is.Priority, is.Size} {
			if tag != "" {
				ensure(tag)
				labels = append(labels, tag)
			}
		}
		url, err := gitops.CreateIssue(cfg.Repo, is.Title, is.Body, labels, false)
		if err != nil {
			logf(fmt.Sprintf("⚠️ could not create %q: %v", is.Title, err), colors.Yellow)
			continue
		}
		created++
		logf(fmt.Sprintf("✓ %s [%s/%s] %s", url, orDash(is.Priority), orDash(is.Size), is.Title), colors.Green)
	}
	if created == 0 {
		return 1
	}
	logf(fmt.Sprintf("\n🌱 Seeded %d issue(s). Drain them with:\n   mm --label %q --close-issues --merge", created, label), colors.Cyan+colors.Bold)
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
