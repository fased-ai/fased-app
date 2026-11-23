package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"golang.org/x/term"
)

var (
	aiConnectionRemoteClient       = connectedRemoteClient
	aiConnectionDirectRemoteClient = core.NewRemoteClient
	aiConnectionOpenBrowser        = openLocalBrowser
)

type remoteSignInBridgeOpts struct {
	provider        string
	name            string
	googleProject   string
	noBrowser       bool
	passphraseStdin bool
	passphrase      string
}

func openLocalBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return errors.New("browser auto-open unsupported on this OS")
	}
	return cmd.Start()
}

func readPassphraseLine(in *os.File) (string, error) {
	if in == nil || term.IsTerminal(int(in.Fd())) {
		return "", errors.New("vault passphrase requires a private pipe, not an echoing terminal")
	}
	raw, err := io.ReadAll(io.LimitReader(in, 257))
	if err != nil {
		return "", err
	}
	defer clear(raw)
	if len(raw) > 256 {
		return "", errors.New("oversized vault passphrase")
	}
	passphrase := strings.TrimRight(string(raw), "\r\n")
	if strings.ContainsAny(passphrase, "\r\n") || strings.TrimSpace(passphrase) == "" {
		return "", errors.New("one single-line vault passphrase required on stdin")
	}
	return passphrase, nil
}

func validateRemoteLoopbackRedirect(signInURL string) (string, *url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(signInURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", nil, errors.New("remote sign-in URL must use HTTPS")
	}
	rawRedirect := strings.TrimSpace(parsed.Query().Get("redirect_uri"))
	if rawRedirect == "" {
		return "", nil, errors.New("remote sign-in URL missing redirect_uri")
	}
	ru, err := url.Parse(rawRedirect)
	if err != nil || ru.Scheme != "http" || ru.Hostname() != "127.0.0.1" || ru.Path != "/auth/callback" || ru.User != nil || ru.RawQuery != "" || ru.Fragment != "" {
		return "", nil, errors.New("remote redirect_uri must point to local loopback /auth/callback")
	}
	port, err := strconv.Atoi(ru.Port())
	if err != nil || port < 1024 || port > 65535 {
		return "", nil, errors.New("remote redirect_uri port out of range")
	}
	return rawRedirect, ru, nil
}

