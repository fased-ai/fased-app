package main

import (
	"context"
	"encoding/base64"
	"github.com/fased-ai/fased-app/internal/ownerclient"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/wailsapp/wails/v2"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOwnerWindowAssetsAndBindingsAreIsolated(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := &WalletOwner{ctx: ctx, cancel: cancel}
	assets, err := fs.Sub(ownerAssets, "ownerui")
	if err != nil {
		t.Fatal(err)
	}
	opts := ownerOptions(owner, assets)
	if len(opts.Bind) != 1 || opts.Bind[0] != owner || opts.AssetServer.Handler != nil || opts.AssetServer.Middleware != nil {
		t.Fatal("owner window inherited a core transport")
	}
	script, err := fs.ReadFile(assets, "owner.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fetch(", "localStorage", "sessionStorage", "console.", "innerHTML", "location.href"} {
		if strings.Contains(string(script), forbidden) {
			t.Fatal("unsafe owner asset capability", forbidden)
		}
	}
	if _, err = fs.ReadFile(assets, "app.js"); err == nil {
		t.Fatal("core assets reachable in owner window")
	}
	if opts.OnBeforeClose(ctx) {
		t.Fatal("close refused")
	}
	if ctx.Err() == nil {
		t.Fatal("closing owner window did not cancel private input")
	}
}

// Actual Wails/WebKit binding proof, explicitly selected on a graphical owner
// session. It opens no core, wallet, SSH connection, file dialog or OS consent.
func TestOwnerNativeRuntimeFixture(t *testing.T) {
	if os.Getenv("FASED_NATIVE_OWNER_RUNTIME") != "1" {
		t.Skip("actual graphical native fixture not selected")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := &WalletOwner{ctx: ctx, cancel: cancel, intent: signer.OwnerIntent{Network: "devnet", Action: "setup"}}
	assets, err := fs.Sub(ownerAssets, "ownerui")
	if err != nil {
		t.Fatal(err)
	}
	opts := ownerOptions(owner, assets)
	proof := make(chan bool, 1)
	opts.OnStartup = func(ctx context.Context) {
		wailsruntime.EventsOn(ctx, "fased-owner-runtime-proof", func(args ...interface{}) {
			ok := len(args) == 1 && args[0] == "PASS"
			select {
			case proof <- ok:
			default:
			}
			wailsruntime.Quit(ctx)
		})
		go func() {
			select {
			case <-time.After(15 * time.Second):
				wailsruntime.Quit(ctx)
			case <-ctx.Done():
			}
		}()
	}
	opts.OnDomReady = func(ctx context.Context) {
		wailsruntime.WindowExecJS(ctx, `(async()=>{try{const i=await window.go.main.WalletOwner.Info();for(let n=0;n<100&&document.querySelector('#network')?.textContent!=='Devnet';n++)await new Promise(r=>setTimeout(r,10));const ok=i.network==='devnet'&&document.querySelector('#unlock')?.type==='password'&&!!document.querySelector('#enable-button')&&document.querySelector('#network')?.textContent==='Devnet';window.runtime.EventsEmit('fased-owner-runtime-proof',ok?'PASS':'FAIL')}catch(e){window.runtime.EventsEmit('fased-owner-runtime-proof','FAIL')}})()`)
	}
	if err = wails.Run(opts); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-proof:
		if !ok {
			t.Fatal("native binding or owner assets failed")
		}
	default:
		t.Fatal("native fixture produced no proof")
	}
}

func TestOwnerConnectRetainedStateExposesSetupThenReconnects(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("protected native enrollment is Linux-only")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	i := signer.OwnerIntent{Version: 1, InstanceID: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), OperationID: strings.Repeat("a", 32), Action: "setup", Network: "devnet"}
	w := &WalletOwner{ctx: ctx, cancel: cancel, intent: i, origin: "https://chosen.ts.net", root: t.TempDir() + "/owner"}
	w.openOwner = func(context.Context, signer.OwnerIntent, string, string) (*ownerclient.Client, error) {
		return nil, ownerclient.ErrEnrollmentRequired
	}
	if ready, err := w.Connect(w.origin, "alice"); err != nil || ready || w.client != nil {
		t.Fatal("retained target cannot reach setup", ready, err)
	}
	w.enroll = func(context.Context, signer.OwnerIntent, string, string, ownerclient.Package) (*ownerclient.Enrollment, error) {
		return nil, &ownerclient.RemoteAdminConsent{Command: "public console setup"}
	}
	w.packageSource = func() (ownerclient.Package, error) { return ownerclient.Package{}, nil }
	w.terminal = func(context.Context, string, []string) error {
		t.Fatal("missing consent launched terminal")
		return nil
	}
	if err := w.Enable(w.origin, "alice"); err == nil || w.Info()["setup"] != "public console setup" {
		t.Fatal("retained resume lost admin handoff", err)
	}
	readyNow := true
	client := &ownerclient.Client{Intent: i, Peer: ownerclient.Peer{Ready: &readyNow, Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: os.Getuid()}}
	w.openOwner = func(context.Context, signer.OwnerIntent, string, string) (*ownerclient.Client, error) {
		return client, nil
	}
	if ready, err := w.Connect(w.origin, "alice"); err != nil || !ready || w.client != client {
		t.Fatal("resumed target did not reconnect", ready, err)
	}
	if ready, err := w.Connect(w.origin, "alice"); err != nil || !ready {
		t.Fatal("second ready connection changed identity", err)
	}
}
