package core

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOrdinaryOnchainHTTPApprovalLockResultRecovery(t *testing.T) {
	testOrdinaryOnchainHTTPApprovalLockResultRecovery(t, false)
}

func TestRemoteCustodyOnchainHTTPApprovalLockResultRecovery(t *testing.T) {
	testOrdinaryOnchainHTTPApprovalLockResultRecovery(t, true)
}

func testOrdinaryOnchainHTTPApprovalLockResultRecovery(t *testing.T, remote bool) {
	if runtime.GOOS != "linux" {
		t.Skip("ordinary custody is Linux-only")
	}
	s := fixtureStore(t)
	token := claimFixture(t, s)
	if remote {
		id := tailnet.Identity{UserID: 1, NodeID: "owner-device", Login: "owner"}
		remoteToken, err := s.remoteSession(t.Context(), id, "", token)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Logout(t.Context(), remoteToken); err != nil {
			t.Fatal(err)
		}
		if err = s.Logout(t.Context(), token); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "signer")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	v, e := signer.OpenControlledOwnerCustody(dir, strings.Repeat("a", 32), "mainnet-beta")
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	pw := []byte("disposable ordinary HTTP password")
	chain := &onchaintest.Chain{}
	if e = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); e != nil {
		t.Fatal(e)
	}
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	bind := func(s *Store) {
		s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
			return signer.Response{Status: v.Status()}, nil
		}
		s.custodyOwnerSupported = true
		s.custodyNetwork = ref.Network
	}
	bind(s)
	record, e := s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	record.Accounts = append(record.Accounts, Account{Version: 4, ID: strings.Repeat("c", 32), Name: "Wallet", Mode: "managed", Network: ref.Network, PublicKey: ref.PublicKey, Signer: &ref})
	s.rpcAdmission = true
	saved, e := s.UpdateAccounts(t.Context(), record)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: strings.Repeat("c", 32), Revision: saved.Record.Revision, Endpoint: "https://mainnet.helius-rpc.com", Network: ref.Network}); e != nil {
		t.Fatal(e)
	}
	h := &swapHarness{s, v, chain, pw, "trading-analyst"}
	h.connect()
	handler, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	configureHandler := func() {
		if remote {
			handler.instanceLocation, handler.instanceOwnerUID, handler.instanceOwnerKind = "remote", 1000, "custody"
		}
	}
	configureHandler()
	request := func(action string, c SwapCommand, want int) AgentView {
		t.Helper()
		current, e := h.store.Agents(t.Context(), h.profile, "")
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(AgentCommand{Action: action, ProfileID: h.profile, Revision: current.Revision, Swap: &c})
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/agents", bytes.NewReader(body))
		req.Header.Set("Origin", "http://127.0.0.1:7331")
		req.Header.Set("Content-Type", "application/json")
		if remote {
			req.Header.Set(custodyOnchainHeader, h.store.instanceID)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-CSRF-Token", csrfToken(token))
		}
		w := httptest.NewRecorder()
		if remote {
			privateIPCHandler(handler).ServeHTTP(w, req)
		} else {
			handler.ServeHTTP(w, req)
		}
		if w.Code != want {
			t.Fatalf("%s: got %d, want %d", action, w.Code, want)
		}
		var out AgentView
		if want == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("invalid view")
		}
		return out
	}
	quote := func() *SwapReview {
		t.Helper()
		return request("swap-quote", SwapCommand{Request: &onchain.Request{AccountID: strings.Repeat("c", 32), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000}}, 200).Onchain.Review
	}
	p := quote()
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = v.ApproveSwap(p.Proposal); e != nil {
		t.Fatal(e)
	}
	v.Lock()
	command := SwapCommand{ID: p.Proposal.Quote.ID, Digest: p.Digest}
	request("swap-submit", command, 400)
	current, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil || len(current.Onchain.Tasks) != 0 {
		t.Fatal("locked submission mutated tasks", e)
	}
	if _, e = v.CancelSwap(command.ID); e != nil {
		t.Fatal(e)
	}
	p = quote()
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = v.ApproveSwap(p.Proposal); e != nil {
		t.Fatal(e)
	}
	command = SwapCommand{ID: p.Proposal.Quote.ID, Digest: p.Digest}
	if request("swap-review", command, 200).Onchain.Review.Approval != "approved" {
		t.Fatal("missing owner approval")
	}
	queued := request("swap-submit", command, 200)
	if len(queued.Onchain.Tasks) != 1 {
		t.Fatal("missing shared task")
	}
	request("swap-submit", command, 400)
	chain.Unknown = true
	if e = h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	unknown := request("swap-view", SwapCommand{}, 200)
	if unknown.Onchain.Runs[0].Swap.State != "unknown" || chain.Submissions != 1 {
		t.Fatal("uncertain original replayed")
	}
	stateDir := h.store.dir
	h.store.Close()
	h.store, e = OpenStore(stateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer h.store.Close()
	bind(h.store)
	h.connect()
	handler, e = NewHTTPHandler(h.store, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	configureHandler()
	chain.Unknown = false
	settled := request("swap-reconcile", SwapCommand{ID: queued.Onchain.Tasks[0].ID}, 200)
	if settled.Onchain.Runs[0].Swap.State != "confirmed" || chain.Submissions != 1 || !v.Status().RecoveryRequired {
		t.Fatal("recovery lost original or holds")
	}
}
