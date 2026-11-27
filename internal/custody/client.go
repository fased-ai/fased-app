package custody

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SocketClient implements Backend against an isolated out-of-process fased-signer daemon over Unix socket.
type SocketClient struct {
	sockPath string
	client   *http.Client
}

// NewSocketClient connects to a running fased-signer Unix domain socket.
func NewSocketClient(sockPath string) (*SocketClient, error) {
	clean := strings.TrimSpace(sockPath)
	if clean == "" {
		return nil, errors.New("custody socket path required")
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", clean)
		},
	}
	return &SocketClient{
		sockPath: clean,
		client:   &http.Client{Transport: tr, Timeout: 35 * time.Second},
	}, nil
}

func (c *SocketClient) Close() error {
	if tr, ok := c.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}

func mapSentinelCode(code, msg string) error {
	var base error
	switch code {
	case "wallet_not_found":
		base = ErrWalletNotFound
	case "review_not_found":
		base = ErrReviewNotFound
	case "ledger_not_found":
		base = ErrLedgerNotFound
	case "policy_denied":
		base = ErrPolicyDenied
	case "role_denied":
		base = ErrRoleDenied
	case "policy_conflict":
		base = ErrPolicyConflict
	case "wrong_password":
		base = ErrWrongPassword
	case "owner_auth_required":
		base = ErrOwnerAuthRequired
	case "tx_tampered":
		base = ErrTxTampered
	case "ambiguous_state":
		base = ErrAmbiguousState
	default:
		if msg == "" {
			return errors.New("signer daemon request failed")
		}
		return errors.New(msg)
	}
	if msg == "" || msg == base.Error() {
		return base
	}
	return fmt.Errorf("%w: %s", base, msg)
}

func (c *SocketClient) do(ctx context.Context, method, path string, reqBody, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://fased-signer"+path, bodyReader)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if proof := OwnerProofFromContext(ctx); proof != "" {
		req.Header.Set(ownerProofHeader, proof)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("signer daemon unreachable at %s: %w", c.sockPath, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var errEnv struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(raw, &errEnv) == nil && (errEnv.Error != "" || errEnv.Code != "") {
			return mapSentinelCode(errEnv.Code, errEnv.Error)
		}
		return fmt.Errorf("signer daemon HTTP %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *SocketClient) SecurityStatus(ctx context.Context) (SecurityStatus, error) {
	var resp struct {
		Security SecurityStatus `json:"security"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &resp); err != nil {
		return SecurityStatus{}, err
	}
	resp.Security.ProcessIsolated = true
	resp.Security.SocketPath = c.sockPath
	return resp.Security, nil
}

func (c *SocketClient) VerifyAuditChain(ctx context.Context) (int, error) {
	var resp struct {
		Count int `json:"count"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/audit/verify", nil, &resp); err != nil {
		return 0, err
	}
	return resp.Count, nil
}

func (c *SocketClient) HasOwnerAuth(ctx context.Context) (bool, error) {
	var resp struct {
		HasOwnerAuth bool `json:"hasOwnerAuth"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/owner/status", nil, &resp); err != nil {
		return false, err
	}
	return resp.HasOwnerAuth, nil
}

func (c *SocketClient) ConfigureOwnerAuth(ctx context.Context, password string) error {
	return c.do(ctx, http.MethodPost, "/v1/owner/configure", map[string]string{
		"password": password,
	}, nil)
}

func (c *SocketClient) AuthorizeOwnerAction(ctx context.Context, password, action, walletID string) (string, time.Time, error) {
	return c.AuthorizeOwnerActionBound(ctx, password, action, walletID, "")
}

func (c *SocketClient) AuthorizeOwnerActionBound(ctx context.Context, password, action, walletID, bindingDigest string) (string, time.Time, error) {
	var resp struct {
		Proof     string `json:"proof"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/owner/authorize", map[string]string{
		"password":      password,
		"action":        action,
		"walletId":      walletID,
		"bindingDigest": bindingDigest,
	}, &resp); err != nil {
		return "", time.Time{}, err
	}
	return resp.Proof, time.Unix(resp.ExpiresAt, 0).UTC(), nil
}

func (c *SocketClient) ListOwnerPasskeys(ctx context.Context) ([]OwnerPasskey, error) {
	var resp struct {
		Passkeys []OwnerPasskey `json:"passkeys"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/owner/passkeys", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Passkeys == nil {
		return []OwnerPasskey{}, nil
	}
	return resp.Passkeys, nil
}

func (c *SocketClient) DeleteOwnerPasskey(ctx context.Context, credentialID string) (bool, error) {
	var resp struct {
		Deleted bool `json:"deleted"`
	}
	if err := c.do(ctx, http.MethodDelete, "/v1/owner/passkeys/"+url.PathEscape(credentialID), nil, &resp); err != nil {
		return false, err
	}
	return resp.Deleted, nil
}

func (c *SocketClient) BeginPasskeyRegistration(ctx context.Context, rpID, origin, label string) (PasskeyRegisterOptions, error) {
	var out PasskeyRegisterOptions
	err := c.do(ctx, http.MethodPost, "/v1/owner/passkeys/register/begin", map[string]string{
		"rpId": rpID, "origin": origin, "label": label,
	}, &out)
	return out, err
}

func (c *SocketClient) FinishPasskeyRegistration(ctx context.Context, req PasskeyRegisterFinishRequest) (OwnerPasskey, error) {
	var out OwnerPasskey
	err := c.do(ctx, http.MethodPost, "/v1/owner/passkeys/register/finish", req, &out)
	return out, err
}

func (c *SocketClient) BeginPasskeyAssertion(ctx context.Context, rpID, origin, action, walletID, bindingDigest string) (PasskeyAssertOptions, error) {
	var out PasskeyAssertOptions
	err := c.do(ctx, http.MethodPost, "/v1/owner/passkeys/assert/begin", map[string]string{
		"rpId": rpID, "origin": origin, "action": action, "walletId": walletID, "bindingDigest": bindingDigest,
	}, &out)
	return out, err
}

func (c *SocketClient) FinishPasskeyAssertion(ctx context.Context, req PasskeyAssertFinishRequest) (string, time.Time, string, string, string, error) {
	var resp struct {
		Proof         string `json:"proof"`
		ExpiresAt     int64  `json:"expiresAt"`
		Action        string `json:"action"`
		WalletID      string `json:"walletId"`
		BindingDigest string `json:"bindingDigest"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/owner/passkeys/assert/finish", req, &resp); err != nil {
		return "", time.Time{}, "", "", "", err
	}
	return resp.Proof, time.Unix(resp.ExpiresAt, 0).UTC(), resp.Action, resp.WalletID, resp.BindingDigest, nil
}

func (c *SocketClient) ImportSolanaKeypairFile(ctx context.Context, absPath, name, role, primaryRPCURL string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPost, "/v1/owner/import-raw", map[string]string{
		"filePath": absPath, "name": name, "role": role, "primaryRpcUrl": primaryRPCURL,
	}, &out)
	return out, err
}

