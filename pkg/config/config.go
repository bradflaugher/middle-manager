package config

import (
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

type StepConfig struct {
	Agent      string   `json:"agent"`
	Model      string   `json:"model"`
	ExtraArgs  []string `json:"extra_args"`
	PromptFile string   `json:"prompt_file"`
	Enabled    bool     `json:"enabled"`
}

type LoopConfig struct {
	Repo              string            `json:"repo"`
	Steps             int               `json:"steps"`
	MaxIterations     int               `json:"max_iterations"`
	Yolo              bool              `json:"yolo"`
	DryRun            bool              `json:"dry_run"`
	Interactive       bool              `json:"interactive"`
	Issue             string            `json:"issue"`
	Mode              string            `json:"mode"` // repair | issue | queue | feature
	Mission           string            `json:"mission"`
	Fresh             bool              `json:"fresh"`
	IssueQueue        *IssueQueueConfig `json:"issue_queue"`
	BranchPrefix      string            `json:"branch_prefix"`
	BaseBranch        string            `json:"base_branch"`
	NoPR              bool              `json:"no_pr"`
	NoMerge           bool              `json:"no_merge"`
	Discover          StepConfig        `json:"discover"`
	Execute           StepConfig        `json:"execute"`
	Verify            StepConfig        `json:"verify"`
	Commit            StepConfig        `json:"commit"`
	BinaryOverrides   map[string]string `json:"binary_overrides"`
	StateDir          string            `json:"state_dir"`
	AgentMemoryFile   string            `json:"agent_memory_file"`
	FixUnrelatedTests bool              `json:"fix_unrelated_tests"`
	StreamOutput      bool              `json:"stream_output"`
	BatchSize         int               `json:"batch_size"`

	// Interactive Wizard overrides
	Wizard   bool
	NoWizard bool
}

func NewDefaultConfig() *LoopConfig {
	return &LoopConfig{
		Steps:           4,
		MaxIterations:   10,
		Yolo:            true,
		BranchPrefix:    "mm",
		NoMerge:         true,
		AgentMemoryFile: "AGENTS.md",
		StreamOutput:    false,
		BatchSize:       1,
		Fresh:           true,
		BinaryOverrides:    make(map[string]string),
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
	case "execute":
		return &cfg.Execute
	case "verify":
		return &cfg.Verify
	case "commit":
		return &cfg.Commit
	default:
		return nil
	}
}

func (cfg *LoopConfig) ActiveSteps() []string {
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

func (cfg *LoopConfig) StatePath() string {
	var base string
	if cfg.StateDir != "" {
		base = cfg.StateDir
	} else {
		base = filepath.Join(cfg.Repo, ".middle-manager")
	}
	_ = os.MkdirAll(base, 0755)
	return base
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
	if v, ok := data["stream_output"].(bool); ok {
		cfg.StreamOutput = v
	}
	if v, ok := data["batch_size"].(float64); ok {
		cfg.BatchSize = int(v)
	}
	if v, ok := data["fix_unrelated_tests"].(bool); ok {
		cfg.FixUnrelatedTests = v
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
		}
	}

	if bOverrides, ok := data["binary_overrides"].(map[string]interface{}); ok {
		for k, v := range bOverrides {
			if s, ok := v.(string); ok {
				cfg.BinaryOverrides[k] = s
			}
		}
	}

	return cfg
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
		"run": true, "quick": true, "agents": true, "init": true, "status": true, "issues": true, "install-path": true,
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

	if configFilePath != "" {
		overrideMap, err := LoadJSONConfig(configFilePath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load config file: %w", err)
		}
		if overrideMap != nil {
			cfg = ConfigFromMap(overrideMap, cfg.Repo)
		}
	}

	// Now parse rest of CLI options
	for i := 0; i < len(restArgs); i++ {
		arg := restArgs[i]
		switch {
		case arg == "--steps" && i+1 < len(restArgs):
			steps, _ := strconv.Atoi(restArgs[i+1])
			cfg.Steps = steps
			if steps == 3 {
				cfg.Commit.Enabled = false
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
		case arg == "--state-dir" && i+1 < len(restArgs):
			cfg.StateDir = restArgs[i+1]
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
	}
}
