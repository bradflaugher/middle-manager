package agy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
)

type agySession struct {
	cwd           string
	isFirstPrompt bool
	cancel        context.CancelFunc
}

type AgyACPAgent struct {
	conn     *acp.AgentSideConnection
	sessions map[string]*agySession
	mu       sync.Mutex
}

var (
	_ acp.Agent       = (*AgyACPAgent)(nil)
	_ acp.AgentLoader = (*AgyACPAgent)(nil)
)

func NewAgyACPAgent() *AgyACPAgent {
	return &AgyACPAgent{
		sessions: make(map[string]*agySession),
	}
}

func (a *AgyACPAgent) SetAgentConnection(conn *acp.AgentSideConnection) {
	a.conn = conn
}

func (a *AgyACPAgent) Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: false,
		},
		AgentInfo: &acp.Implementation{
			Name:    "agy-acp-go",
			Title:   acp.Ptr("Agy ACP Go Bridge"),
			Version: "1.0.0",
		},
	}, nil
}

func (a *AgyACPAgent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sid := a.randomID()
	a.mu.Lock()
	a.sessions[sid] = &agySession{
		cwd:           params.Cwd,
		isFirstPrompt: true,
	}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: acp.SessionId(sid)}, nil
}

func (a *AgyACPAgent) Authenticate(ctx context.Context, params acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *AgyACPAgent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	return acp.LoadSessionResponse{}, nil
}

func (a *AgyACPAgent) Logout(ctx context.Context, params acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (a *AgyACPAgent) ListSessions(ctx context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}

func (a *AgyACPAgent) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionResume)
}

func (a *AgyACPAgent) CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	delete(a.sessions, string(params.SessionId))
	a.mu.Unlock()
	return acp.CloseSessionResponse{}, nil
}

func (a *AgyACPAgent) SetSessionConfigOption(ctx context.Context, params acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}

func (a *AgyACPAgent) SetSessionMode(ctx context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (a *AgyACPAgent) Cancel(ctx context.Context, params acp.CancelNotification) error {
	a.mu.Lock()
	sess, ok := a.sessions[string(params.SessionId)]
	a.mu.Unlock()
	if ok && sess != nil && sess.cancel != nil {
		sess.cancel()
	}
	return nil
}

func (a *AgyACPAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	sid := string(params.SessionId)
	a.mu.Lock()
	sess, ok := a.sessions[sid]
	a.mu.Unlock()
	if !ok {
		return acp.PromptResponse{}, fmt.Errorf("session %s not found", sid)
	}

	a.mu.Lock()
	if sess.cancel != nil {
		prev := sess.cancel
		a.mu.Unlock()
		prev()
	} else {
		a.mu.Unlock()
	}

	pctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	sess.cancel = cancel
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		sess.cancel = nil
		a.mu.Unlock()
	}()

	// Extract prompt text
	promptText := ""
	for _, block := range params.Prompt {
		if block.Text != nil {
			promptText += block.Text.Text
		}
	}

	// Build agy command line
	// TODO: check in the future for agy acp support natively instead of this bridge
	cmdArgs := []string{"--dangerously-skip-permissions"}
	if sess.cwd != "" {
		cmdArgs = append(cmdArgs, "--add-dir", sess.cwd)
	}
	if !sess.isFirstPrompt {
		cmdArgs = append(cmdArgs, "--continue")
	}
	sess.isFirstPrompt = false
	cmdArgs = append(cmdArgs, "--print", promptText)

	cmd := exec.CommandContext(pctx, "agy", cmdArgs...)
	
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return acp.PromptResponse{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return acp.PromptResponse{}, err
	}

	if err := cmd.Start(); err != nil {
		return acp.PromptResponse{}, fmt.Errorf("start agy process: %w", err)
	}

	var accumulated []string
	var mu sync.Mutex

	// Read output streams concurrently and stream chunks to the client
	var wg sync.WaitGroup
	wg.Add(2)

	readStream := func(r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				mu.Lock()
				accumulated = append(accumulated, chunk)
				mu.Unlock()

				// Send update notification to client
				_ = a.conn.SessionUpdate(pctx, acp.SessionNotification{
					SessionId: acp.SessionId(sid),
					Update:    acp.UpdateAgentMessageText(chunk),
				})
			}
			if err != nil {
				break
			}
		}
	}

	go readStream(stdoutPipe)
	go readStream(stderrPipe)

	wg.Wait()
	_ = cmd.Wait()

	return acp.PromptResponse{
		StopReason: acp.StopReasonEndTurn,
	}, nil
}

func (a *AgyACPAgent) randomID() string {
	var b [12]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}
	return "sess_" + hex.EncodeToString(b[:])
}

// StartAgyACPBridge runs middle-manager's embedded agy-acp bridge
func StartAgyACPBridge() {
	ag := NewAgyACPAgent()
	asc := acp.NewAgentSideConnection(ag, os.Stdout, os.Stdin)
	asc.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ag.SetAgentConnection(asc)

	<-asc.Done()
}
