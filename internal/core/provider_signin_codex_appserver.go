package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/providers"
)

const (
	codexAppServerMaxSessions = 4
	codexAppServerIdleTTL     = 3 * time.Minute
	codexShadowConfigMaxBytes = providers.CodexShadowConfigMaxBytes
)

type codexAppServerTurnRequest struct {
	BinPath      string
	Cred         cliProviderCredential
	Model        string
	SystemPrompt string
	Prompt       string
	ExtraArgs    []string
	ExtraEnv     []string
	OnTextDelta  func(string)
}

type codexAppServerTurnResult struct {
	Text         string
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type codexRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *codexRPCError  `json:"error,omitempty"`
}

type codexRPCFrame struct {
	env codexRPCEnvelope
	err error
}

type codexAppServerKey struct {
	runnerID   string
	binPath    string
	binaryPath string
	configDir  string
	shadowHome string
	launchArgs string
}

type codexAppServerSession struct {
	key        codexAppServerKey
	gen        uint64
	stdin      io.WriteCloser
	waitFn     func() error
	killFn     func()
	cancel     context.CancelFunc
	frames     chan codexRPCFrame
	closedCh   chan struct{}
	readerDone chan struct{}
	stderrDone chan struct{}

	writeMu   sync.Mutex
	stderrMu  sync.Mutex
	stderrBuf bytes.Buffer
	closeOnce sync.Once
	nextID    int64

	inUse     bool
	lastUsed  time.Time
	idleTimer *time.Timer
}

type codexAppServerPool struct {
	mu       sync.Mutex
	gen      uint64
	sessions []*codexAppServerSession
}

var defaultCodexAppServerPool = &codexAppServerPool{}

func parseSafeCLILaunchArgs(raw string) ([]string, error) {
	return providers.ParseSafeCLILaunchArgs(raw)
}

func prepareCodexShadowHome(configDir, shadowHome string) (string, error) {
	return providers.PrepareCodexShadowHome(configDir, shadowHome)
}

func evictCodexAppServerSessions() {
	defaultCodexAppServerPool.evictAll()
}

func (p *codexAppServerPool) evictAll() {
	p.mu.Lock()
	p.gen++
	victims := make([]*codexAppServerSession, 0, len(p.sessions))
	kept := make([]*codexAppServerSession, 0, len(p.sessions))
	for _, s := range p.sessions {
		if s.idleTimer != nil {
			s.idleTimer.Stop()
			s.idleTimer = nil
		}
		if s.inUse {
			kept = append(kept, s)
		} else {
			victims = append(victims, s)
		}
	}
	p.sessions = kept
	p.mu.Unlock()

	for _, s := range victims {
		s.close()
	}
}

func (p *codexAppServerPool) checkout(key codexAppServerKey) (*codexAppServerSession, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.sessions) - 1; i >= 0; i-- {
		s := p.sessions[i]
		if s.inUse || s.key != key {
			continue
		}
		select {
		case <-s.readerDone:
			p.sessions = append(p.sessions[:i], p.sessions[i+1:]...)
			if s.idleTimer != nil {
				s.idleTimer.Stop()
				s.idleTimer = nil
			}
			go s.close()
			continue
		default:
		}
		if s.idleTimer != nil {
			s.idleTimer.Stop()
			s.idleTimer = nil
		}
		s.inUse = true
		return s, p.gen
	}
	return nil, p.gen
}

func (p *codexAppServerPool) registerAndCheckout(s *codexAppServerSession, gen uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen != gen {
		return false
	}
	s.inUse = true
	p.sessions = append(p.sessions, s)
	return true
}

func (p *codexAppServerPool) release(s *codexAppServerSession, healthy bool) {
	var toClose []*codexAppServerSession

	p.mu.Lock()
	if !healthy || s.gen != p.gen || s.isDead() {
		p.removeLocked(s)
		p.mu.Unlock()
		s.close()
		return
	}
	found := false
	for _, cur := range p.sessions {
		if cur == s {
			found = true
			break
		}
	}
	if !found {
		p.mu.Unlock()
		s.close()
		return
	}
	s.inUse = false
	s.lastUsed = time.Now()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	gen := s.gen
	s.idleTimer = time.AfterFunc(codexAppServerIdleTTL, func() {
		p.evictIdle(s, gen)
	})
	for len(p.sessions) > codexAppServerMaxSessions {
		victim := p.oldestIdleLocked(s)
		if victim == nil {
			break
		}
		p.removeLocked(victim)
		toClose = append(toClose, victim)
	}
	p.mu.Unlock()

	for _, victim := range toClose {
		victim.close()
	}
}

