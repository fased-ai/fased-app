package core

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type providerCLIAuthAttempt struct {
	view   CLIAuthView
	cancel context.CancelFunc
	kill   func()
	timer  *time.Timer
}

var (
	ansiCSIPattern  = regexp.MustCompile(`\x1b\[[0-9;?=]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][AB012]`)
	deviceCodeRegex = regexp.MustCompile(`\b[A-Z0-9]{4,5}-[A-Z0-9]{4,5}\b`)
	httpsURLRegex   = regexp.MustCompile(`https://[^\s"'<>()]+`)
)

func stripANSI(s string) string {
	return ansiCSIPattern.ReplaceAllString(s, "")
}

func parseDeviceAuthLine(raw string) (string, string) {
	line := stripANSI(raw)
	verificationURL := ""
	if match := httpsURLRegex.FindString(line); match != "" {
		cleaned := strings.TrimRight(match, ".,;:!?)]}>\"'")
		if u, err := url.Parse(cleaned); err == nil && u.Scheme == "https" && u.Host != "" {
			verificationURL = cleaned
		}
	}
	candidateText := line
	if verificationURL != "" {
		candidateText = strings.ReplaceAll(candidateText, verificationURL, " ")
	}
	userCode := ""
	for _, token := range deviceCodeRegex.FindAllString(candidateText, -1) {
		hasLetter := false
		for _, r := range token {
			if r >= 'A' && r <= 'Z' {
				hasLetter = true
				break
			}
		}
		if hasLetter {
			userCode = token
			break
		}
	}
	return verificationURL, userCode
}

func cliAuthCommandArgs(provider, binary string) ([]string, error) {
	switch provider {
	case "openai":
		return []string{"login", "--device-auth"}, nil
	case "xai":
		return []string{"login", "--device-auth"}, nil
	case "anthropic":
		return []string{"auth", "login"}, nil
	case "google":
		if binary == "antigravity" {
			return nil, errors.New("Antigravity ACP requires Google sign-in; run 'antigravity acp --login' in a terminal, then click Check again")
		}
		return nil, errors.New("Gemini CLI requires interactive terminal sign-in; run 'gemini' in a terminal, then click Check again")
	default:
		return nil, errors.New("unsupported CLI subscription provider")
	}
}

// Caller holds s.provider.mu.
func (s *Store) cliAuthViewLocked() *CLIAuthView {
	a := s.provider.cliAuth
	if a == nil {
		return nil
	}
	if a.view.ExpiresAt > 0 && a.view.ExpiresAt <= s.now().Unix() {
		s.cancelCLIAuthLocked()
		return nil
	}
	v := a.view
	return &v
}

// Caller holds s.provider.mu.
func (s *Store) cancelCLIAuthLocked() {
	a := s.provider.cliAuth
	if a == nil {
		return
	}
	s.provider.cliAuth = nil
	if a.timer != nil {
		a.timer.Stop()
	}
	if a.kill != nil {
		a.kill()
	}
	if a.cancel != nil {
		a.cancel()
	}
}

func (s *Store) startProviderCLIAuth(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	runner := s.cliRunner()
	spec, _, ok := resolveCLIConnectTarget(ctx, runner, c.Provider, c.Binary, "", "")
	if !ok {
		return AIConnectionView{}, errors.New("select a supported CLI subscription provider")
	}
	args, err := cliAuthCommandArgs(spec.Provider, spec.Binary)
	if err != nil {
		return AIConnectionView{}, err
	}
	binPath, err := runner.LookPath(spec.Binary)
	if err != nil || binPath == "" {
		return AIConnectionView{}, errors.New(spec.Label + " is not installed on this host")
	}

	id, err := randomToken()
	if err != nil {
		return AIConnectionView{}, err
	}

	s.provider.mu.Lock()
	if cur := s.cliAuthViewLocked(); cur != nil && (cur.State == "starting" || cur.State == "awaiting-code") {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("finish or cancel the pending CLI sign-in first")
	}
	s.cancelCLIAuthLocked()

	authCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	stdout, stderr, waitFn, killFn, startErr := runner.Start(authCtx, binPath, args, "")
	if startErr != nil {
		cancel()
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("cannot start " + spec.Label + " sign-in")
	}

	attempt := &providerCLIAuthAttempt{
		view: CLIAuthView{
			ID:        id,
			Provider:  spec.Provider,
			Binary:    spec.Binary,
			State:     "starting",
			ExpiresAt: s.now().Add(10 * time.Minute).Unix(),
		},
		cancel: cancel,
		kill:   killFn,
	}
	attempt.timer = time.AfterFunc(10*time.Minute, func() {
		s.provider.mu.Lock()
		if s.provider.cliAuth == attempt {
			s.cancelCLIAuthLocked()
		}
		s.provider.mu.Unlock()
		signalStoreChange(s)
	})
	s.provider.cliAuth = attempt
	out, viewErr := s.aiConnectionLocked(c.Ref)
	s.provider.mu.Unlock()

	signalStoreChange(s)
	go s.watchProviderCLIAuth(attempt, runner, spec, stdout, stderr, waitFn)
	_ = ctx
	return out, viewErr
}

