package main

import (
	"context"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/ownerclient"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed ownerui/*
var ownerAssets embed.FS

// WalletOwner is bound ONLY in the separate immutable owner window. The regular
// core-proxy window has no binding to these methods or these assets.
type WalletOwner struct {
	openOwner     func(context.Context, signer.OwnerIntent, string, string) (*ownerclient.Client, error)
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	intent        signer.OwnerIntent
	origin, root  string
	setupCommand  string
	client        *ownerclient.Client
	busy          bool
	enroll        func(context.Context, signer.OwnerIntent, string, string, ownerclient.Package) (*ownerclient.Enrollment, error)
	packageSource func() (ownerclient.Package, error)
	terminal      func(context.Context, string, []string) error
}

func ownerWindow(link string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".local", "state", "fased", "owner-backups")
	intent, origin, err := ownerclient.Resume(root, link)
	if err != nil {
		return err
	}
	assets, err := fs.Sub(ownerAssets, "ownerui")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &WalletOwner{ctx: ctx, cancel: cancel, intent: intent, origin: origin, root: root, enroll: ownerclient.PrepareEnrollment, packageSource: func() (ownerclient.Package, error) {
		self, err := os.Executable()
		if err != nil {
			return ownerclient.Package{}, err
		}
		return ownerclient.NativePackage(self)
	}, terminal: launchOwnerEnrollment}
	return wails.Run(ownerOptions(owner, assets))
}

func ownerOptions(owner *WalletOwner, assets fs.FS) *options.App {
	return &options.App{Title: "Fased Wallet", Width: 520, Height: 700, MinWidth: 390, MinHeight: 640, BackgroundColour: options.NewRGBA(23, 22, 31, 255), AssetServer: &assetserver.Options{Assets: assets}, Bind: []interface{}{owner}, EnableDefaultContextMenu: false, DragAndDrop: &options.DragAndDrop{DisableWebViewDrop: true, EnableFileDrop: false}, OnBeforeClose: func(context.Context) bool { owner.Cancel(); return false }}
}

func (w *WalletOwner) Info() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	info := map[string]string{"setup": w.setupCommand, "instance": w.intent.InstanceID, "network": w.intent.Network, "action": w.intent.Action, "origin": w.origin}
	if address, user, err := ownerclient.SavedTarget(w.root, w.intent.InstanceID); err == nil {
		info["address"], info["user"] = address, user
	}
	return info
}

func (w *WalletOwner) Connect(selectedOrigin, user string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.setupCommand = ""
	if w.busy {
		return false, errors.New("wallet operation is running")
	}
	ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
	defer cancel()
	if w.origin == "" && selectedOrigin != "" || w.origin != "" && selectedOrigin == "" {
		return false, errors.New("select the private wallet address in this owner window")
	}
	w.client = nil
	c, err := w.connectOwner(ctx, selectedOrigin, user)
	if errors.Is(err, ownerclient.ErrEnrollmentRequired) {
		return false, nil
	}
	if err == nil {
		err = ownerclient.RememberTarget(w.root, selectedOrigin, user, c.Peer)
	}
	if err == nil {
		w.client = c
	}
	return err == nil, err
}

func (w *WalletOwner) connectOwner(ctx context.Context, origin, user string) (*ownerclient.Client, error) {
	if w.openOwner != nil {
		return w.openOwner(ctx, w.intent, origin, user)
	}
	return ownerclient.Open(ctx, w.intent, origin, user)
}

func (w *WalletOwner) Cancel() { w.cancel() }

