package agents

import (
	"reflect"
	"testing"
)

func TestCleanAgentEnv(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=/some/path",
		"USER=root",
		"CLAUDE_CODE_SSE_PORT=1234",
		"CLAUDE_AGENT_SDK_VERSION=1.0.0",
		"OTHER_VAR=value",
	}

	expected := []string{
		"PATH=/usr/bin",
		"USER=root",
		"OTHER_VAR=value",
	}

	result := cleanAgentEnv(input)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("cleanAgentEnv() = %v, want %v", result, expected)
	}
}