func (s *Store) watchProviderCLIAuth(attempt *providerCLIAuthAttempt, runner providerCLIRunner, spec cliProviderSpec, stdout, stderr io.ReadCloser, waitFn func() error) {
	scanDone := make(chan string, 2)
	scanStream := func(r io.ReadCloser) {
		defer r.Close()
		var lastErrLine string
		scanner := bufio.NewScanner(io.LimitReader(r, 256*1024))
		scanner.Buffer(make([]byte, 4096), 64*1024)
		for scanner.Scan() {
			rawLine := scanner.Text()
			clean := sanitizeDiagValue(stripANSI(rawLine), 200)
			if clean != "" {
				lastErrLine = clean
			}
			vURL, uCode := parseDeviceAuthLine(rawLine)
			if vURL == "" && uCode == "" {
				continue
			}
			changed := false
			s.provider.mu.Lock()
			if s.provider.cliAuth == attempt && (attempt.view.State == "starting" || attempt.view.State == "awaiting-code") {
				if vURL != "" && attempt.view.VerificationURL != vURL {
					attempt.view.VerificationURL = vURL
					changed = true
				}
				if uCode != "" && attempt.view.UserCode != uCode {
					attempt.view.UserCode = uCode
					changed = true
				}
				if attempt.view.State != "awaiting-code" {
					attempt.view.State = "awaiting-code"
					changed = true
				}
			}
			s.provider.mu.Unlock()
			if changed {
				signalStoreChange(s)
			}
		}
		scanDone <- lastErrLine
	}

	if stdout != nil {
		go scanStream(stdout)
	} else {
		scanDone <- ""
	}
	if stderr != nil {
		go scanStream(stderr)
	} else {
		scanDone <- ""
	}

	l1 := <-scanDone
	l2 := <-scanDone
	waitErr := error(nil)
	if waitFn != nil {
		waitErr = waitFn()
	}
	if attempt.timer != nil {
		attempt.timer.Stop()
	}
	if attempt.cancel != nil {
		attempt.cancel()
	}

	if waitErr == nil {
		status := probeSingleCLI(context.Background(), runner, spec)
		s.provider.mu.Lock()
		for i, cur := range s.provider.cliCache {
			if cur.Provider == spec.Provider {
				s.provider.cliCache[i] = status
			}
		}
		if s.provider.cliAuth == attempt {
			if status.Authenticated {
				attempt.view.State = "authenticated"
				attempt.view.Message = status.Account
			} else {
				attempt.view.State = "failed"
				attempt.view.Message = "Sign-in finished without active credentials."
			}
		}
		s.provider.mu.Unlock()
		signalStoreChange(s)
		return
	}

	msg := l2
	if msg == "" {
		msg = l1
	}
	if msg == "" {
		msg = sanitizeDiagValue(waitErr.Error(), 160)
	}
	s.provider.mu.Lock()
	if s.provider.cliAuth == attempt {
		attempt.view.State = "failed"
		attempt.view.Message = msg
	}
	s.provider.mu.Unlock()
	signalStoreChange(s)
}

func (s *Store) cancelProviderCLIAuth(c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	a := s.provider.cliAuth
	if a == nil || (c.SignInID != "" && a.view.ID != c.SignInID) {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	s.cancelCLIAuthLocked()
	s.provider.mu.Unlock()
	signalStoreChange(s)
	return s.AIConnection(c.Ref)
}
