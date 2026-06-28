package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/config"
)

// TestInterjectionRoundTrip proves the monitor captures a typed instruction and
// hands it to the loop exactly once (consumed on read, then cleared). This is
// the "queued, applied to the next step" path — not mid-agent steering.
func TestInterjectionRoundTrip(t *testing.T) {
	GlobalModel = NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})

	GlobalModel.textInput.SetValue("focus on error handling")
	GlobalModel.handleInput()

	if got := GetTUIInterjection(); got != "focus on error handling" {
		t.Fatalf("first read = %q, want the queued instruction", got)
	}
	if got := GetTUIInterjection(); got != "" {
		t.Fatalf("second read = %q, want empty (already consumed)", got)
	}
}

func TestSlashCommands(t *testing.T) {
	GlobalModel = NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})

	// A slash command must NOT be treated as an interjection.
	GlobalModel.textInput.SetValue("/pause")
	GlobalModel.handleInput()
	if !IsTUIPaused() {
		t.Fatal("/pause did not pause")
	}
	if got := GetTUIInterjection(); got != "" {
		t.Fatalf("/pause leaked into interjection: %q", got)
	}

	GlobalModel.textInput.SetValue("/resume")
	GlobalModel.handleInput()
	if IsTUIPaused() {
		t.Fatal("/resume did not resume")
	}

	GlobalModel.textInput.SetValue("/skip")
	GlobalModel.handleInput()
	if !IsTUISkipStep() {
		t.Fatal("/skip did not set skip")
	}
	if IsTUISkipStep() {
		t.Fatal("/skip should be a one-shot flag (cleared after read)")
	}

	// RequestPause is how interactive mode pauses before the next step.
	RequestPause()
	if !IsTUIPaused() {
		t.Fatal("RequestPause did not pause")
	}
}

// TestInterjectionNoTUIIsNoop documents that without an active monitor (e.g.
// --stream-output mode) interjection is simply disabled, not broken.
func TestInterjectionNoTUIIsNoop(t *testing.T) {
	GlobalModel = nil
	if got := GetTUIInterjection(); got != "" {
		t.Fatalf("expected empty interjection with no TUI, got %q", got)
	}
	if IsTUIPaused() || IsTUISkipStep() || IsTUIQuitting() {
		t.Fatal("expected all controls inert with no TUI")
	}
}

func TestLiveLogLeftAlignmentPreservesIndentation(t *testing.T) {
	m := NewMonitorModel(&config.LoopConfig{Repo: "/tmp/x"})

	m.pushLog(renderLiveLog("first line\n", false))
	m.pushLog(renderLiveLog("  indented child\n", false))
	m.pushLog(renderLiveLog("\tcode block\n", true))

	content := stripANSITest(m.logViewport.GetContent())
	if strings.Contains(content, "first line          indented child") {
		t.Fatalf("live log lines were joined by lipgloss padding: %q", content)
	}
	if !strings.Contains(content, "first line\n  indented child\n    code block\n") {
		t.Fatalf("live log did not preserve left alignment and indentation: %q", content)
	}
}

var ansiTestRe = regexp.MustCompile(`\x1B(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~]|[@-Z\\-_])`)

func stripANSITest(text string) string {
	return ansiTestRe.ReplaceAllString(text, "")
}