func (p *codexAppServerPool) evictIdle(target *codexAppServerSession, gen uint64) {
	p.mu.Lock()
	if p.gen != gen || target.inUse {
		p.mu.Unlock()
		return
	}
	if !p.removeLocked(target) {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	target.close()
}

func (p *codexAppServerPool) oldestIdleLocked(exclude *codexAppServerSession) *codexAppServerSession {
	var oldest *codexAppServerSession
	for _, s := range p.sessions {
		if s.inUse || s == exclude {
			continue
		}
		if oldest == nil || s.lastUsed.Before(oldest.lastUsed) {
			oldest = s
		}
	}
	return oldest
}

func (p *codexAppServerPool) removeLocked(target *codexAppServerSession) bool {
	for i, s := range p.sessions {
		if s == target {
			if s.idleTimer != nil {
				s.idleTimer.Stop()
				s.idleTimer = nil
			}
			p.sessions = append(p.sessions[:i], p.sessions[i+1:]...)
			return true
		}
	}
	return false
}

func codexRunnerIdentity(runner providerCLIRunner) string {
	if _, isOS := runner.(osCLIRunner); isOS {
		return "os"
	}
	return fmt.Sprintf("%T:%p", runner, runner)
}

func spawnCodexAppServerSession(runner interactiveCLIRunner, key codexAppServerKey, gen uint64, extraArgs, extraEnv []string) (*codexAppServerSession, error) {
	sessCtx, cancel := context.WithCancel(context.Background())
	args := make([]string, 0, 2+len(extraArgs))
	args = append(args, "app-server", "--stdio")
	args = append(args, extraArgs...)

	var launchEnv []string
	if len(extraEnv) > 0 {
		launchEnv = mergeCLIEnv(safeCLIEnv(), extraEnv)
	}

	stdin, stdout, stderr, waitFn, killFn, err := runner.StartInteractive(sessCtx, key.binPath, args, launchEnv)
	if err != nil {
		cancel()
		return nil, err
	}

	s := &codexAppServerSession{
		key:        key,
		gen:        gen,
		stdin:      stdin,
		waitFn:     waitFn,
		killFn:     killFn,
		cancel:     cancel,
		frames:     make(chan codexRPCFrame, 64),
		closedCh:   make(chan struct{}),
		readerDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
	}

	go func() {
		defer close(s.stderrDone)
		if stderr == nil {
			return
		}
		defer stderr.Close()
		buf := make([]byte, 2048)
		for {
			n, rErr := stderr.Read(buf)
			if n > 0 {
				s.stderrMu.Lock()
				if s.stderrBuf.Len() < 8192 {
					rem := 8192 - s.stderrBuf.Len()
					if n > rem {
						n = rem
					}
					_, _ = s.stderrBuf.Write(buf[:n])
				}
				s.stderrMu.Unlock()
			}
			if rErr != nil {
				return
			}
		}
	}()

	go func() {
		defer close(s.readerDone)
		if stdout == nil {
			s.pushFrame(codexRPCFrame{err: io.EOF})
			return
		}
		defer stdout.Close()
		reader := bufio.NewReaderSize(stdout, 64*1024)
		for {
			line, rErr := reader.ReadBytes('\n')
			if len(line) > 2*1024*1024 {
				s.pushFrame(codexRPCFrame{err: errors.New("codex app-server message exceeded frame limit")})
				return
			}
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				var env codexRPCEnvelope
				if json.Unmarshal(trimmed, &env) == nil {
					if !s.pushFrame(codexRPCFrame{env: env}) {
						return
					}
				}
			}
			if rErr != nil {
				s.pushFrame(codexRPCFrame{err: rErr})
				return
			}
		}
	}()

	return s, nil
}

func (s *codexAppServerSession) pushFrame(f codexRPCFrame) bool {
	select {
	case s.frames <- f:
		return true
	case <-s.closedCh:
		return false
	}
}

func (s *codexAppServerSession) isDead() bool {
	select {
	case <-s.closedCh:
		return true
	case <-s.readerDone:
		return true
	default:
		return false
	}
}

func (s *codexAppServerSession) stderrDiag() string {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	return sanitizeDiagValue(stripANSI(s.stderrBuf.String()), 240)
}

