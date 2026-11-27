package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custody"
)

func doV2JSONWithStepUp(t *testing.T, method, url, origin, token, csrf, stepUp, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, bodyReader)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if stepUp != "" {
		req.Header.Set("X-Fased-Step-Up", stepUp)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestWalletsV2HardenedCustodyPoliciesRPCHardwareAndRecovery(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// 1. Unauthenticated access denied.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/wallets", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on GET /api/v1/wallets, got %d", unauth.StatusCode)
	}

	// 2. Create native agent wallet with inline Solana RPC -> NEVER returns mnemonic/private key over HTTP!
	createResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/create", ts.URL, token, csrf,
		`{"name":"Agent Desk","role":"agent","rpcUrl":"https://mainnet.helius-rpc.com/?api-key=secret-token"}`,
		"application/json",
	)
	rawCreate, _ := io.ReadAll(createResp.Body)
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/wallets/create failed: code=%d body=%s", createResp.StatusCode, rawCreate)
	}
	createBody := string(rawCreate)
	if strings.Contains(createBody, "mnemonic") || strings.Contains(createBody, "privateKey") || strings.Contains(createBody, "secret-token") {
		t.Fatalf("SECURITY FAILURE: raw secret or RPC key leaked in HTTP response: %s", createBody)
	}
	var created struct {
		Wallet custody.WalletPublic `json:"wallet"`
	}
	if err := json.Unmarshal(rawCreate, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Wallet.PublicKey == "" || created.Wallet.RPC.Cluster != "mainnet-beta" || created.Wallet.RPC.Host != "https://mainnet.helius-rpc.com" {
		t.Fatalf("unexpected wallet metadata: %+v", created.Wallet)
	}

	// 3. Old /export raw-secret endpoint MUST NOT EXIST (returns 404).
	legacyExport := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/export", ts.URL, token, csrf, `{"passphrase":"secret-pass-123"}`, "application/json")
	legacyExport.Body.Close()
	if legacyExport.StatusCode != http.StatusNotFound {
		t.Fatalf("expected legacy raw /export endpoint removed (404), got %d", legacyExport.StatusCode)
	}

	// 4. Create Vault wallet and confirm autonomous connection binding requires step-up and is hard-blocked (403).
	vaultResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/create", ts.URL, token, csrf,
		`{"name":"Reserve Vault","role":"vault","rpcUrl":"https://api.mainnet-beta.solana.com"}`,
		"application/json",
	)
	rawVault, _ := io.ReadAll(vaultResp.Body)
	vaultResp.Body.Close()
	var createdVault struct {
		Wallet custody.WalletPublic `json:"wallet"`
	}
	_ = json.Unmarshal(rawVault, &createdVault)

	// Connections update MUST require step-up (403 owner_password_required before password setup).
	badConn := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+createdVault.Wallet.ID+"/connections", ts.URL, token, csrf,
		`{"agentIds":["wen-operator"],"allowTradeBots":true}`, "application/json")
	rawBadConn, _ := io.ReadAll(badConn.Body)
	badConn.Body.Close()
	if badConn.StatusCode != http.StatusForbidden || !strings.Contains(string(rawBadConn), "owner_password_required") {
		t.Fatalf("expected 403 owner_password_required on /connections before password setup, got code=%d body=%s", badConn.StatusCode, rawBadConn)
	}

	// 5. Fail-closed policy denies transfer before policy loosened; confirmLoosen unlocks bounded execution.
	badRev := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/reviews", ts.URL, token, csrf,
		`{"operation":"solana.nativeTransfer","destination":"`+createdVault.Wallet.Handle+`","amountHuman":"0.1"}`,
		"application/json",
	)
	badRev.Body.Close()
	if badRev.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 fail-closed before policy grant, got %d", badRev.StatusCode)
	}

	pol := created.Wallet.Policy
	pol.ApprovalMode = custody.ApprovalBoundedAuto
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget}
	pol.Destinations = []string{createdVault.Wallet.Handle}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	polPayload, _ := json.Marshal(map[string]any{
		"policy":             pol,
		"confirmLoosen":      true,
		"expectedPolicyHash": created.Wallet.Policy.PolicyHash,
	})
	// Step-up: loosening policy before setting owner password returns 403 owner_password_required.
	noPWPol := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, string(polPayload), "application/json")
	rawNoPW, _ := io.ReadAll(noPWPol.Body)
	noPWPol.Body.Close()
	if noPWPol.StatusCode != http.StatusForbidden || !strings.Contains(string(rawNoPW), "owner_password_required") {
		t.Fatalf("expected 403 owner_password_required before password setup, got code=%d body=%s", noPWPol.StatusCode, rawNoPW)
	}

	// Configure Local owner password & helper for single-use, action-and-wallet-scoped step-up confirmation.
	const ownerPass = "correct-owner-pass-2026"
	pwResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/auth/password", ts.URL, token, csrf, `{"password":"`+ownerPass+`"}`, "application/json")
	pwResp.Body.Close()
	if pwResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/password failed: %d", pwResp.StatusCode)
	}
	if hasPW, _ := s.hasPassword(t.Context()); !hasPW {
		t.Fatalf("expected hasPassword=true after configuring owner password")
	}

	getScopedStepUpBound := func(action, walletID, bindingDigest string) string {
		t.Helper()
		payload := map[string]string{
			"password": ownerPass,
			"action":   action,
			"walletId": walletID,
		}
		if bindingDigest != "" {
			payload["bindingDigest"] = bindingDigest
		}
		body, _ := json.Marshal(payload)
		resp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/auth/step-up", ts.URL, token, csrf, string(body), "application/json")
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/v1/auth/step-up(%s/%s) failed: code=%d body=%s", action, walletID, resp.StatusCode, raw)
		}
		var out struct {
			StepUpToken string `json:"stepUpToken"`
		}
		_ = json.Unmarshal(raw, &out)
		return out.StepUpToken
	}
	getScopedStepUp := func(action, walletID string) string {
		t.Helper()
		return getScopedStepUpBound(action, walletID, "")
	}

	// Vault autonomous binding STILL hard-blocked (403) even with valid step-up!
	connVaultStep := getScopedStepUp("connections", createdVault.Wallet.ID)
	badConnAuthed := doV2JSONWithStepUp(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+createdVault.Wallet.ID+"/connections", ts.URL, token, csrf, connVaultStep,
		`{"agentIds":["wen-operator"],"allowTradeBots":true}`)
	badConnAuthed.Body.Close()
	if badConnAuthed.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 binding vault to autonomous bots/agents, got %d", badConnAuthed.StatusCode)
	}

	// Missing X-Fased-Step-Up header returns 403 step_up_required.
	noStepPol := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, string(polPayload), "application/json")
	rawNoStep, _ := io.ReadAll(noStepPol.Body)
	noStepPol.Body.Close()
	if noStepPol.StatusCode != http.StatusForbidden || !strings.Contains(string(rawNoStep), "step_up_required") {
		t.Fatalf("expected 403 step_up_required, got code=%d body=%s", noStepPol.StatusCode, rawNoStep)
	}

	// Single-use & scope isolation: a "backup" token CANNOT be used on "policy", and once used CANNOT be replayed!
	wrongScopeTok := getScopedStepUp("backup", created.Wallet.ID)
	wrongScopeResp := doV2JSONWithStepUp(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, wrongScopeTok, string(polPayload))
	wrongScopeResp.Body.Close()
	if wrongScopeResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 rejecting backup-scoped token on policy endpoint, got %d", wrongScopeResp.StatusCode)
	}

	// Unbound policy step-up MUST be rejected fail-closed (Wave S-6 digest binding).
	unboundPolStep := getScopedStepUp("policy", created.Wallet.ID)
	unboundPolResp := doV2JSONWithStepUp(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, unboundPolStep, string(polPayload))
	unboundPolResp.Body.Close()
	if unboundPolResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 rejecting unbound policy step-up token, got %d", unboundPolResp.StatusCode)
	}

	nextPol := pol
	nextPol.Role = created.Wallet.Role
	nextPol.Version = created.Wallet.Policy.Version + 1
	polStep := getScopedStepUpBound("policy", created.Wallet.ID, custody.ComputePolicyHash(nextPol))
	polResp := doV2JSONWithStepUp(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, polStep, string(polPayload))
	rawPol, _ := io.ReadAll(polResp.Body)
	polResp.Body.Close()
	if polResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /policy failed: code=%d body=%s", polResp.StatusCode, rawPol)
	}
	// Replaying the already-consumed polStep token on a further policy expansion MUST fail with 403!
	pol2 := pol
	pol2.SOLCap.MaxDailyRaw = 2_000_000_000
	pol2Payload, _ := json.Marshal(map[string]any{
		"policy":        pol2,
		"confirmLoosen": true,
	})
	replayPol := doV2JSONWithStepUp(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, polStep, string(pol2Payload))
	replayPol.Body.Close()
	if replayPol.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 rejecting replayed single-use step-up token, got %d", replayPol.StatusCode)
	}

	// Prepare & execute via native signer -> updates balance/ledger cleanly with fresh execute-scoped step-up.
	revResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/reviews", ts.URL, token, csrf,
		`{"operation":"solana.nativeTransfer","destination":"`+createdVault.Wallet.Handle+`","amountHuman":"0.2"}`,
		"application/json",
	)
	rawRev, _ := io.ReadAll(revResp.Body)
	revResp.Body.Close()
	if revResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /reviews failed: code=%d body=%s", revResp.StatusCode, rawRev)
	}
	var revOut struct {
		Review custody.WalletReview `json:"review"`
	}
	_ = json.Unmarshal(rawRev, &revOut)

	// Unbound execute step-up MUST be rejected fail-closed.
	unboundExecStep := getScopedStepUp("execute", created.Wallet.ID)
	unboundExecResp := doV2JSONWithStepUp(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/reviews/"+revOut.Review.ID+"/execute", ts.URL, token, csrf, unboundExecStep, `{}`)
	unboundExecResp.Body.Close()
	if unboundExecResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 rejecting unbound execute step-up token, got %d", unboundExecResp.StatusCode)
	}

	execStep := getScopedStepUpBound("execute", created.Wallet.ID, revOut.Review.TxDigest)
	execResp := doV2JSONWithStepUp(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/reviews/"+revOut.Review.ID+"/execute", ts.URL, token, csrf, execStep, `{}`)
	execResp.Body.Close()
	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /execute failed: %d", execResp.StatusCode)
	}

	// Wave S-6: DELETE /api/v1/auth/passkeys/{credID} must NOT return 405; with valid step-up it reaches signer (404 for unknown credID).
	delPKStep := getScopedStepUp("passkey-delete", "*")
	delPKResp := doV2JSONWithStepUp(t, http.MethodDelete, ts.URL+"/api/v1/auth/passkeys/nonexistent-cred-id", ts.URL, token, csrf, delPKStep, "")
	delPKResp.Body.Close()
	if delPKResp.StatusCode == http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /api/v1/auth/passkeys/{id} returned 405 Method Not Allowed (regression)")
	}
	if delPKResp.StatusCode != http.StatusNotFound && delPKResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 404/400 for nonexistent passkey delete, got %d", delPKResp.StatusCode)
	}

	// Wave S-6: POST /api/v1/wallets/create requires step-up once owner password is set.
	noStepCreate := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/create", ts.URL, token, csrf,
		`{"name":"Blocked Without StepUp","role":"agent","rpcUrl":"https://api.mainnet-beta.solana.com"}`, "application/json")
	noStepCreate.Body.Close()
	if noStepCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on POST /wallets/create without step-up after password configured, got %d", noStepCreate.StatusCode)
	}

	// 6. Attach Hardware Wallet & verify tampered signed tx is rejected cleanly (403).
	hwPub, hwPriv, _ := ed25519.GenerateKey(rand.Reader)
	hwPubB58 := custody.Base58Encode(hwPub)
	attResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/attach", ts.URL, token, csrf,
		`{"custody":"hardware_standard","name":"Ledger Reserve","role":"vault","publicKey":"`+hwPubB58+`","rpcUrl":"https://api.mainnet-beta.solana.com"}`,
		"application/json",
	)
	rawAtt, _ := io.ReadAll(attResp.Body)
	attResp.Body.Close()
	if attResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /attach failed: code=%d body=%s", attResp.StatusCode, rawAtt)
	}
	var hwOut struct {
		Wallet custody.WalletPublic `json:"wallet"`
	}
	_ = json.Unmarshal(rawAtt, &hwOut)
	hwPol := hwOut.Wallet.Policy
	hwPol.Operations = []string{"solana.nativeTransfer"}
	hwPol.Programs = []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget}
	hwPol.Destinations = []string{created.Wallet.PublicKey}
	hwPol.SOLCap.MaxPerTxRaw = 500_000_000
	hwPol.SOLCap.MaxDailyRaw = 1_000_000_000
	hwPolPayload, _ := json.Marshal(map[string]any{
		"policy":             hwPol,
		"confirmLoosen":      true,
		"expectedPolicyHash": hwOut.Wallet.Policy.PolicyHash,
	})
	hwNextPol := hwPol
	hwNextPol.Role = hwOut.Wallet.Role
	hwNextPol.Version = hwOut.Wallet.Policy.Version + 1
	hwPolStep := getScopedStepUpBound("policy", hwOut.Wallet.ID, custody.ComputePolicyHash(hwNextPol))
	hwPolResp := doV2JSONWithStepUp(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+hwOut.Wallet.ID+"/policy", ts.URL, token, csrf, hwPolStep, string(hwPolPayload))
	hwPolResp.Body.Close()
	if hwPolResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /hw policy failed: %d", hwPolResp.StatusCode)
	}

	hwRevResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+hwOut.Wallet.ID+"/reviews", ts.URL, token, csrf,
		`{"operation":"solana.nativeTransfer","destination":"`+created.Wallet.PublicKey+`","amountHuman":"0.1"}`,
		"application/json",
	)
	rawHWRev, _ := io.ReadAll(hwRevResp.Body)
	hwRevResp.Body.Close()
	var hwRevOut struct {
		Review custody.WalletReview `json:"review"`
	}
	_ = json.Unmarshal(rawHWRev, &hwRevOut)
	msgBytes, _ := base64.StdEncoding.DecodeString(hwRevOut.Review.UnsignedMsgBase64)
	validSignedTx, _ := custody.SignLegacyMessage(msgBytes, hwPriv)

	tamperedMsg := append([]byte(nil), msgBytes...)
	tamperedMsg[0] ^= 0x01
	badSignedTx, _ := custody.SignLegacyMessage(tamperedMsg, hwPriv)

	badSub := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+hwOut.Wallet.ID+"/reviews/"+hwRevOut.Review.ID+"/submit-signed", ts.URL, token, csrf,
		`{"signedTxBase64":"`+base64.StdEncoding.EncodeToString(badSignedTx)+`"}`, "application/json")
	badSub.Body.Close()
	if badSub.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on tampered hardware signed tx, got %d", badSub.StatusCode)
	}

	okSub := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+hwOut.Wallet.ID+"/reviews/"+hwRevOut.Review.ID+"/submit-signed", ts.URL, token, csrf,
		`{"signedTxBase64":"`+base64.StdEncoding.EncodeToString(validSignedTx)+`"}`, "application/json")
	okSub.Body.Close()
	if okSub.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on valid hardware signed tx, got %d", okSub.StatusCode)
	}

	// 7. Encrypted Argon2id Backup returns encrypted archive ONLY (no plaintext key) and hits rate limit on excess calls.
	bakResp := doV2JSONWithStepUp(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/backup", ts.URL, token, csrf, wrongScopeTok, `{"passphrase":"secret-pass-2026"}`)
	rawBak, _ := io.ReadAll(bakResp.Body)
	bakResp.Body.Close()
	if bakResp.StatusCode != http.StatusOK || !strings.Contains(string(rawBak), `"fased-recovery/1"`) {
		t.Fatalf("POST /backup failed: code=%d body=%s", bakResp.StatusCode, rawBak)
	}

	// Rate limit check on repeated backup requests (5/min limit).
	hit429 := false
	for i := 0; i < 6; i++ {
		extra := doV2JSONWithStepUp(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/backup", ts.URL, token, csrf, "spent-tok", `{"passphrase":"secret-pass-2026"}`)
		extra.Body.Close()
		if extra.StatusCode == http.StatusTooManyRequests {
			hit429 = true
			break
		}
	}
	if !hit429 {
		t.Fatalf("expected 429 rate limit on repeated sensitive wallet endpoints")
	}
}

func TestWalletsV2OutOfProcessSignerSocketAndPasswordDisablesAutoEnter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	signerDir := t.TempDir()
	signerSvc, err := custody.NewService(custody.Config{
		DataDir:     signerDir,
		FixtureMode: true,
		FastKDF:     true,
	})
	if err != nil {
		t.Fatalf("custody.NewService: %v", err)
	}
	t.Cleanup(func() { _ = signerSvc.Close() })
	sockPath := filepath.Join(signerDir, "app.sock")
	sockSrv, err := custody.StartSocketServerAt(signerSvc, sockPath, os.Getuid(), 0o600)
	if err != nil {
		t.Fatalf("StartSocketServerAt: %v", err)
	}
	t.Cleanup(func() { _ = sockSrv.Close(t.Context()) })

	s := fixtureStore(t)
	s.providerFixture = true
	s.ConfigureCustodySocket(sockPath)
	ts, token, csrf := startV2HTTPTestServer(t, s)

	// 1. List wallets confirms out-of-process isolated signer posture.
	listResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/wallets", "", token, "", "", "application/json")
	rawList, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK || !strings.Contains(string(rawList), `"processIsolated":true`) {
		t.Fatalf("expected processIsolated=true from GET /api/v1/wallets, got code=%d body=%s", listResp.StatusCode, rawList)
	}

	// 2. Creating a wallet works via socket and core's dataDir never creates signer-vault/.
	cr := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/create", ts.URL, token, csrf,
		`{"name":"Isolated Bot","role":"agent","rpcUrl":"https://api.mainnet-beta.solana.com"}`, "application/json")
	rawCR, _ := io.ReadAll(cr.Body)
	cr.Body.Close()
	if cr.StatusCode != http.StatusOK {
		t.Fatalf("socket create wallet failed: %d", cr.StatusCode)
	}
	var createdSock struct {
		Wallet custody.WalletPublic `json:"wallet"`
	}
	_ = json.Unmarshal(rawCR, &createdSock)
	if _, err := os.Stat(filepath.Join(s.dir, "signer-vault")); !os.IsNotExist(err) {
		t.Fatalf("expected core dir to have NO signer-vault/, got err=%v", err)
	}

	// 2b. Creating a native signing wallet IMMEDIATELY disables passwordless local auto-entry even before password setup!
	prePWLocalReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/auth/local", strings.NewReader(`{"action":"browser"}`))
	prePWLocalReq.Header.Set("Origin", ts.URL)
	prePWLocalReq.Header.Set("Content-Type", "application/json")
	prePWLocalReq.Header.Set("X-Fased-Local", "1")
	prePWLocalResp, err := http.DefaultClient.Do(prePWLocalReq)
	if err != nil {
		t.Fatalf("POST /api/v1/auth/local pre-pw: %v", err)
	}
	prePWLocalResp.Body.Close()
	if prePWLocalResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 blocking passwordless local auto-entry once native wallet exists, got %d", prePWLocalResp.StatusCode)
	}

	// 3. Setting Local owner password enrolls in-signer owner auth too, blocking direct app.sock /backup without proof!
	pwResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/auth/password", ts.URL, token, csrf, `{"password":"correct-owner-pass-2026"}`, "application/json")
	pwResp.Body.Close()
	if pwResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/password failed: %d", pwResp.StatusCode)
	}
	directClient, err := custody.NewSocketClient(sockPath)
	if err != nil {
		t.Fatalf("NewSocketClient: %v", err)
	}
	defer directClient.Close()
	if _, _, err := directClient.ExportEncryptedRecovery(ctx, createdSock.Wallet.ID, "attacker-pass-2026", "compromised-app"); err == nil {
		t.Fatalf("expected compromised direct app.sock /backup call to be rejected by fased-signer")
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/auth/local", strings.NewReader(`{"action":"browser"}`))
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fased-Local", "1")
	localResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/auth/local: %v", err)
	}
	localResp.Body.Close()
	if localResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 blocking passwordless local auto-entry after owner password set, got %d", localResp.StatusCode)
	}
}