func (c *SocketClient) ExportRawKeypairFile(ctx context.Context, walletID, absOutPath string, ack bool) error {
	return c.do(ctx, http.MethodPost, "/v1/owner/export-raw", map[string]any{
		"walletId": walletID, "outputPath": absOutPath, "acknowledgeCustodyReduction": ack,
	}, nil)
}

func (c *SocketClient) ListWallets(ctx context.Context) ([]WalletPublic, error) {
	var out []WalletPublic
	err := c.do(ctx, http.MethodGet, "/v1/wallets", nil, &out)
	return out, err
}

func (c *SocketClient) GetWallet(ctx context.Context, idOrHandle string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodGet, "/v1/wallets/"+url.PathEscape(idOrHandle), nil, &out)
	return out, err
}

func (c *SocketClient) ResolveWallet(ctx context.Context, sel WalletSelector) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPost, "/v1/wallets/resolve", sel, &out)
	return out, err
}

func (c *SocketClient) CreateNativeWallet(ctx context.Context, name, role, primaryRPCURL string) (WalletPublic, error) {
	var resp struct {
		Wallet WalletPublic `json:"wallet"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/wallets/create", map[string]string{
		"name": name, "role": role, "primaryRpcUrl": primaryRPCURL,
	}, &resp)
	return resp.Wallet, err
}

func (c *SocketClient) AttachExternalWallet(ctx context.Context, custodyMode, name, role, publicKeyB58, primaryRPCURL string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPost, "/v1/wallets/attach", map[string]string{
		"custody": custodyMode, "name": name, "role": role, "publicKey": publicKeyB58, "primaryRpcUrl": primaryRPCURL,
	}, &out)
	return out, err
}

func (c *SocketClient) UpdatePolicy(ctx context.Context, walletID string, next WalletPolicy, confirmLoosen bool, expectedPolicyHash, actor string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPut, "/v1/wallets/"+url.PathEscape(walletID)+"/policy", map[string]any{
		"policy": next, "confirmLoosen": confirmLoosen, "expectedPolicyHash": expectedPolicyHash, "actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) SetFreeze(ctx context.Context, walletID string, frozen bool, reason, actor string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPost, "/v1/wallets/"+url.PathEscape(walletID)+"/freeze", map[string]any{
		"frozen": frozen, "reason": reason, "actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) GetGlobalFreeze(ctx context.Context) (GlobalFreezeState, error) {
	var out GlobalFreezeState
	err := c.do(ctx, http.MethodGet, "/v1/global-freeze", nil, &out)
	return out, err
}

func (c *SocketClient) SetGlobalFreeze(ctx context.Context, active bool, reason, actor string) (GlobalFreezeState, error) {
	var out GlobalFreezeState
	err := c.do(ctx, http.MethodPost, "/v1/global-freeze", map[string]any{
		"active": active, "reason": reason, "actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) UpdateConnections(ctx context.Context, walletID string, next WalletConnections, actor string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPut, "/v1/wallets/"+url.PathEscape(walletID)+"/connections", map[string]any{
		"connections": next, "actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) SetWalletRPC(ctx context.Context, walletID, primaryURL, fallbackURL, actor string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPut, "/v1/wallets/"+url.PathEscape(walletID)+"/rpc", map[string]string{
		"primaryUrl": primaryURL, "fallbackUrl": fallbackURL, "actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) DeleteWallet(ctx context.Context, walletID string) (bool, error) {
	var resp struct {
		Deleted bool `json:"deleted"`
	}
	err := c.do(ctx, http.MethodDelete, "/v1/wallets/"+url.PathEscape(walletID), nil, &resp)
	return resp.Deleted, err
}

func (c *SocketClient) FetchBalance(ctx context.Context, walletID string) (BalanceResult, error) {
	var out BalanceResult
	err := c.do(ctx, http.MethodGet, "/v1/wallets/"+url.PathEscape(walletID)+"/balance", nil, &out)
	return out, err
}

func (c *SocketClient) PrepareReview(ctx context.Context, req PrepareReviewRequest) (WalletReview, error) {
	var out WalletReview
	err := c.do(ctx, http.MethodPost, "/v1/wallets/"+url.PathEscape(req.WalletID)+"/reviews/prepare", req, &out)
	return out, err
}

func (c *SocketClient) GetReview(ctx context.Context, walletID, reviewID string) (WalletReview, error) {
	var out WalletReview
	err := c.do(ctx, http.MethodGet, "/v1/wallets/"+url.PathEscape(walletID)+"/reviews/"+url.PathEscape(reviewID), nil, &out)
	return out, err
}

func (c *SocketClient) ExecuteNativeReview(ctx context.Context, walletID, reviewID, actor string) (LedgerEntry, error) {
	var out LedgerEntry
	err := c.do(ctx, http.MethodPost, "/v1/wallets/"+url.PathEscape(walletID)+"/reviews/"+url.PathEscape(reviewID)+"/execute", map[string]string{
		"actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) SubmitHardwareSignedReview(ctx context.Context, walletID, reviewID, signedTxB64, actor string) (LedgerEntry, error) {
	var out LedgerEntry
	err := c.do(ctx, http.MethodPost, "/v1/wallets/"+url.PathEscape(walletID)+"/reviews/"+url.PathEscape(reviewID)+"/submit-signed", map[string]string{
		"signedTxBase64": signedTxB64, "actor": actor,
	}, &out)
	return out, err
}

func (c *SocketClient) ReconcileLedgerEntry(ctx context.Context, walletID, entryID string) (LedgerEntry, error) {
	var out LedgerEntry
	err := c.do(ctx, http.MethodPost, "/v1/wallets/"+url.PathEscape(walletID)+"/ledger/"+url.PathEscape(entryID)+"/reconcile", nil, &out)
	return out, err
}

func (c *SocketClient) ListActivity(ctx context.Context, walletID string) ([]WalletReview, []LedgerEntry, []AuditEntry, error) {
	var resp struct {
		Reviews []WalletReview `json:"reviews"`
		Ledger  []LedgerEntry  `json:"ledger"`
		Audit   []AuditEntry   `json:"audit"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/wallets/"+url.PathEscape(walletID)+"/activity", nil, &resp)
	return resp.Reviews, resp.Ledger, resp.Audit, err
}

func (c *SocketClient) ExportEncryptedRecovery(ctx context.Context, walletID, passphrase, actor string) ([]byte, string, error) {
	var resp struct {
		Archive     json.RawMessage `json:"archive"`
		Fingerprint string          `json:"fingerprint"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/wallets/"+url.PathEscape(walletID)+"/backup", map[string]string{
		"passphrase": passphrase, "actor": actor,
	}, &resp)
	return resp.Archive, resp.Fingerprint, err
}

func (c *SocketClient) ImportEncryptedRecovery(ctx context.Context, archiveRaw []byte, passphrase, name, role, primaryRPCURL string) (WalletPublic, error) {
	var out WalletPublic
	err := c.do(ctx, http.MethodPost, "/v1/wallets/restore", map[string]any{
		"archive": json.RawMessage(archiveRaw), "passphrase": passphrase, "name": name, "role": role, "primaryRpcUrl": primaryRPCURL,
	}, &out)
	return out, err
}