func (s *codexAppServerSession) close() {
	s.closeOnce.Do(func() {
		close(s.closedCh)
		if s.cancel != nil {
			s.cancel()
		}
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.killFn != nil {
			s.killFn()
		}
		if s.waitFn != nil {
			_ = s.waitFn()
		}
		<-s.readerDone
		<-s.stderrDone
	})
}

func (s *codexAppServerSession) sendJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stdin == nil {
		return errors.New("app-server stdin closed")
	}
	_, err = s.stdin.Write(append(raw, '\n'))
	return err
}

func (s *codexAppServerSession) sendDenyServerRequest(env codexRPCEnvelope) error {
	return s.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(env.ID),
		"error": map[string]any{
			"code":    -32601,
			"message": "host tool or approval execution is disabled under non-custodial app-server policy",
		},
	})
}

func (s *codexAppServerSession) call(ctx context.Context, method string, params any, onNote func(codexRPCEnvelope)) (codexRPCEnvelope, error) {
	s.nextID++
	wantID := fmt.Sprintf("%d", s.nextID)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      s.nextID,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	if err := s.sendJSON(req); err != nil {
		return codexRPCEnvelope{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return codexRPCEnvelope{}, ctx.Err()
		case frame := <-s.frames:
			if frame.err != nil {
				return codexRPCEnvelope{}, frame.err
			}
			env := frame.env
			if env.Method != "" && len(env.ID) > 0 && string(env.ID) != "null" {
				_ = s.sendDenyServerRequest(env)
				continue
			}
			if env.Method != "" {
				if onNote != nil {
					onNote(env)
				}
				continue
			}
			if strings.TrimSpace(string(env.ID)) == wantID {
				return env, nil
			}
		}
	}
}

func (s *codexAppServerSession) initialize(ctx context.Context) error {
	initResp, err := s.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "fased",
			"version": "0.1.36",
		},
	}, nil)
	if err != nil {
		return err
	}
	if initResp.Error != nil {
		return errors.New(sanitizeDiagValue(initResp.Error.Message, 180))
	}
	return s.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialized",
	})
}