func cancelRemoteSignIn(client *core.Client, signInID string) {
	if client == nil || signInID == "" {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ignored core.AIConnectionView
	_ = client.Do(cancelCtx, http.MethodPost, "/api/v1/ai/connection", core.AIConnectionCommand{
		Action:   "sign-in-cancel",
		SignInID: signInID,
	}, &ignored)
}

func runRemoteSignInBridge(ctx context.Context, client *core.Client, opts remoteSignInBridgeOpts, in *os.File, out, errOut io.Writer) error {
	switch opts.provider {
	case "openai", "google", "openrouter":
	default:
		return errors.New("--sign-in must be openai, google, or openrouter")
	}
	if opts.googleProject != "" && opts.provider != "google" {
		return errors.New("--google-project requires --sign-in google")
	}
	if opts.passphraseStdin {
		if opts.provider == "openrouter" {
			return errors.New("--passphrase-stdin cannot be combined with interactive OpenRouter code input")
		}
		p, err := readPassphraseLine(in)
		if err != nil {
			return err
		}
		opts.passphrase = p
	}

	if opts.provider == "openrouter" {
		return runRemoteOpenRouterBridge(ctx, client, opts, in, out, errOut)
	}

	var (
		startView   core.AIConnectionView
		redirectURI string
		redirectURL *url.URL
		listener    net.Listener
	)
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var view core.AIConnectionView
		err := client.Do(ctx, http.MethodPost, "/api/v1/ai/connection", core.AIConnectionCommand{
			Action:        "sign-in-start",
			Provider:      opts.provider,
			Name:          opts.name,
			GoogleProject: opts.googleProject,
			Remote:        true,
		}, &view)
		if err != nil {
			return err
		}
		if view.SignIn == nil || view.SignIn.ID == "" || view.SignIn.URL == "" {
			return errors.New("remote core did not return a pending sign-in flow")
		}
		if !view.OSVaultAvailable && opts.passphrase == "" {
			cancelRemoteSignIn(client, view.SignIn.ID)
			return errors.New("remote host requires OS credential protection or --passphrase-stdin")
		}
		ruStr, ruParsed, valErr := validateRemoteLoopbackRedirect(view.SignIn.URL)
		if valErr != nil {
			cancelRemoteSignIn(client, view.SignIn.ID)
			return valErr
		}
		ln, listenErr := net.Listen("tcp4", ruParsed.Host)
		if listenErr != nil {
			cancelRemoteSignIn(client, view.SignIn.ID)
			if attempt == maxAttempts-1 {
				return fmt.Errorf("cannot bind local callback %s: %w", ruParsed.Host, listenErr)
			}
			continue
		}
		startView = view
		redirectURI = ruStr
		redirectURL = ruParsed
		listener = ln
		break
	}

	callbackCh := make(chan string, 1)
	doneRenderCh := make(chan error, 1)
	var handleOnce sync.Once

	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    8192,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
			if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" || r.Host != redirectURL.Host || len(r.URL.RawQuery) > 8192 {
				http.Error(w, "Invalid callback", http.StatusBadRequest)
				return
			}
			accepted := false
			handleOnce.Do(func() { accepted = true })
			if !accepted {
				http.Error(w, "Callback already handled", http.StatusConflict)
				return
			}
			fullCallback := redirectURI
			if r.URL.RawQuery != "" {
				fullCallback += "?" + r.URL.RawQuery
			}
			callbackCh <- fullCallback

			var completeErr error
			select {
			case completeErr = <-doneRenderCh:
			case <-time.After(20 * time.Second):
				completeErr = errors.New("timed out waiting for remote verification")
			case <-r.Context().Done():
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if completeErr != nil {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><title>Fased</title><body style=\"font-family:system-ui,sans-serif;background:#0d1014;color:#f3f5f8;display:grid;place-items:center;min-height:100vh;margin:0\"><p>Sign-in failed: %s. Return to terminal.</p></body>", html.EscapeString(completeErr.Error()))
				return
			}
			_, _ = io.WriteString(w, "<!doctype html><meta charset=\"utf-8\"><title>Fased</title><body style=\"font-family:system-ui,sans-serif;background:#0d1014;color:#f3f5f8;display:grid;place-items:center;min-height:100vh;margin:0\"><p>Connected to remote Fased — you can close this tab.</p></body>")
		}),
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- srv.Serve(listener)
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	_, _ = fmt.Fprintf(errOut, "Open sign-in URL in your browser:\n%s\nWaiting for local callback on http://%s/auth/callback ...\n", startView.SignIn.URL, redirectURL.Host)
	if !opts.noBrowser {
		_ = aiConnectionOpenBrowser(startView.SignIn.URL)
	}

	var callbackURL string
	select {
	case callbackURL = <-callbackCh:
	case serveErr := <-serveErrCh:
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return fmt.Errorf("local callback listener failed: %w", serveErr)
	case <-ctx.Done():
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return ctx.Err()
	}

	var doneView core.AIConnectionView
	completeErr := client.Do(ctx, http.MethodPost, "/api/v1/ai/connection", core.AIConnectionCommand{
		Action:     "sign-in-complete",
		SignInID:   startView.SignIn.ID,
		Callback:   callbackURL,
		Passphrase: opts.passphrase,
	}, &doneView)
	select {
	case doneRenderCh <- completeErr:
	default:
	}
	if completeErr != nil {
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return completeErr
	}
	return json.NewEncoder(out).Encode(doneView)
}

func runRemoteOpenRouterBridge(ctx context.Context, client *core.Client, opts remoteSignInBridgeOpts, in *os.File, out, errOut io.Writer) error {
	var startView core.AIConnectionView
	if err := client.Do(ctx, http.MethodPost, "/api/v1/ai/connection", core.AIConnectionCommand{
		Action:   "sign-in-start",
		Provider: "openrouter",
		Name:     opts.name,
		Remote:   true,
	}, &startView); err != nil {
		return err
	}
	if startView.SignIn == nil || startView.SignIn.ID == "" || startView.SignIn.URL == "" {
		return errors.New("remote core did not return a pending OpenRouter sign-in flow")
	}
	if !startView.OSVaultAvailable && opts.passphrase == "" {
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return errors.New("remote host requires OS credential protection")
	}
	_, _ = fmt.Fprintf(errOut, "Open OpenRouter sign-in URL in your browser:\n%s\n", startView.SignIn.URL)
	if !opts.noBrowser {
		_ = aiConnectionOpenBrowser(startView.SignIn.URL)
	}
	_, _ = fmt.Fprint(errOut, "Paste the callback code or URL from OpenRouter and press Enter: ")
	reader := bufio.NewReader(io.LimitReader(in, 8192))
	line, readErr := reader.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return readErr
	}
	code := strings.TrimSpace(line)
	if code == "" {
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return errors.New("no OpenRouter authorization code provided")
	}
	var doneView core.AIConnectionView
	if err := client.Do(ctx, http.MethodPost, "/api/v1/ai/connection", core.AIConnectionCommand{
		Action:     "sign-in-complete",
		SignInID:   startView.SignIn.ID,
		Callback:   code,
		Passphrase: opts.passphrase,
	}, &doneView); err != nil {
		cancelRemoteSignIn(client, startView.SignIn.ID)
		return err
	}
	return json.NewEncoder(out).Encode(doneView)
}
