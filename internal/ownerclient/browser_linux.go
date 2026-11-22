//go:build linux

package ownerclient

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
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
	"github.com/fased-ai/fased-app/internal/update"
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
	remote            *Client
	remotePeer        Peer
	root, host, token string
	owner             int
	mu                sync.Mutex
	finished          bool
	run               func(context.Context, browserInput) (browserResult, error)
}

type browserInput struct {
	Unlock, UnlockConfirm, BackupPassword, BackupConfirm string
	Seed, Recovery, RecoveryBase64, Fingerprint          string
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
	return serveWalletBrowser(ctx, &browserWallet{intent: intent, root: root, owner: os.Getuid()})
}

// OpenRemoteBrowser uses the same owner-only browser and key exchange as
// Local. The public link supplies only workspace intent. The OS account and
// protected package identifier are selected by the user on this computer.
func OpenRemoteBrowser(ctx context.Context, link, user, packageID string) error {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || signer.VerifyExecutable() != nil || !update.IDOK(packageID) {
		return errors.New("the protected Remote Wallets client requires its ordinary owner and exact package")
	}
	intent, origin, err := ParseLink(link)
	if err != nil || origin == "" || intent.Version != 1 {
		return errors.New("the original Remote wallet request is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".local", "state", "fased", "owner-backups")
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := locate(probeCtx, intent, origin, user, filepath.Join(home, ".ssh", "known_hosts"), os.Getuid(), run)
	if err != nil {
		return err
	}
	remoteRoot, err := update.PackageInstanceRoot(client.Peer.OwnerUID, "remote", packageID)
	if err != nil {
		return err
	}
	client.packageOwner = filepath.Join(remoteRoot, "fased-app")
	if err = client.inspect(probeCtx); err != nil && !errors.Is(err, ErrEnrollmentRequired) {
		return err
	}
	if client.Peer.SignerID != packageID || !sameRemoteWalletPeer(client.Peer, client.Peer) {
		return errors.New("the selected Remote owner did not verify the original package")
	}
	intent, _, err = resume(root, link, func(request signer.OwnerIntent, saved *draft) error {
		pending := client.Peer.PendingOperation
		if pending != "" && (!update.IDOK(pending) || saved == nil || !saved.Dispatched || saved.Origin != origin ||
			saved.Intent.OperationID != pending || saved.Intent.InstanceID != request.InstanceID ||
			saved.Intent.Network != request.Network || saved.Intent.Action != request.Action) {
			return errors.New("resume the original pending Remote wallet operation")
		}
		return nil
	})
	if err != nil {
		return err
	}
	client.Intent = intent
	return serveWalletBrowser(ctx, &browserWallet{intent: intent, remote: client, remotePeer: client.Peer, root: root, owner: os.Getuid()})
}

func serveWalletBrowser(ctx context.Context, w *browserWallet) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("cannot open the protected wallet")
	}
	defer listener.Close()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	w.host, w.token = listener.Addr().String(), hex.EncodeToString(secret[:])
	w.run = w.enroll
	server := w.server()
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	launch := exec.Command("/usr/bin/xdg-open", "http://"+w.host+"/#"+w.token)
	if err = launch.Start(); err != nil {
		_ = server.Close()
		return errors.New("cannot open the protected wallet in your browser")
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
		return errors.New("protected wallet connection stopped")
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

func decodeBrowserRecovery(action, network string, input browserInput) ([]byte, error) {
	if action != "recover" {
		if input.Recovery != "" || input.RecoveryBase64 != "" || input.Fingerprint != "" {
			return nil, errors.New("unexpected recovery input")
		}
		return nil, nil
	}
	if input.Recovery != "" && input.RecoveryBase64 != "" {
		return nil, errors.New("choose one recovery input")
	}
	rawRecovery := []byte(input.Recovery)
	if input.RecoveryBase64 != "" {
		var err error
		rawRecovery, err = base64.StdEncoding.DecodeString(input.RecoveryBase64)
		if err != nil {
			return nil, errors.New("invalid recovery file")
		}
		defer clear(rawRecovery)
	}
	return signer.RecoveryDocument(rawRecovery, input.Fingerprint, network)
}

func (w *browserWallet) enroll(ctx context.Context, input browserInput) (browserResult, error) {
	var result browserResult
	var pair Package
	var err error
	signerID := w.remotePeer.SignerID
	if w.remote == nil {
		pair, err = LocalOwnerPackage(w.intent)
		if err != nil {
			return result, err
		}
		signerID = filepath.Base(pair.LocalPaths.Root)
	}
	in := signer.OwnerInput{Intent: w.intent, SignerID: signerID, Phase: "prepare", Unlock: []byte(input.Unlock), BackupPassword: []byte(input.BackupPassword)}
	defer in.Clear()
	if w.intent.Action == "import" {
		in.Seed, err = hex.DecodeString(strings.TrimSpace(input.Seed))
		if err != nil {
			return result, err
		}
	} else if input.Seed != "" {
		return result, errors.New("unexpected seed")
	}
	backup, err := decodeBrowserRecovery(w.intent.Action, w.intent.Network, input)
	if err != nil {
		return result, err
	}
	in.Backup = backup
	if w.intent.Action == "recover" {
		in.Fingerprint = input.Fingerprint
	}
	if err = in.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	client, err := w.prepareSigner(ctx, pair)
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

func (w *browserWallet) prepareSigner(ctx context.Context, pair Package) (*Client, error) {
	if w.remote != nil {
		c := w.remote
		before := w.remotePeer
		err := c.inspect(ctx)
		if err != nil && !errors.Is(err, ErrEnrollmentRequired) {
			return nil, err
		}
		if !sameRemoteWalletPeer(c.Peer, before) || c.Peer.PendingOperation != "" && c.Peer.PendingOperation != w.intent.OperationID {
			return nil, errors.New("the Remote wallet target changed; preserve the original operation")
		}
		if dispatchErr := MarkDispatched(w.root, w.intent); dispatchErr != nil {
			return nil, dispatchErr
		}
		if errors.Is(err, ErrEnrollmentRequired) {
			args := append(c.arguments(), "--initialize", "--operation", w.intent.OperationID)
			if _, err = c.invoke(ctx, c.ownerExecutable(), args, nil); err != nil {
				return nil, errors.New("Remote wallet setup was interrupted; resume the same operation")
			}
			if err = c.inspect(ctx); err != nil || !sameRemoteWalletPeer(c.Peer, before) {
				return nil, errors.New("the original Remote wallet service is not ready")
			}
		}
		return c, nil
	}
	client, err := Open(ctx, w.intent, "", "")
	var plan *Enrollment
	if errors.Is(err, ErrEnrollmentRequired) {
		plan, err = PrepareEnrollment(ctx, w.intent, "", "", pair)
	}
	if err != nil {
		return nil, err
	}
	// No mutation precedes the durable draft identity.
	if err = MarkDispatched(w.root, w.intent); err != nil {
		return nil, err
	}
	if plan != nil {
		if ok, err := plan.RunPackage(ctx); err != nil || !ok {
			return nil, errors.New("the protected package did not initialize")
		}
		return plan.Wait(ctx)
	}
	return client, nil
}

func sameRemoteWalletPeer(a, b Peer) bool {
	return a.Ready != nil && b.Ready != nil && a.SignerID == b.SignerID && a.OwnerUID == b.OwnerUID && a.InstanceID == b.InstanceID &&
		a.Network == b.Network && a.Provenance == signer.OwnerKeyProvenance && a.Provenance == b.Provenance &&
		len(a.CoreSHA256) == 64 && a.CoreSHA256 == b.CoreSHA256 && len(a.SignerSHA256) == 64 && a.SignerSHA256 == b.SignerSHA256
}
