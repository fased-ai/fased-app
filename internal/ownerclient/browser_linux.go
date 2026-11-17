//go:build linux

package ownerclient

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

//go:embed browser_wallet.html
var walletPage string

//go:embed browser_wallet.js
var walletScript string

//go:embed browser_wallet.css
var walletStyle string

type ownerConnectionKey struct{}

type browserWallet struct {
	intent            signer.OwnerIntent
	root, host, token string
	owner             int
	mu                sync.Mutex
	finished          bool
	run               func(context.Context, browserInput) (browserResult, error)
}

type browserInput struct {
	Unlock, UnlockConfirm, BackupPassword, BackupConfirm string
	Seed, Recovery, Fingerprint                          string
}

type browserResult struct {
	Address      string `json:"address"`
	BackupFile   string `json:"backupFile"`
	Fingerprint  string `json:"fingerprint"`
	RecoveryHeld bool   `json:"recoveryHeld"`
}

// OpenLocalBrowser runs only as the OS owner and exposes a short-lived owner
// origin. It never forwards passwords or recovery material to the app service.
func OpenLocalBrowser(ctx context.Context, link string) error {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || signer.VerifyExecutable() != nil {
		return errors.New("open Wallets from the protected Local installation")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".local", "state", "fased", "owner-backups")
	intent, origin, err := ResumeLocal(root, link)
	if err != nil || origin != "" || intent.Version != 1 {
		return errors.New("the original Local wallet request is required")
	}
	pair, err := LocalOwnerPackage(intent)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil || self != pair.Core {
		return errors.New("open Wallets from the original protected package")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("cannot open the Local wallet")
	}
	defer listener.Close()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	w := &browserWallet{intent: intent, root: root, host: listener.Addr().String(), token: hex.EncodeToString(secret[:]), owner: os.Getuid()}
	w.run = w.enroll
	server := w.server()
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	launch := exec.Command("/usr/bin/xdg-open", "http://"+w.host+"/#"+w.token)
	if err = launch.Start(); err != nil {
		_ = server.Close()
		return errors.New("cannot open the Local wallet in your browser")
	}
	_ = launch.Process.Release()
	select {
	case <-ctx.Done():
		_ = server.Close()
		return nil
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("Local wallet connection stopped")
	}
}

func (w *browserWallet) server() *http.Server {
	return &http.Server{Handler: w, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 4 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			uid, err := browserPeerUID(conn)
			if err != nil {
				uid = -1
			}
			return context.WithValue(ctx, ownerConnectionKey{}, uid)
		}}
}

func (w *browserWallet) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	out.Header().Set("Cache-Control", "no-store")
	out.Header().Set("X-Content-Type-Options", "nosniff")
	out.Header().Set("Referrer-Policy", "no-referrer")
	out.Header().Set("X-Frame-Options", "DENY")
	out.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; object-src 'none'")
	uid, ok := r.Context().Value(ownerConnectionKey{}).(int)
	if !ok || uid != w.owner || r.Host != w.host || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		http.Error(out, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		out.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(out, walletPage)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/wallet.js" {
		out.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(out, walletScript)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/wallet.css" {
		out.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(out, walletStyle)
		return
	}
	if len(r.Header.Values("X-Fased-Owner")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Fased-Owner")), []byte(w.token)) != 1 || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != "http://"+w.host {
		http.Error(out, "forbidden", http.StatusForbidden)
		return
	}
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodPost && r.URL.Path == "/info" && r.Header.Get("Content-Type") == "application/json" {
		_ = json.NewEncoder(out).Encode(map[string]string{"action": w.intent.Action, "network": w.intent.Network, "instance": w.intent.InstanceID})
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/enroll" || r.Header.Get("Content-Type") != "application/json" {
		http.Error(out, "not found", http.StatusNotFound)
		return
	}
	if !w.mu.TryLock() {
		http.Error(out, "wallet operation is already running", http.StatusConflict)
		return
	}
	defer w.mu.Unlock()
	if w.finished {
		http.Error(out, "wallet operation already completed", http.StatusConflict)
		return
	}
	var input browserInput
	decoder := json.NewDecoder(http.MaxBytesReader(out, r.Body, signer.MaxOwnerInput))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Unlock != input.UnlockConfirm || input.BackupPassword != input.BackupConfirm {
		http.Error(out, "check the wallet input", http.StatusBadRequest)
		return
	}
	result, err := w.run(r.Context(), input)
	if err != nil {
		http.Error(out, "Wallets did not finish. Reopen the same request to recover it.", http.StatusConflict)
		return
	}
	w.finished = true
	_ = json.NewEncoder(out).Encode(result)
}

func (w *browserWallet) enroll(ctx context.Context, input browserInput) (browserResult, error) {
	var result browserResult
	pair, err := LocalOwnerPackage(w.intent)
	if err != nil {
		return result, err
	}
	in := signer.OwnerInput{Intent: w.intent, SignerID: filepath.Base(pair.LocalPaths.Root), Phase: "prepare", Unlock: []byte(input.Unlock), BackupPassword: []byte(input.BackupPassword)}
	defer in.Clear()
	if w.intent.Action == "import" {
		in.Seed, err = hex.DecodeString(strings.TrimSpace(input.Seed))
		if err != nil {
			return result, err
		}
	} else if input.Seed != "" {
		return result, errors.New("unexpected seed")
	}
	if w.intent.Action == "recover" {
		in.Backup, err = signer.RecoveryDocument([]byte(input.Recovery), input.Fingerprint, w.intent.Network)
		if err != nil {
			return result, err
		}
		in.Fingerprint = input.Fingerprint
	} else if input.Recovery != "" || input.Fingerprint != "" {
		return result, errors.New("unexpected recovery input")
	}
	if err = in.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	client, err := Open(ctx, w.intent, "", "")
	var plan *Enrollment
	if errors.Is(err, ErrEnrollmentRequired) {
		plan, err = PrepareEnrollment(ctx, w.intent, "", "", pair)
		if err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	}
	// Initialization changes the protected journal. Persist its identity first,
	// so another action cannot replace an uncertain operation after a crash.
	if err = MarkDispatched(w.root, w.intent); err != nil {
		return result, err
	}
	if plan != nil {
		var packageSetup bool
		packageSetup, err = plan.RunPackage(ctx)
		if err != nil || !packageSetup {
			return result, errors.New("the protected package did not initialize")
		}
		client, err = plan.Wait(ctx)
	}
	if err != nil || client.Peer.SignerID != in.SignerID || client.Peer.Provenance != signer.OwnerKeyProvenance {
		return result, errors.New("the original owner signer is unavailable")
	}
	prepared, err := client.Exchange(ctx, in)
	if err != nil {
		return result, err
	}
	path, saved, err := SaveBackup(w.root, prepared)
	if err != nil {
		return result, err
	}
	clear(in.Backup)
	in.Phase, in.Backup, in.Fingerprint = "verify", saved, prepared.Fingerprint
	verified, err := client.Exchange(ctx, in)
	if err != nil || !verified.BackupVerified || verified.Status.PublicIdentity == nil {
		return result, errors.New("backup verification is incomplete")
	}
	return browserResult{Address: verified.Status.PublicIdentity.Address, BackupFile: path, Fingerprint: verified.Fingerprint, RecoveryHeld: verified.Status.RecoveryRequired}, nil
}
