package custody

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWaveS1OwnerAuthNoAutoEnrollNoAppConfigureAndRateLimit(t *testing.T) {
	ctx := context.Background()
	svc, err := NewService(Config{
		DataDir:          t.TempDir(),
		FixtureMode:      true,
		FastKDF:          true,
		RequireOwnerAuth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// C1: AuthorizeOwnerAction MUST NOT auto-enroll when unenrolled.
	if _, _, err := svc.AuthorizeOwnerAction(ctx, "attacker-password-1234", "execute", "w-1"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired on unenrolled authorize, got %v", err)
	}
	if has, _ := svc.HasOwnerAuth(ctx); has {
		t.Fatal("AuthorizeOwnerAction auto-enrolled password!")
	}

	// C1: app.sock MUST reject /v1/owner/configure when RequireOwnerAuth is true,
	// whereas owner.sock permits initial enrollment.
	sockDir := t.TempDir()
	appSock, err := StartSocketServerAt(svc, filepath.Join(sockDir, "app.sock"), os.Getuid(), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer appSock.Close(ctx)
	ownerSock, err := StartOwnerSocketServerAt(svc, filepath.Join(sockDir, "owner.sock"), os.Getuid(), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerSock.Close(ctx)

	appClient, err := NewSocketClient(appSock.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := appClient.ConfigureOwnerAuth(ctx, "attacker-password-1234"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected app.sock ConfigureOwnerAuth blocked under RequireOwnerAuth, got %v", err)
	}

	ownerClient, err := NewSocketClient(ownerSock.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	const ownerPass = "real-owner-secret-2026"
	if err := ownerClient.ConfigureOwnerAuth(ctx, ownerPass); err != nil {
		t.Fatalf("owner.sock ConfigureOwnerAuth failed: %v", err)
	}

	// C1: Wildcard walletID "*" rejected for single-wallet actions (only allowed for "restore").
	if _, _, err := appClient.AuthorizeOwnerAction(ctx, ownerPass, "execute", "*"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected wildcard walletId rejected for execute, got %v", err)
	}
	if _, _, err := appClient.AuthorizeOwnerAction(ctx, ownerPass, "restore", "*"); err != nil {
		t.Fatalf("expected wildcard walletId allowed for restore, got %v", err)
	}

	// C1: In-signer failed-attempt rate limit blocks rapid brute-force after 5 wrong passwords.
	for i := 0; i < ownerMaxFailsPerMn; i++ {
		if _, _, err := svc.AuthorizeOwnerAction(ctx, "wrong-password-123456", "execute", "w-1"); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("attempt %d: expected ErrWrongPassword, got %v", i, err)
		}
	}
	if _, _, err := svc.AuthorizeOwnerAction(ctx, ownerPass, "execute", "w-1"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected rate-limited ErrWrongPassword even with valid password during lockout, got %v", err)
	}
}

func TestWaveS1ActorBypassBlockedForManualReview(t *testing.T) {
	ctx := context.Background()
	svc, err := NewService(Config{
		DataDir:          t.TempDir(),
		FixtureMode:      true,
		FastKDF:          true,
		RequireOwnerAuth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	const ownerPass = "strong-owner-secret-2026"
	if err := svc.ConfigureOwnerAuth(ctx, ownerPass); err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateNativeWallet(ctx, "Trader", RoleTrading, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := svc.CreateNativeWallet(ctx, "Vault", RoleVault, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}

	pol := w.Policy
	pol.ApprovalMode = ApprovalBoundedAuto
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{dest.PublicKey}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	if _, err := svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateConnections(ctx, w.ID, WalletConnections{
		AllowTradeBots:   true,
		AllowTradeManual: true,
		AgentIDs:         []string{"agent-ok"},
	}, "owner"); err != nil {
		t.Fatal(err)
	}

	sock, err := StartSocketServerAt(svc, filepath.Join(t.TempDir(), "app.sock"), os.Getuid(), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer sock.Close(ctx)
	client, err := NewSocketClient(sock.SocketPath())
	if err != nil {
		t.Fatal(err)
	}

	// C2: Prepare a MANUAL review (IsBotOrAuto: false), then attempt to execute via app.sock claiming actor="trade-bot".
	manualRev, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: dest.PublicKey,
		AmountHuman: "0.1",
		IsBotOrAuto: false,
	})
	if err != nil {
		t.Fatalf("PrepareReview manual failed: %v", err)
	}
	if _, err := client.ExecuteNativeReview(ctx, w.ID, manualRev.ID, "trade-bot"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired blocking actor=trade-bot on manual review, got %v", err)
	}

	// C2: Unbound AgentID MUST be rejected at PrepareReview.
	if _, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: dest.PublicKey,
		AmountHuman: "0.1",
		IsBotOrAuto: true,
		AgentID:     "rogue-agent",
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for unbound AgentID, got %v", err)
	}

	// Legitimate bound agent prepared with IsBotOrAuto:true succeeds with matching actor="agent:agent-ok".
	botRev, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: dest.PublicKey,
		AmountHuman: "0.1",
		IsBotOrAuto: true,
		AgentID:     "agent-ok",
	})
	if err != nil {
		t.Fatalf("PrepareReview bound agent failed: %v", err)
	}
	// Wrong actor even on bot review MUST fail without owner proof.
	if _, err := client.ExecuteNativeReview(ctx, w.ID, botRev.ID, "trade-bot"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired when actor mismatches rev.AgentID, got %v", err)
	}
	if _, err := client.ExecuteNativeReview(ctx, w.ID, botRev.ID, "agent:agent-ok"); err != nil {
		t.Fatalf("expected bound agent bounded-auto execution to succeed, got %v", err)
	}
}

func TestWaveS1ConcurrentReconcileSingleCapRelease(t *testing.T) {
	ctx := context.Background()
	rt := p0RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		var respBody string
		switch req.Method {
		case "getGenesisHash":
			respBody = `{"jsonrpc":"2.0","id":1,"result":"` + SolanaMainnetGenesis + `"}`
		case "getSlot":
			respBody = `{"jsonrpc":"2.0","id":1,"result":300000000}`
		case "getLatestBlockhash":
			respBody = `{"jsonrpc":"2.0","id":1,"result":{"value":{"blockhash":"` + SolanaMainnetGenesis + `"}}}`
		case "simulateTransaction":
			respBody = `{"jsonrpc":"2.0","id":1,"result":{"value":{"err":null}}}`
		case "sendTransaction":
			respBody = `{"jsonrpc":"2.0","id":1,"result":"5ConcurrentTestSig111111111111111111111111111111111111111111111111"}`
		case "getSignatureStatuses":
			// Both primary & witness confirm failure so cap release happens cleanly.
			time.Sleep(15 * time.Millisecond)
			respBody = `{"jsonrpc":"2.0","id":1,"result":{"value":[{"err":"InstructionError","confirmationStatus":"confirmed"}]}}`
		default:
			respBody = `{"jsonrpc":"2.0","id":1,"result":null}`
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(respBody)),
		}, nil
	})

	svc, err := NewService(Config{
		DataDir:      t.TempDir(),
		FastKDF:      true,
		RPCTransport: rt,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	w, err := svc.CreateNativeWallet(ctx, "Conc", RoleTrading, "https://primary.rpc.example")
	if err != nil {
		t.Fatal(err)
	}
	dest, err := svc.CreateNativeWallet(ctx, "Dest", RoleVault, "https://primary.rpc.example")
	if err != nil {
		t.Fatal(err)
	}
	pol := w.Policy
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{dest.PublicKey}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	if _, err := svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner"); err != nil {
		t.Fatal(err)
	}

	rev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: dest.PublicKey,
		AmountHuman: "0.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	led, err := svc.ExecuteNativeReview(ctx, w.ID, rev.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed an extra reservation (simulating another in-flight tx) so a double-decrement bug would drop below 50_000_000!
	svc.mu.Lock()
	day := time.Unix(led.CreatedAt, 0).UTC().Format("2006-01-02")
	_, _ = svc.db.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = reserved_raw + 50000000 WHERE wallet_id = ? AND day_utc = ?`, w.ID, day)
	svc.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.ReconcileLedgerEntry(ctx, w.ID, led.ID)
		}()
	}
	wg.Wait()

	after, err := svc.GetWallet(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DailyReservedLamports != 50_000_000 {
		t.Fatalf("expected exact remaining reservation 50000000 (single release), got %d", after.DailyReservedLamports)
	}
}

func TestWaveS1SSRFHardeningAndMultiIPCDN(t *testing.T) {
	// H3: Reserved/NAT64 ranges must be rejected.
	for _, raw := range []string{"0.1.2.3", "192.0.2.1", "198.18.0.1", "64:ff9b::1", "2001:db8::1"} {
		if IsPublicUnicastIP(netip.MustParseAddr(raw)) {
			t.Fatalf("expected non-public IP %s rejected", raw)
		}
	}

	// H3: Multi-IP (>6 A records) CDN host must dial cleanly (fixes len(targets) > 6 bug).
	eightIPs := []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("1.1.1.2"),
		netip.MustParseAddr("1.1.1.3"),
		netip.MustParseAddr("1.1.1.4"),
		netip.MustParseAddr("1.1.1.5"),
		netip.MustParseAddr("1.1.1.6"),
		netip.MustParseAddr("1.1.1.7"),
		netip.MustParseAddr("1.1.1.8"),
	}
	dialed := 0
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	conn, err := dialSSRFSafe(
		context.Background(), "tcp", "cdn.rpc.example:443", false,
		func(context.Context, string, string) ([]netip.Addr, error) { return eightIPs, nil },
		func(context.Context, string, string) (net.Conn, error) {
			dialed++
			return c1, nil
		},
	)
	if err != nil || conn == nil || dialed != 1 {
		t.Fatalf("expected multi-IP CDN host to dial cleanly, dialed=%d err=%v", dialed, err)
	}

	// H3: Remote hostname resolving to 127.0.0.1 MUST be blocked even when allowLoopback=true.
	_, err = dialSSRFSafe(
		context.Background(), "tcp", "rebind.evil.example:8899", true,
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			return c1, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected DNS rebinding to loopback blocked even with allowLoopback=true, got %v", err)
	}

	// H3: Production CreateNativeWallet blocks http://127.0.0.1 when AllowLoopbackRPC=false.
	prodSvc, err := NewService(Config{DataDir: t.TempDir(), FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer prodSvc.Close()
	if _, err := prodSvc.CreateNativeWallet(context.Background(), "SSRF", RoleTrading, "http://127.0.0.1:8899"); err == nil {
		t.Fatal("expected production CreateNativeWallet to reject loopback RPC URL")
	}
}
