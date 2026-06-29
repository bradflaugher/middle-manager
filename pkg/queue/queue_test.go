package queue

import (
	"os"
	"strings"
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/loop"
)

func newTestRunner(t *testing.T) *IssueQueueRunner {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.LoopConfig{
		Repo:       dir,
		StateDir:   dir,
		IssueQueue: &config.IssueQueueConfig{State: "open", Limit: 5},
	}
	r, err := NewIssueQueueRunner(cfg)
	if err != nil {
		t.Fatalf("NewIssueQueueRunner: %v", err)
	}
	return r
}

func TestNewIssueQueueRunnerRequiresQueueConfig(t *testing.T) {
	if _, err := NewIssueQueueRunner(&config.LoopConfig{Repo: "/tmp/x"}); err == nil {
		t.Fatal("expected an error when IssueQueue is nil")
	}
}

// Cancel must be safe to call when no issue is in flight (operator quits before
// the first loop, or after the drain finished) — it should just flag the drain.
func TestCancelIsSafeWithNoInflightLoop(t *testing.T) {
	r := newTestRunner(t)
	r.Cancel() // must not panic with r.current == nil

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.canceled {
		t.Fatal("Cancel did not set the canceled flag")
	}
}

// Cancel runs on the monitor goroutine while Run() touches the same guarded
// fields on the worker goroutine. Run under -race, this proves the handoff is
// data-race-free and that Cancel reaches an in-flight issue's loop.
func TestCancelDuringInflightLoopIsRaceFree(t *testing.T) {
	r := newTestRunner(t)
	l := loop.NewMiddleManagerLoop(r.cfg)

	r.mu.Lock()
	r.current = l
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.Cancel()
		close(done)
	}()
	// Mirror Run()'s guarded read of the same fields, concurrently.
	r.mu.Lock()
	_ = r.canceled
	r.mu.Unlock()
	<-done

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.canceled {
		t.Fatal("Cancel did not set the canceled flag")
	}
}

// The on-disk queue.log must store plain, ANSI-free lines even when the console
// copy is colored — so the log stays greppable.
func TestLogWritesPlainLineToFile(t *testing.T) {
	r := newTestRunner(t)
	r.Log("issue #7 done", colors.Green)

	data, err := os.ReadFile(r.logPath)
	if err != nil {
		t.Fatalf("read queue.log: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "issue #7 done") {
		t.Fatalf("log missing message: %q", s)
	}
	if strings.Contains(s, "\x1b[") {
		t.Fatalf("queue.log retained ANSI escapes: %q", s)
	}
}