func runCodexAppServerTurn(ctx context.Context, runner providerCLIRunner, req codexAppServerTurnRequest) (codexAppServerTurnResult, bool, error) {
	interactive, ok := runner.(interactiveCLIRunner)
	if !ok {
		return codexAppServerTurnResult{}, false, errors.New("interactive runner unavailable")
	}

	if req.Cred.ShadowHome != "" {
		if _, isOS := runner.(osCLIRunner); isOS {
			if _, err := prepareCodexShadowHome(req.Cred.ConfigDir, req.Cred.ShadowHome); err != nil {
				return codexAppServerTurnResult{}, true, err
			}
		}
	}

	turnCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	key := codexAppServerKey{
		runnerID:   codexRunnerIdentity(runner),
		binPath:    req.BinPath,
		binaryPath: req.Cred.BinaryPath,
		configDir:  req.Cred.ConfigDir,
		shadowHome: req.Cred.ShadowHome,
		launchArgs: strings.Join(req.ExtraArgs, "\x00"),
	}

	sess, gen := defaultCodexAppServerPool.checkout(key)
	if sess == nil {
		fresh, spawnErr := spawnCodexAppServerSession(interactive, key, gen, req.ExtraArgs, req.ExtraEnv)
		if spawnErr != nil {
			return codexAppServerTurnResult{}, false, spawnErr
		}
		initCtx, initCancel := context.WithTimeout(turnCtx, 15*time.Second)
		initErr := fresh.initialize(initCtx)
		initCancel()
		if initErr != nil {
			fresh.close()
			if ctx.Err() != nil {
				return codexAppServerTurnResult{}, true, ctx.Err()
			}
			return codexAppServerTurnResult{}, false, initErr
		}
		if !defaultCodexAppServerPool.registerAndCheckout(fresh, gen) {
			fresh.close()
			return codexAppServerTurnResult{}, false, errors.New("codex app-server pool invalidated")
		}
		sess = fresh
	}

	healthy := false
	defer func() {
		defaultCodexAppServerPool.release(sess, healthy)
	}()

	threadParams := map[string]any{
		"approvalPolicy": "untrusted",
		"sandbox":        "read-only",
		"ephemeral":      true,
	}
	if req.Model != "" {
		threadParams["model"] = req.Model
	}
	if req.SystemPrompt != "" {
		threadParams["developerInstructions"] = req.SystemPrompt
	}

	threadResp, err := sess.call(turnCtx, "thread/start", threadParams, nil)
	if err != nil {
		if ctx.Err() != nil {
			return codexAppServerTurnResult{}, true, ctx.Err()
		}
		return codexAppServerTurnResult{}, false, err
	}
	if threadResp.Error != nil {
		return codexAppServerTurnResult{}, false, errors.New(sanitizeDiagValue(threadResp.Error.Message, 180))
	}
	var threadRes struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(threadResp.Result, &threadRes) != nil || threadRes.Thread.ID == "" {
		return codexAppServerTurnResult{}, false, errors.New("codex app-server returned no thread id")
	}
	threadID := threadRes.Thread.ID

	var fullText strings.Builder
	var result codexAppServerTurnResult
	seenByItem := map[string]string{}
	emittedDelta := false
	completed := false
	turnStatus := ""
	turnErrMsg := ""
	lastErrMsg := ""
	var turnID string

	handleNotification := func(env codexRPCEnvelope) {
		switch env.Method {
		case "item/agentMessage/delta":
			var p struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				ItemID   string `json:"itemId"`
				Delta    string `json:"delta"`
			}
			if json.Unmarshal(env.Params, &p) != nil {
				return
			}
			if p.ThreadID != "" && p.ThreadID != threadID {
				return
			}
			if turnID != "" && p.TurnID != "" && p.TurnID != turnID {
				return
			}
			if p.Delta != "" {
				emittedDelta = true
				itemKey := p.ItemID
				if itemKey == "" {
					itemKey = "_default"
				}
				seenByItem[itemKey] += p.Delta
				fullText.WriteString(p.Delta)
				if req.OnTextDelta != nil {
					req.OnTextDelta(p.Delta)
				}
			}

		case "item/completed":
			var p struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Item     *struct {
					ID   string `json:"id"`
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
			}
			if json.Unmarshal(env.Params, &p) != nil || p.Item == nil {
				return
			}
			if p.ThreadID != "" && p.ThreadID != threadID {
				return
			}
			if turnID != "" && p.TurnID != "" && p.TurnID != turnID {
				return
			}
			if p.Item.Type == "agentMessage" || p.Item.Type == "agent_message" || p.Item.Type == "message" {
				if p.Item.Text == "" {
					return
				}
				itemKey := p.Item.ID
				if itemKey == "" {
					itemKey = "_default"
				}
				prev := seenByItem[itemKey]
				if strings.HasPrefix(p.Item.Text, prev) {
					delta := p.Item.Text[len(prev):]
					if delta != "" {
						emittedDelta = true
						seenByItem[itemKey] = p.Item.Text
						fullText.WriteString(delta)
						if req.OnTextDelta != nil {
							req.OnTextDelta(delta)
						}
					}
				} else if prev == "" {
					emittedDelta = true
					seenByItem[itemKey] = p.Item.Text
					fullText.WriteString(p.Item.Text)
					if req.OnTextDelta != nil {
						req.OnTextDelta(p.Item.Text)
					}
				}
			}

		case "thread/tokenUsage/updated":
			var p struct {
				ThreadID   string `json:"threadId"`
				TurnID     string `json:"turnId"`
				TokenUsage struct {
					Last *struct {
						InputTokens       int64 `json:"inputTokens"`
						CachedInputTokens int64 `json:"cachedInputTokens"`
						OutputTokens      int64 `json:"outputTokens"`
					} `json:"last"`
					Total *struct {
						InputTokens       int64 `json:"inputTokens"`
						CachedInputTokens int64 `json:"cachedInputTokens"`
						OutputTokens      int64 `json:"outputTokens"`
					} `json:"total"`
				} `json:"tokenUsage"`
			}
			if json.Unmarshal(env.Params, &p) != nil {
				return
			}
			if p.ThreadID != "" && p.ThreadID != threadID {
				return
			}
			if last := p.TokenUsage.Last; last != nil && (last.InputTokens > 0 || last.CachedInputTokens > 0 || last.OutputTokens > 0) {
				result.InputTokens = last.InputTokens
				result.CachedTokens = last.CachedInputTokens
				result.OutputTokens = last.OutputTokens
			} else if total := p.TokenUsage.Total; total != nil {
				result.InputTokens = total.InputTokens
				result.CachedTokens = total.CachedInputTokens
				result.OutputTokens = total.OutputTokens
			}

		case "error":
			var p struct {
				ThreadID string `json:"threadId"`
				Message  string `json:"message"`
				Error    *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(env.Params, &p) == nil && (p.ThreadID == "" || p.ThreadID == threadID) {
				if p.Error != nil && p.Error.Message != "" {
					lastErrMsg = sanitizeDiagValue(p.Error.Message, 200)
				} else if p.Message != "" {
					lastErrMsg = sanitizeDiagValue(p.Message, 200)
				}
			}

		case "turn/completed":
			var p struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(env.Params, &p) != nil {
				return
			}
			if p.ThreadID != "" && p.ThreadID != threadID {
				return
			}
			if turnID != "" && p.Turn.ID != "" && p.Turn.ID != turnID {
				return
			}
			turnStatus = p.Turn.Status
			if p.Turn.Error != nil && p.Turn.Error.Message != "" {
				turnErrMsg = sanitizeDiagValue(p.Turn.Error.Message, 200)
			}
			completed = true
		}
	}

	turnParams := map[string]any{
		"threadId":       threadID,
		"approvalPolicy": "untrusted",
		"input": []map[string]any{
			{"type": "text", "text": req.Prompt},
		},
	}
	if req.Model != "" {
		turnParams["model"] = req.Model
	}

	turnResp, err := sess.call(turnCtx, "turn/start", turnParams, handleNotification)
	if err != nil {
		if ctx.Err() != nil {
			return codexAppServerTurnResult{}, true, ctx.Err()
		}
		if !emittedDelta {
			return codexAppServerTurnResult{}, false, err
		}
		return codexAppServerTurnResult{}, true, err
	}
	if turnResp.Error != nil {
		errOut := errors.New(sanitizeDiagValue(turnResp.Error.Message, 180))
		if !emittedDelta {
			return codexAppServerTurnResult{}, false, errOut
		}
		return codexAppServerTurnResult{}, true, errOut
	}
	var turnRes struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(turnResp.Result, &turnRes) == nil {
		turnID = turnRes.Turn.ID
	}

	for !completed {
		select {
		case <-turnCtx.Done():
			if turnID != "" {
				sess.nextID++
				interruptID := sess.nextID
				sent := make(chan struct{})
				go func() {
					_ = sess.sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"id":      interruptID,
						"method":  "turn/interrupt",
						"params": map[string]any{
							"threadId": threadID,
							"turnId":   turnID,
						},
					})
					close(sent)
				}()
				select {
				case <-sent:
				case <-time.After(100 * time.Millisecond):
				}
			}
			if ctx.Err() != nil {
				return codexAppServerTurnResult{}, true, ctx.Err()
			}
			return codexAppServerTurnResult{}, true, turnCtx.Err()

		case frame := <-sess.frames:
			if frame.err != nil {
				if ctx.Err() != nil {
					return codexAppServerTurnResult{}, true, ctx.Err()
				}
				diag := sess.stderrDiag()
				if !emittedDelta {
					if diag != "" {
						return codexAppServerTurnResult{}, false, errors.New(diag)
					}
					return codexAppServerTurnResult{}, false, frame.err
				}
				if diag != "" {
					return codexAppServerTurnResult{}, true, errors.New("codex failed: " + diag)
				}
				return codexAppServerTurnResult{}, true, errors.New("codex execution failed")
			}
			env := frame.env
			if env.Method != "" && len(env.ID) > 0 && string(env.ID) != "null" {
				_ = sess.sendDenyServerRequest(env)
				continue
			}
			if env.Method != "" {
				handleNotification(env)
			}
		}
		if fullText.Len() > 1024*1024 {
			break
		}
	}

	if ctx.Err() != nil {
		return codexAppServerTurnResult{}, true, ctx.Err()
	}
	if turnStatus == "interrupted" && strings.TrimSpace(fullText.String()) == "" {
		return codexAppServerTurnResult{}, true, errors.New("codex turn was interrupted")
	}
	if turnStatus == "failed" {
		msg := turnErrMsg
		if msg == "" {
			msg = lastErrMsg
		}
		if msg == "" {
			msg = sess.stderrDiag()
		}
		if msg == "" {
			msg = "codex execution failed"
		}
		return codexAppServerTurnResult{}, true, errors.New(msg)
	}

	result.Text = strings.TrimSpace(fullText.String())
	if result.Text == "" {
		healthy = true
		return codexAppServerTurnResult{}, true, errors.New("codex returned an empty reply")
	}
	healthy = true
	return result, true, nil
}
