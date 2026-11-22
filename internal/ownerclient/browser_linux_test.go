//go:build linux

package ownerclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestBrowserWalletRequiresOwnerOriginAndToken(t *testing.T) {
	called := 0
	w := &browserWallet{intent: intent(), host: "127.0.0.1:45231", token: strings.Repeat("b", 64), owner: 1000}
	w.run = func(_ context.Context, in browserInput) (browserResult, error) {
		called++
		if in.Unlock != "disposable password" {
			t.Fatal("owner input was changed")
		}
		return browserResult{Address: "public"}, nil
	}
	request := func(uid int, host, origin, token, body string, content string, duplicate bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://"+w.host+"/enroll", strings.NewReader(body))
		r.Host = host
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Fased-Owner", token)
		if duplicate {
			r.Header.Add("X-Fased-Owner", token)
		}
		r.Header.Set("Content-Type", content)
		r = r.WithContext(context.WithValue(r.Context(), ownerConnectionKey{}, uid))
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	body := `{"Unlock":"disposable password","UnlockConfirm":"disposable password","BackupPassword":"disposable backup","BackupConfirm":"disposable backup"}`
	for _, tc := range []struct {
		uid                                int
		host, origin, token, body, content string
		duplicate                          bool
	}{
		{1001, w.host, "http://" + w.host, w.token, body, "application/json", false},
		{1000, "other.invalid", "http://" + w.host, w.token, body, "application/json", false},
		{1000, w.host, "https://attacker.invalid", w.token, body, "application/json", false},
		{1000, w.host, "http://" + w.host, "wrong", body, "application/json", false},
		{1000, w.host, "http://" + w.host, w.token, body, "text/plain", false},
		{1000, w.host, "http://" + w.host, w.token, body, "application/json", true},
		{1000, w.host, "http://" + w.host, w.token, `{"Unlock":"x","unknown":true}`, "application/json", false},
		{1000, w.host, "http://" + w.host, w.token, body + `{}`, "application/json", false},
	} {
		if result := request(tc.uid, tc.host, tc.origin, tc.token, tc.body, tc.content, tc.duplicate); result.Code == http.StatusOK {
			t.Fatal("untrusted request accepted")
		}
	}
	if called != 0 {
		t.Fatal("untrusted request reached the signer path")
	}
	result := request(1000, w.host, "http://"+w.host, w.token, body, "application/json", false)
	if result.Code != http.StatusOK || called != 1 || strings.Contains(result.Body.String(), "disposable password") {
		t.Fatal("verified owner request failed or leaked input", result.Code)
	}
	if duplicate := request(1000, w.host, "http://"+w.host, w.token, body, "application/json", false); duplicate.Code != http.StatusConflict || called != 1 {
		t.Fatal("completed wallet operation was executed twice")
	}
	if result.Header().Get("Cache-Control") != "no-store" || !strings.Contains(result.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("owner response is not isolated")
	}
}

func TestBrowserWalletOnRealLocalConnection(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	w := &browserWallet{intent: intent(), host: l.Addr().String(), token: strings.Repeat("c", 64), owner: os.Getuid()}
	w.run = func(_ context.Context, _ browserInput) (browserResult, error) {
		return browserResult{Address: "public"}, nil
	}
	server := w.server()
	go func() { _ = server.Serve(l) }()
	defer server.Close()
	client := &http.Client{}
	response, err := client.Get("http://" + w.host + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(page), "wallet.js") {
		t.Fatal("owner browser page did not load", err)
	}
	request, _ := http.NewRequest(http.MethodPost, "http://"+w.host+"/info", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://"+w.host)
	request.Header.Set("X-Fased-Owner", w.token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("the real owner connection was refused", response.StatusCode)
	}
}

// Run explicitly on the owner host after installing its existing Playwright
// browser. The signer callback is disposable; installed acceptance is separate.
func TestBrowserWalletRendersAndSubmitsOnLocalHost(t *testing.T) {
	if os.Getenv("FASED_OWNER_BROWSER_TEST") != "1" {
		t.Skip("local browser check not requested")
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	w := &browserWallet{intent: intent(), host: l.Addr().String(), token: strings.Repeat("d", 64), owner: os.Getuid()}
	w.run = func(_ context.Context, in browserInput) (browserResult, error) {
		if in.Unlock != "disposable wallet password" || in.BackupPassword != "disposable backup password" {
			return browserResult{}, io.ErrUnexpectedEOF
		}
		return browserResult{Address: "public-test-address", BackupFile: "/test/backup", Fingerprint: "test-fingerprint"}, nil
	}
	server := w.server()
	go func() { _ = server.Serve(l) }()
	defer server.Close()
	const browserCheck = `const { chromium } = require('../../node_modules/@playwright/test');
(async () => { const browser = await chromium.launch({headless: true}); try {
 const page = await browser.newPage(); const base = process.env.FASED_TEST_ORIGIN; let outside = false;
 page.on('request', r => { if (!r.url().startsWith(base + '/')) outside = true; });
 await page.goto(base + '/#' + process.env.FASED_TEST_TOKEN);
 await page.getByLabel('Wallet password', {exact:true}).fill('disposable wallet password');
 await page.getByLabel('Confirm wallet password', {exact:true}).fill('disposable wallet password');
 await page.getByLabel('Backup password', {exact:true}).fill('disposable backup password');
 await page.getByLabel('Confirm backup password', {exact:true}).fill('disposable backup password');
 await page.getByRole('button', {name:'Continue'}).click();
 await page.locator('#result:not([hidden])').waitFor({timeout:10000});
 if ((await page.locator('#address').textContent()) !== 'public-test-address' || outside) throw Error('wallet browser result mismatch');
 if (await page.evaluate(() => location.hash)) throw Error('session token remains in the address bar');
 } finally { await browser.close(); } })().catch(() => process.exit(1));`
	cmd := exec.CommandContext(t.Context(), "node", "-e", browserCheck)
	cmd.Env = append(os.Environ(), "FASED_TEST_ORIGIN=http://"+w.host, "FASED_TEST_TOKEN="+w.token)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("local browser check failed: %v %s", err, output)
	}
}

func TestRemoteWalletBrowserResumesOriginalProtectedSetup(t *testing.T) {
	request := intent()
	origin := "https://owner.example.ts.net"
	link, err := Link(request.InstanceID, request.Network, request.Action, origin)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	request, _, err = Resume(root, link)
	if err != nil {
		t.Fatal(err)
	}
	notReady, ready := false, true
	peer := Peer{
		Version: 1, Ready: &notReady, OwnerUID: 1000, InstanceID: request.InstanceID,
		Network: request.Network, Provenance: signer.OwnerKeyProvenance,
		SignerID: strings.Repeat("a", 32), CoreSHA256: strings.Repeat("b", 64), SignerSHA256: strings.Repeat("c", 64),
	}
	observed := peer
	var applies int
	cmd := "/var/lib/fased-app-package/1000/remote/" + peer.SignerID + "/fased-app"
	client := &Client{Intent: request, Peer: peer, host: "owner.example.ts.net", user: "operator", knownHosts: "/home/test/.ssh/known_hosts", packageOwner: cmd}
	client.run = func(_ context.Context, path string, args []string, input []byte) ([]byte, error) {
		if path != "/usr/bin/ssh" || !slices.Contains(args, cmd) || !slices.Contains(args, "-oStrictHostKeyChecking=yes") || len(input) != 0 {
			t.Fatal("public setup used an unpinned or secret-bearing command")
		}
		if slices.Contains(args, "--inspect") {
			return json.Marshal(observed)
		}
		if !slices.Contains(args, "--initialize") || !slices.Contains(args, "--ordinary-custody") || args[len(args)-1] != request.OperationID {
			t.Fatal("initialization did not carry the original operation")
		}
		applies++
		if applies == 1 {
			observed.PendingOperation = request.OperationID
			return nil, errors.New("interrupted setup")
		}
		observed.Ready, observed.PendingOperation = &ready, ""
		return nil, nil
	}
	window := browserWallet{intent: request, root: root, remote: client, remotePeer: peer}
	if _, err = window.prepareSigner(t.Context(), Package{}); err == nil || applies != 1 {
		t.Fatal("interruption did not keep the initial setup unresolved", err)
	}
	observed.CoreSHA256 = strings.Repeat("d", 64)
	for range 2 {
		if _, err = window.prepareSigner(t.Context(), Package{}); err == nil || applies != 1 {
			t.Fatal("changed image was accepted on retry", err)
		}
	}
	observed.CoreSHA256 = peer.CoreSHA256
	observed.PendingOperation = strings.Repeat("f", 32)
	if _, err = window.prepareSigner(t.Context(), Package{}); err == nil || applies != 1 {
		t.Fatal("another pending operation was accepted", err)
	}
	observed.PendingOperation = request.OperationID
	got, err := window.prepareSigner(t.Context(), Package{})
	if err != nil || got != client || applies != 2 || client.Peer.Ready == nil || !*client.Peer.Ready {
		t.Fatal("the original setup could not resume", err)
	}
}

func TestBrowserWalletBinaryRecoveryUploadPreservesBytes(t *testing.T) {
	rawBinary := append([]byte("FASED-ENCRYPTED-HISTORY:v1\n"), []byte{0x80, 0xff, 0xfe, 0x00, 0x01, 0x99, 0xc3, 0x28}...)
	pin := signer.BackupFingerprint(rawBinary)
	recoverIntent := intent()
	recoverIntent.Action = "recover"

	var decoded []byte
	w := &browserWallet{intent: recoverIntent, host: "127.0.0.1:45231", token: strings.Repeat("b", 64), owner: 1000}
	w.run = func(_ context.Context, in browserInput) (browserResult, error) {
		doc, err := decodeBrowserRecovery(w.intent.Action, w.intent.Network, in)
		if err != nil {
			return browserResult{}, err
		}
		decoded = doc
		return browserResult{Address: "public"}, nil
	}

	postEnroll := func(payload map[string]string) *httptest.ResponseRecorder {
		rawBody, _ := json.Marshal(payload)
		r := httptest.NewRequest(http.MethodPost, "http://"+w.host+"/enroll", bytes.NewReader(rawBody))
		r.Host = w.host
		r.Header.Set("Origin", "http://"+w.host)
		r.Header.Set("X-Fased-Owner", w.token)
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(r.Context(), ownerConnectionKey{}, 1000))
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}

	basePayload := func() map[string]string {
		return map[string]string{
			"Unlock":         "disposable password",
			"UnlockConfirm":  "disposable password",
			"BackupPassword": "disposable backup",
			"BackupConfirm":  "disposable backup",
			"Fingerprint":    pin,
		}
	}

	badDual := basePayload()
	badDual["Recovery"] = "text"
	badDual["RecoveryBase64"] = base64.StdEncoding.EncodeToString(rawBinary)
	if res := postEnroll(badDual); res.Code == http.StatusOK {
		t.Fatal("accepted simultaneous text and binary recovery")
	}

	badB64 := basePayload()
	badB64["RecoveryBase64"] = "!!!not-base64!!!"
	if res := postEnroll(badB64); res.Code == http.StatusOK {
		t.Fatal("accepted malformed base64 recovery")
	}

	setupIntent := intent()
	if _, err := decodeBrowserRecovery(setupIntent.Action, setupIntent.Network, browserInput{
		RecoveryBase64: base64.StdEncoding.EncodeToString(rawBinary),
	}); err == nil {
		t.Fatal("accepted RecoveryBase64 on non-recover intent")
	}

	valid := basePayload()
	valid["RecoveryBase64"] = base64.StdEncoding.EncodeToString(rawBinary)
	if res := postEnroll(valid); res.Code != http.StatusOK {
		t.Fatalf("binary recovery upload failed: %d %s", res.Code, res.Body.String())
	}
	if !bytes.Equal(decoded, rawBinary) {
		t.Fatalf("binary recovery bytes mutated: got %x want %x", decoded, rawBinary)
	}
}
