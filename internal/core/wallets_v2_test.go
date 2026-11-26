package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custody"
)

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

	// 4. Create Vault wallet and confirm autonomous connection binding is hard-blocked (403).
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

	badConn := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+createdVault.Wallet.ID+"/connections", ts.URL, token, csrf,
		`{"agentIds":["wen-operator"],"allowTradeBots":true}`, "application/json")
	badConn.Body.Close()
	if badConn.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 binding vault to autonomous bots/agents, got %d", badConn.StatusCode)
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
	polResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/policy", ts.URL, token, csrf, string(polPayload), "application/json")
	rawPol, _ := io.ReadAll(polResp.Body)
	polResp.Body.Close()
	if polResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /policy failed: code=%d body=%s", polResp.StatusCode, rawPol)
	}

	// Prepare & execute via native signer -> updates balance/ledger cleanly.
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

	execResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/reviews/"+revOut.Review.ID+"/execute", ts.URL, token, csrf, `{}`, "application/json")
	execResp.Body.Close()
	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /execute failed: %d", execResp.StatusCode)
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
	hwPolResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/wallets/"+hwOut.Wallet.ID+"/policy", ts.URL, token, csrf, string(hwPolPayload), "application/json")
	hwPolResp.Body.Close()

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

	// 7. Encrypted Argon2id Backup returns encrypted archive ONLY (no plaintext key).
	bakResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/backup", ts.URL, token, csrf, `{"passphrase":"secret-pass-123"}`, "application/json")
	rawBak, _ := io.ReadAll(bakResp.Body)
	bakResp.Body.Close()
	if bakResp.StatusCode != http.StatusOK || !strings.Contains(string(rawBak), `"fased-recovery/1"`) {
		t.Fatalf("POST /backup failed: code=%d body=%s", bakResp.StatusCode, rawBak)
	}
}
