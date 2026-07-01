package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type IssueQueueConfig struct {
	Label          string `json:"label"`
	Author         string `json:"author"`
	State          string `json:"state"`
	Limit          int    `json:"limit"`
	CloseOnSuccess bool   `json:"close_on_success"`
	CloseComment   string `json:"close_comment"`
}

// AgentRef names one agent (and optionally a model) — used as a rung in a
// step's escalation ladder. Parsed from "agent" or "agent:model" strings.
type AgentRef struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

// ParseAgentRef parses "agent" or "agent:model". Only the FIRST colon splits,
// so models containing colons (e.g. ollama tags like "qwen2.5:14b") survive.
func ParseAgentRef(s string) AgentRef {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ":"); i >= 0 {
		return AgentRef{Agent: strings.TrimSpace(s[:i]), Model: strings.TrimSpace(s[i+1:])}
	}
	return AgentRef{Agent: s}
}

// ParseAgentRefList parses a comma-separated escalation ladder like
// "claude:opus,codex" into ordered AgentRefs, skipping empty entries.
func ParseAgentRefList(s string) []AgentRef {
	var refs []AgentRef
	for _, part := range strings.Split(s, ",") {
		if ref := ParseAgentRef(part); ref.Agent != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// AgentDef declares a custom agent CLI in config (key "agents"), so any
// headless coding CLI — not just the built-in roster — can fill any seat.
// Zero-value fields follow the same semantics as agents.AgentSpec: an empty
// PrintFlag appends the prompt as the trailing positional argument.
type AgentDef struct {
	Binary     string   `json:"binary"`
	Subcommand []string `json:"subcommand"`
	PrintFlag  string   `json:"print_flag"`
	YoloFlags  []string `json:"yolo_flags"`
	ModelFlag  string   `json:"model_flag"`
	CwdFlag    string   `json:"cwd_flag"`
	ExtraArgs  []string `json:"extra_args"`
	Notes      string   `json:"notes"`
}

type StepConfig struct {
	Agent      string   `json:"agent"`
	Model      string   `json:"model"`
	ExtraArgs  []string `json:"extra_args"`
	PromptFile string   `json:"prompt_file"`
	Enabled    bool     `json:"enabled"`
	// TimeoutMinutes bounds one invocation of this step's agent. 0 inherits the
	// global StepTimeoutMinutes; negative disables the timeout for this step.
	TimeoutMinutes int `json:"timeout_minutes"`
	// Escalate is the step's ordered escalation ladder: after every
	// EscalateAfter failed iterations the step moves one rung up, so a cheap
	// agent gets the first attempts and a stronger one takes over when the
	// cheap one is verifiably failing.
	Escalate []AgentRef `json:"escalate"`
}

type LoopConfig struct {
	Repo            string            `json:"repo"`
	Steps           int               `json:"steps"`
	MaxIterations   int               `json:"max_iterations"`
	Yolo            bool              `json:"yolo"`
	DryRun          bool              `json:"dry_run"`
	Interactive     bool              `json:"interactive"`
	Issue           string            `json:"issue"`
	Mode            string            `json:"mode"` // repair | issue | queue | feature
	Mission         string            `json:"mission"`
	Fresh           bool              `json:"fresh"`
	IssueQueue      *IssueQueueConfig `json:"issue_queue"`
	BranchPrefix    string            `json:"branch_prefix"`
	BaseBranch      string            `json:"base_branch"`
	NoPR            bool              `json:"no_pr"`
	NoMerge         bool              `json:"no_merge"`
	Discover        StepConfig        `json:"discover"`
	Execute         StepConfig        `json:"execute"`
	Verify          StepConfig        `json:"verify"`
	Commit          StepConfig        `json:"commit"`
	BinaryOverrides map[string]string `json:"binary_overrides"`
	StateDir        string            `json:"state_dir"`
	// NotesFile is the orchestrator notes file: durable, repo-scoped learnings
	// that live OUTSIDE the repository so agents read them via the prompt but
	// can never commit them. Empty means "<state root>/notes.md"; the queue
	// runner pins it before per-issue StateDir overrides so every issue in a
	// drain shares one file.
	NotesFile         string `json:"notes_file"`
	AgentMemoryFile   string `json:"agent_memory_file"`
	FixUnrelatedTests bool   `json:"fix_unrelated_tests"`
	StreamOutput      bool   `json:"stream_output"`
	BatchSize         int    `json:"batch_size"`

	// Solo is single-agent mode: one agent does discover+execute+verify+tests in
	// a single step (Steps==1) and emits the VERDICT; mm still commits/PRs
	// deterministically. In a queue it serializes by waiting for each PR to merge.
	Solo bool `json:"solo"`
	// Worktree turns a queue drain into the worktree-collapse strategy: each issue
	// runs in its own git worktree (no per-issue PR), then one agent collapses the
	// branches into a single "mega" PR. Queue mode only.
	Worktree bool `json:"worktree"`
	// WaitForMerge makes the loop block until the PR it opened actually merges
	// (bounded by MergeTimeoutMinutes) before returning. Implied by Solo; in a
	// queue this is what serializes issues so they never conflict.
	WaitForMerge bool `json:"wait_for_merge"`
	// MergeTimeoutMinutes bounds the WaitForMerge poll so a stuck PR (failed
	// required check, branch-protection review) can never hang the drain forever.
	MergeTimeoutMinutes int `json:"merge_timeout_minutes"`
	// KeepWorktrees leaves the per-issue worktrees on disk after a collapse for
	// debugging instead of pruning them.
	KeepWorktrees bool `json:"keep_worktrees"`

	// CustomAgents registers extra agent CLIs by name (merged into the built-in
	// roster at startup), so mm works with any combination of agents.
	CustomAgents map[string]AgentDef `json:"agents"`
	// StepTimeoutMinutes is the default wall-clock bound for a single agent
	// invocation; a hung CLI can otherwise stall the factory forever. 0 disables.
	// Per-step TimeoutMinutes overrides it.
	StepTimeoutMinutes int `json:"step_timeout_minutes"`
	// EscalateAfter is how many failed iterations each escalation rung gets
	// before the ladder advances (min 1).
	EscalateAfter int `json:"escalate_after"`
	// DistinctVerifier forces the verify step onto a different agent than the
	// one that executed, so the critic never grades its own homework. With
	// "random" steps the verifier gets its own roll.
	DistinctVerifier bool `json:"distinct_verifier"`
	// MaxWallMinutes bounds a whole run's wall clock (0 = unbounded); the loop
	// stops before starting an iteration that would exceed it.
	MaxWallMinutes int `json:"max_wall_minutes"`

	// Interactive Wizard overrides
	Wizard   bool
	NoWizard bool
}

func NewDefaultConfig() *LoopConfig {
	return &LoopConfig{
		Steps:               4,
		MaxIterations:       10,
		Yolo:                true,
		BranchPrefix:        "mm",
		NoMerge:             true,
		AgentMemoryFile:     "AGENTS.md",
		StreamOutput:        false,
		BatchSize:           1,
		Fresh:               true,
		MergeTimeoutMinutes: 60,
		StepTimeoutMinutes:  60,
		EscalateAfter:       1,
		BinaryOverrides:     make(map[string]string),
		Discover: StepConfig{
			Agent:   "claude",
			Enabled: true,
		},
		Execute: StepConfig{
			Agent:   "opencode",
			Enabled: true,
		},
		Verify: StepConfig{
			Agent:   "claude",
			Enabled: true,
		},
		Commit: StepConfig{
			Agent:   "grok",
			Enabled: true,
		},
	}
}

func (cfg *LoopConfig) StepFor(name string) *StepConfig {
	switch name {
	case "discover":
		return &cfg.Discover
	case "execute", "solo":
		// Solo mode reuses the Execute step's agent/model slot — it is the one
		// "programmer" agent that does the whole job — so the wizard's existing
		// per-step agent picker configures it without a new field.
		return &cfg.Execute
	case "verify":
		return &cfg.Verify
	case "commit":
		return &cfg.Commit
	default:
		return nil
	}
}

// IsSolo reports whether the run is single-agent solo mode. Steps==1 and the
// Solo flag are kept equivalent here so every consumer (ActiveSteps, the
// commit/PR path, the queue serializer) agrees regardless of how the config was
// built (CLI, JSON, or direct construction).
func (cfg *LoopConfig) IsSolo() bool {
	return cfg.Solo || cfg.Steps == 1
}

func (cfg *LoopConfig) ActiveSteps() []string {
	// Solo collapses the whole pipeline into a single agent step.
	if cfg.IsSolo() {
		return []string{"solo"}
	}
	names := []string{"discover", "execute", "verify", "commit"}
	if cfg.Steps == 3 {
		names = []string{"discover", "execute", "verify"}
	}
	active := []string{}
	for _, n := range names {
		if sc := cfg.StepFor(n); sc != nil && sc.Enabled {
			active = append(active, n)
		}
	}
	return active
}

// Validate rejects incoherent flag combinations before a run starts.
func (cfg *LoopConfig) Validate() error {
	if cfg.Worktree && cfg.IsSolo() {
		return fmt.Errorf("--worktree and --solo are competing queue strategies; pick one")
	}
	// Worktree is a queue strategy and needs an actual issue queue to drain.
	// The --worktree flag flips Mode to "queue" on its own, so a Mode check alone
	// is unreachable — guard on the queue source itself.
	if cfg.Worktree && cfg.IssueQueue == nil {
		return fmt.Errorf("--worktree drains a GitHub issue queue — pass --label and/or --author")
	}
	return nil
}

func (cfg *LoopConfig) StatePath() string {
	var base string
	if cfg.StateDir != "" {
		base = cfg.StateDir
	} else {
		base = DefaultStatePath(cfg.Repo)
	}
	_ = os.MkdirAll(base, 0755)
	return base
}

// DefaultStatePath returns the out-of-repo state directory for a repo:
// $XDG_STATE_HOME/middle-manager/<base>-<hash8> (falling back to
// ~/.local/state). Keeping orchestrator state outside the working tree means
// mm never pollutes the repo — no .gitignore edits, and no way for an agent's
// `git add -A` to sweep prompts/logs/worktrees into a commit. The hash of the
// absolute repo path keeps two repos with the same basename apart.
func DefaultStatePath(repo string) string {
	abs, err := filepath.Abs(repo)
	if err != nil {
		abs = repo
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, herr := os.UserHomeDir()
		if herr != nil || home == "" {
			// No home directory at all — fall back to the legacy in-repo dir
			// (EnsureStateExcluded keeps it out of git via .git/info/exclude).
			return filepath.Join(abs, ".middle-manager")
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	sum := sha256.Sum256([]byte(abs))
	slug := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:4])
	return filepath.Join(stateHome, "middle-manager", slug)
}

// NotesPath resolves the orchestrator notes file (see NotesFile). Callers that
// override StateDir per issue must pin NotesFile first so notes stay shared.
func (cfg *LoopConfig) NotesPath() string {
	if cfg.NotesFile != "" {
		return cfg.NotesFile
	}
	return filepath.Join(cfg.StatePath(), "notes.md")
}

func MergeConfig(base, override map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range base {
		out[k] = v
	}

	stepKeys := map[string]bool{"discover": true, "execute": true, "verify": true, "commit": true}

	for k, v := range override {
		if stepKeys[k] {
			// Replace step configurations entirely to prevent default values from leaking
			out[k] = v
		} else if vMap, ok := v.(map[string]interface{}); ok {
			if outVal, ok := out[k]; ok {
				if outMap, ok := outVal.(map[string]interface{}); ok {
					out[k] = MergeConfig(outMap, vMap)
					continue
				}
			}
			out[k] = v
		} else {
			out[k] = v
		}
	}
	return out
}

func LoadJSONConfig(path string) (map[string]interface{}, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func ConfigFromMap(data map[string]interface{}, repo string) *LoopConfig {
	cfg := NewDefaultConfig()
	if repo != "" {
		cfg.Repo, _ = filepath.Abs(repo)
	} else {
		wd, _ := os.Getwd()
		cfg.Repo = wd
	}

	if v, ok := data["steps"].(float64); ok {
		cfg.Steps = int(v)
	}
	if v, ok := data["max_iterations"].(float64); ok {
		cfg.MaxIterations = int(v)
	}
	if v, ok := data["yolo"].(bool); ok {
		cfg.Yolo = v
	}
	if v, ok := data["branch_prefix"].(string); ok {
		cfg.BranchPrefix = v
	}
	if v, ok := data["base_branch"].(string); ok {
		cfg.BaseBranch = v
	}
	if v, ok := data["no_merge"].(bool); ok {
		cfg.NoMerge = v
	}
	if v, ok := data["agent_memory_file"].(string); ok {
		cfg.AgentMemoryFile = v
	}
	if v, ok := data["notes_file"].(string); ok {
		cfg.NotesFile = v
	}
	if v, ok := data["state_dir"].(string); ok {
		cfg.StateDir = v
	}
	if v, ok := data["stream_output"].(bool); ok {
		cfg.StreamOutput = v
	}
	if v, ok := data["batch_size"].(float64); ok {
		cfg.BatchSize = int(v)
	}
	if v, ok := data["fix_unrelated_tests"].(bool); ok {
		cfg.FixUnrelatedTests = v
	}
	if v, ok := data["solo"].(bool); ok {
		cfg.Solo = v
		if v {
			cfg.Steps = 1
			cfg.Commit.Enabled = false
			cfg.WaitForMerge = true
		}
	}
	if v, ok := data["worktree"].(bool); ok {
		cfg.Worktree = v
	}
	if v, ok := data["wait_for_merge"].(bool); ok {
		cfg.WaitForMerge = v
	}
	if v, ok := data["merge_timeout_minutes"].(float64); ok && int(v) > 0 {
		cfg.MergeTimeoutMinutes = int(v)
	}
	if v, ok := data["keep_worktrees"].(bool); ok {
		cfg.KeepWorktrees = v
	}
	if v, ok := data["step_timeout_minutes"].(float64); ok {
		cfg.StepTimeoutMinutes = int(v)
	}
	if v, ok := data["escalate_after"].(float64); ok && int(v) > 0 {
		cfg.EscalateAfter = int(v)
	}
	if v, ok := data["distinct_verifier"].(bool); ok {
		cfg.DistinctVerifier = v
	}
	if v, ok := data["max_wall_minutes"].(float64); ok && int(v) > 0 {
		cfg.MaxWallMinutes = int(v)
	}
	if defs, ok := data["agents"].(map[string]interface{}); ok {
		cfg.CustomAgents = parseAgentDefs(defs)
	}

	for _, step := range []string{"discover", "execute", "verify", "commit"} {
		if sVal, ok := data[step].(map[string]interface{}); ok {
			sc := cfg.StepFor(step)
			if agent, ok := sVal["agent"].(string); ok {
				sc.Agent = agent
			}
			if model, ok := sVal["model"].(string); ok {
				sc.Model = model
			}
			if enabled, ok := sVal["enabled"].(bool); ok {
				sc.Enabled = enabled
			}
			if extraArgs, ok := sVal["extra_args"].([]interface{}); ok {
				args := []string{}
				for _, a := range extraArgs {
					if s, ok := a.(string); ok {
						args = append(args, s)
					}
				}
				sc.ExtraArgs = args
			}
			if promptFile, ok := sVal["prompt_file"].(string); ok {
				sc.PromptFile = promptFile
			}
			if timeout, ok := sVal["timeout_minutes"].(float64); ok {
				sc.TimeoutMinutes = int(timeout)
			}
			if esc, ok := sVal["escalate"]; ok {
				sc.Escalate = parseEscalateValue(esc)
			}
		}
	}

	if bOverrides, ok := data["binary_overrides"].(map[string]interface{}); ok {
		for k, v := range bOverrides {
			if s, ok := v.(string); ok {
				cfg.BinaryOverrides[k] = s
			}
		}
	}

	normalizeSolo(cfg)
	return cfg
}

// parseEscalateValue accepts a JSON escalation ladder as either a list of
// objects ({"agent": "claude", "model": "opus"}), a list of "agent:model"
// strings, or one comma-separated string — whichever the operator finds natural.
func parseEscalateValue(v interface{}) []AgentRef {
	switch val := v.(type) {
	case string:
		return ParseAgentRefList(val)
	case []interface{}:
		var refs []AgentRef
		for _, item := range val {
			switch entry := item.(type) {
			case string:
				if ref := ParseAgentRef(entry); ref.Agent != "" {
					refs = append(refs, ref)
				}
			case map[string]interface{}:
				ref := AgentRef{}
				if a, ok := entry["agent"].(string); ok {
					ref.Agent = a
				}
				if m, ok := entry["model"].(string); ok {
					ref.Model = m
				}
				if ref.Agent != "" {
					refs = append(refs, ref)
				}
			}
		}
		return refs
	}
	return nil
}

// parseAgentDefs decodes the "agents" config map into AgentDefs via a JSON
// round-trip so the struct tags stay the single source of field names.
func parseAgentDefs(raw map[string]interface{}) map[string]AgentDef {
	out := make(map[string]AgentDef, len(raw))
	for name, v := range raw {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var def AgentDef
		if err := json.Unmarshal(b, &def); err != nil {
			continue
		}
		if def.Binary == "" {
			def.Binary = name
		}
		out[name] = def
	}
	return out
}

// DefaultConfigPath returns the persistent operator config file
// ($XDG_CONFIG_HOME or ~/.config)/middle-manager/config.json. It is loaded on
// every run (then overlaid by --config and CLI flags), so custom agents and
// escalation ladders only need declaring once.
func DefaultConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "middle-manager", "config.json")
}

// normalizeSolo keeps the solo invariants consistent no matter which knob set
// it: solo ⇔ Steps==1, no commit agent, and WaitForMerge forced on (solo's
// contract is "watch the PR until it merges", so --no-wait-merge must not be
// able to silently strand a queue mid-drain).
func normalizeSolo(cfg *LoopConfig) {
	if cfg.IsSolo() {
		cfg.Solo = true
		cfg.Steps = 1
		cfg.Commit.Enabled = false
		cfg.WaitForMerge = true
	}
}

// ParseArgs parses CLI arguments and merges with file-based configurations.
func ParseArgs(args []string) (string, *LoopConfig, error) {
	// First let's pre-process command shortcut like:
	// mm quick "add X" -> command="quick", prompt="add X"
	// mm "add feature XYZ" -> command="quick", prompt="add feature XYZ"

	var command string
	var promptArgs []string
	var restArgs []string

	cliCommands := map[string]bool{
		"run": true, "quick": true, "agents": true, "init": true, "status": true, "issues": true, "install-path": true, "merge": true,
	}

	if len(args) > 0 {
		first := args[0]
		if cliCommands[first] {
			command = first
			restArgs = args[1:]
		} else if !strings.HasPrefix(first, "-") {
			command = "quick"
			restArgs = args
		} else {
			command = "run"
			restArgs = args
		}
	} else {
		command = "run"
	}

	// In quick or bare mm "..." mode, we split mission and trailing flags
	var mission string
	if command == "quick" {
		missionParts := []string{}
		for i, arg := range restArgs {
			if strings.HasPrefix(arg, "-") {
				restArgs = restArgs[i:]
				break
			}
			missionParts = append(missionParts, arg)
			if i == len(restArgs)-1 {
				restArgs = []string{}
			}
		}
		mission = strings.Join(missionParts, " ")
	}

	cfg := NewDefaultConfig()
	var configFilePath string

	// Manually parse important flags first (repo, config) to construct base config
	repoVal := ""
	for i := 0; i < len(restArgs); i++ {
		arg := restArgs[i]
		if (arg == "--repo" || arg == "-C") && i+1 < len(restArgs) {
			repoVal = restArgs[i+1]
		} else if strings.HasPrefix(arg, "--repo=") {
			repoVal = strings.TrimPrefix(arg, "--repo=")
		} else if strings.HasPrefix(arg, "-C=") {
			repoVal = strings.TrimPrefix(arg, "-C=")
		} else if arg == "--config" && i+1 < len(restArgs) {
			configFilePath = restArgs[i+1]
		} else if strings.HasPrefix(arg, "--config=") {
			configFilePath = strings.TrimPrefix(arg, "--config=")
		}
	}

	if repoVal != "" {
		cfg.Repo, _ = filepath.Abs(repoVal)
	} else {
		wd, _ := os.Getwd()
		cfg.Repo = wd
	}

	// Layered config: the persistent operator file (custom agents, default
	// ladders) loads first, then an explicit --config file overrides it, then
	// the CLI flags below override both.
	baseMap, _ := LoadJSONConfig(DefaultConfigPath())
	if configFilePath != "" {
		overrideMap, err := LoadJSONConfig(configFilePath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load config file: %w", err)
		}
		if overrideMap != nil {
			if baseMap != nil {
				baseMap = MergeConfig(baseMap, overrideMap)
			} else {
				baseMap = overrideMap
			}
		}
	}
	if baseMap != nil {
		cfg = ConfigFromMap(baseMap, cfg.Repo)
	}

	// Now parse rest of CLI options
	for i := 0; i < len(restArgs); i++ {
		arg := restArgs[i]
		switch {
		case (arg == "--repo" || arg == "-C" || arg == "--config") && i+1 < len(restArgs):
			// Already consumed in the pre-pass above; skip the flag AND its value
			// here so the path isn't mistaken for a trailing mission prompt.
			i++
		case arg == "--steps" && i+1 < len(restArgs):
			steps, _ := strconv.Atoi(restArgs[i+1])
			cfg.Steps = steps
			if steps == 3 {
				cfg.Commit.Enabled = false
			}
			if steps == 1 {
				// 1 step is solo mode: one agent, mm waits for the PR to merge.
				cfg.Solo = true
				cfg.Commit.Enabled = false
				cfg.WaitForMerge = true
			}
			i++
		case arg == "--max-iterations" && i+1 < len(restArgs):
			iters, _ := strconv.Atoi(restArgs[i+1])
			cfg.MaxIterations = iters
			i++
		case arg == "--issue" && i+1 < len(restArgs):
			cfg.Issue = restArgs[i+1]
			cfg.Mode = "issue"
			i++
		case (arg == "--mission" || arg == "-m") && i+1 < len(restArgs):
			cfg.Mission = restArgs[i+1]
			i++
		case arg == "--mode" && i+1 < len(restArgs):
			cfg.Mode = restArgs[i+1]
			i++
		case arg == "--quick" || arg == "-q":
			cfg.Steps = 3
			cfg.Commit.Enabled = false
			cfg.Mode = "feature"
			cfg.Fresh = true
			if cfg.MaxIterations == 10 {
				cfg.MaxIterations = 5
			}
		case arg == "--fresh":
			cfg.Fresh = true
		case arg == "--no-fresh" || arg == "--resume":
			cfg.Fresh = false
		case arg == "--label" && i+1 < len(restArgs):
			if cfg.IssueQueue == nil {
				cfg.IssueQueue = &IssueQueueConfig{State: "open", Limit: 20, CloseOnSuccess: true}
			}
			cfg.IssueQueue.Label = restArgs[i+1]
			cfg.Mode = "queue"
			i++
		case arg == "--author" && i+1 < len(restArgs):
			if cfg.IssueQueue == nil {
				cfg.IssueQueue = &IssueQueueConfig{State: "open", Limit: 20, CloseOnSuccess: true}
			}
			cfg.IssueQueue.Author = restArgs[i+1]
			cfg.Mode = "queue"
			i++
		case arg == "--issue-limit" && i+1 < len(restArgs):
			if cfg.IssueQueue == nil {
				cfg.IssueQueue = &IssueQueueConfig{State: "open", Limit: 20, CloseOnSuccess: true}
			}
			limit, _ := strconv.Atoi(restArgs[i+1])
			cfg.IssueQueue.Limit = limit
			i++
		case arg == "--close-issues":
			if cfg.IssueQueue == nil {
				cfg.IssueQueue = &IssueQueueConfig{State: "open", Limit: 20, CloseOnSuccess: true}
			}
			cfg.IssueQueue.CloseOnSuccess = true
		case arg == "--no-close-issues":
			if cfg.IssueQueue == nil {
				cfg.IssueQueue = &IssueQueueConfig{State: "open", Limit: 20, CloseOnSuccess: true}
			}
			cfg.IssueQueue.CloseOnSuccess = false
		case arg == "--wizard":
			cfg.Wizard = true
		case arg == "--no-wizard":
			cfg.NoWizard = true
		case arg == "--yolo":
			cfg.Yolo = true
		case arg == "--no-yolo":
			cfg.Yolo = false
		case arg == "--dry-run":
			cfg.DryRun = true
		case arg == "--interactive" || arg == "-i":
			cfg.Interactive = true
		case arg == "--branch-prefix" && i+1 < len(restArgs):
			cfg.BranchPrefix = restArgs[i+1]
			i++
		case arg == "--base-branch" && i+1 < len(restArgs):
			cfg.BaseBranch = restArgs[i+1]
			i++
		case arg == "--no-pr":
			cfg.NoPR = true
		case arg == "--solo":
			cfg.Solo = true
			cfg.Steps = 1
			cfg.Commit.Enabled = false
			cfg.WaitForMerge = true
		case arg == "--worktree":
			cfg.Worktree = true
			if cfg.Mode == "" || cfg.Mode == "feature" {
				cfg.Mode = "queue"
			}
		case arg == "--wait-merge":
			cfg.WaitForMerge = true
		case arg == "--no-wait-merge":
			cfg.WaitForMerge = false
		case arg == "--keep-worktrees":
			cfg.KeepWorktrees = true
		case arg == "--merge-timeout" && i+1 < len(restArgs):
			mt, _ := strconv.Atoi(restArgs[i+1])
			if mt > 0 {
				cfg.MergeTimeoutMinutes = mt
			}
			i++
		case arg == "--step-timeout" && i+1 < len(restArgs):
			st, err := strconv.Atoi(restArgs[i+1])
			if err == nil {
				cfg.StepTimeoutMinutes = st // 0 disables
			}
			i++
		case arg == "--escalate-after" && i+1 < len(restArgs):
			ea, _ := strconv.Atoi(restArgs[i+1])
			if ea > 0 {
				cfg.EscalateAfter = ea
			}
			i++
		case arg == "--distinct-verifier":
			cfg.DistinctVerifier = true
		case arg == "--no-distinct-verifier":
			cfg.DistinctVerifier = false
		case arg == "--max-wall-minutes" && i+1 < len(restArgs):
			mw, _ := strconv.Atoi(restArgs[i+1])
			if mw > 0 {
				cfg.MaxWallMinutes = mw
			}
			i++
		case arg == "--state-dir" && i+1 < len(restArgs):
			cfg.StateDir = restArgs[i+1]
			i++
		case arg == "--notes-file" && i+1 < len(restArgs):
			cfg.NotesFile = restArgs[i+1]
			i++
		case arg == "--fix-unrelated-tests":
			cfg.FixUnrelatedTests = true
		case arg == "--stream-output":
			cfg.StreamOutput = true
		case arg == "--batch-size" && i+1 < len(restArgs):
			bs, _ := strconv.Atoi(restArgs[i+1])
			cfg.BatchSize = bs
			i++
		case strings.HasPrefix(arg, "--discover-"):
			parseStepOverride(&cfg.Discover, strings.TrimPrefix(arg, "--discover-"), &i, restArgs)
		case strings.HasPrefix(arg, "--execute-"):
			parseStepOverride(&cfg.Execute, strings.TrimPrefix(arg, "--execute-"), &i, restArgs)
		case strings.HasPrefix(arg, "--verify-"):
			parseStepOverride(&cfg.Verify, strings.TrimPrefix(arg, "--verify-"), &i, restArgs)
		case strings.HasPrefix(arg, "--commit-"):
			parseStepOverride(&cfg.Commit, strings.TrimPrefix(arg, "--commit-"), &i, restArgs)
		case arg == "--binary" && i+1 < len(restArgs):
			parts := strings.SplitN(restArgs[i+1], "=", 2)
			if len(parts) == 2 {
				cfg.BinaryOverrides[parts[0]] = parts[1]
			}
			i++
		default:
			if !strings.HasPrefix(arg, "-") && !cliCommands[arg] {
				promptArgs = append(promptArgs, arg)
			}
		}
	}

	if len(promptArgs) > 0 {
		promptText := strings.Join(promptArgs, " ")
		if cfg.Mission == "" {
			cfg.Mission = promptText
		}
	}

	if command == "quick" {
		cfg.Steps = 3
		cfg.Commit.Enabled = false
		cfg.Mode = "feature"
		cfg.Fresh = true
		if mission != "" {
			cfg.Mission = mission
		}
		if cfg.MaxIterations == 10 {
			cfg.MaxIterations = 5
		}
	}

	// Re-assert solo invariants last, so flag order (e.g. --solo then
	// --no-wait-merge, or quick mode overwriting Steps) can't leave solo half-set.
	normalizeSolo(cfg)

	return command, cfg, nil
}

func nilVal() string {
	return ""
}

func parseStepOverride(sc *StepConfig, field string, idx *int, args []string) {
	if *idx+1 >= len(args) {
		return
	}
	val := args[*idx+1]
	switch field {
	case "agent":
		sc.Agent = val
		*idx++
	case "model":
		sc.Model = val
		*idx++
	case "args":
		parts := strings.Split(val, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				sc.ExtraArgs = append(sc.ExtraArgs, trimmed)
			}
		}
		*idx++
	case "escalate":
		sc.Escalate = ParseAgentRefList(val)
		*idx++
	case "timeout":
		if t, err := strconv.Atoi(val); err == nil {
			sc.TimeoutMinutes = t
		}
		*idx++
	}
}