func (w *WalletOwner) Enable(selectedOrigin, user string) error {
	w.mu.Lock()
	if w.busy {
		w.mu.Unlock()
		return errors.New("wallet operation is running")
	}
	w.busy = true
	w.setupCommand = ""
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.busy = false; w.mu.Unlock() }()
	if w.enroll == nil || w.packageSource == nil || w.terminal == nil {
		return errors.New("open Wallets in the supported native client")
	}
	if w.origin == "" && selectedOrigin != "" || w.origin != "" && selectedOrigin == "" {
		return errors.New("select the private wallet address in this owner window")
	}
	reconnect, stop := context.WithTimeout(w.ctx, 30*time.Second)
	connected, connectErr := w.connectOwner(reconnect, selectedOrigin, user)
	stop()
	if connectErr == nil {
		if err := ownerclient.RememberTarget(w.root, selectedOrigin, user, connected.Peer); err != nil {
			return err
		}
		w.mu.Lock()
		w.client = connected
		w.mu.Unlock()
		return nil
	}
	pair, err := w.packageSource()
	if err != nil {
		return err
	}
	prepare, cancel := context.WithTimeout(w.ctx, 2*time.Minute)
	plan, err := w.enroll(prepare, w.intent, selectedOrigin, user, pair)
	cancel()
	if err != nil {
		var approval *ownerclient.RemoteAdminConsent
		if errors.As(err, &approval) {
			w.mu.Lock()
			w.setupCommand = approval.Command
			w.mu.Unlock()
		}
		return err
	}
	// Human OS consent starts only after image, target and transfer preflight.
	approval, cancel := context.WithTimeout(w.ctx, 5*time.Minute)
	defer cancel()
	path, args := plan.Terminal()
	if err = w.terminal(approval, path, args); err != nil {
		return err
	}
	client, err := plan.Wait(approval)
	if err != nil {
		return err
	}
	if err = ownerclient.RememberTarget(w.root, selectedOrigin, user, client.Peer); err != nil {
		return err
	}
	w.mu.Lock()
	w.client = client
	w.mu.Unlock()
	return nil
}

type walletResult struct {
	Address      string `json:"address"`
	BackupFile   string `json:"backupFile"`
	Fingerprint  string `json:"fingerprint"`
	RecoveryHeld bool   `json:"recoveryHeld"`
}

func (w *WalletOwner) Run(unlock, password, seed, recovery, fingerprint string) (walletResult, error) {
	w.mu.Lock()
	if w.busy || w.client == nil {
		w.mu.Unlock()
		return walletResult{}, errors.New("connect the protected wallet first")
	}
	w.busy = true
	c := w.client
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.busy = false; w.mu.Unlock() }()
	input := signer.OwnerInput{Intent: w.intent, SignerID: c.Peer.SignerID, Phase: "prepare", Unlock: []byte(unlock), BackupPassword: []byte(password)}
	defer input.Clear()
	if input.Intent.Action == "import" {
		var err error
		input.Seed, err = hex.DecodeString(seed)
		if err != nil {
			return walletResult{}, errors.New("enter a 32-byte seed as 64 hexadecimal characters")
		}
	}
	if input.Intent.Action == "recover" {
		var b ownerclient.Backup
		if len(recovery) > signer.MaxOwnerInput || json.Unmarshal([]byte(recovery), &b) != nil || b.Version != 1 || b.Network != input.Intent.Network || fingerprint != b.Fingerprint {
			return walletResult{}, errors.New("choose the encrypted backup and its independent checksum")
		}
		input.Backup, input.Fingerprint = b.Document, fingerprint
	}
	if err := input.Validate(); err != nil {
		return walletResult{}, err
	}
	if err := ownerclient.MarkDispatched(w.root, input.Intent); err != nil {
		return walletResult{}, err
	}
	ctx, cancel := context.WithTimeout(w.ctx, 3*time.Minute)
	defer cancel()
	prepared, err := c.Exchange(ctx, input)
	if err != nil {
		return walletResult{}, err
	}
	path, saved, err := ownerclient.SaveBackup(w.root, prepared)
	if err != nil {
		return walletResult{}, err
	}
	// Round trip the reopened local ciphertext through the protected helper.
	// No successful transmission or in-memory buffer substitutes for this step.
	clear(input.Backup)
	input.Phase, input.Backup, input.Fingerprint = "verify", saved, prepared.Fingerprint
	verified, err := c.Exchange(ctx, input)
	if err != nil {
		return walletResult{}, err
	}
	if !verified.BackupVerified || verified.Status.PublicIdentity == nil {
		return walletResult{}, errors.New("backup verification is incomplete")
	}
	return walletResult{verified.Status.PublicIdentity.Address, path, verified.Fingerprint, verified.Status.RecoveryRequired}, nil
}
