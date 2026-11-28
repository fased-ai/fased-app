package custody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestRoleMiningLV1XPolicyCapsAndApprovalModes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// Create dedicated RoleMining wallet on Devnet.
	mineWallet, err := svc.CreateNativeWallet(ctx, "Devnet Miner", RoleMining, "https://api.devnet.solana.com")
	if err != nil {
		t.Fatal(err)
	}
	// Configure bounded_auto with Devnet LV1X policy (0.05 SOL/round, 0.15 SOL/day).
	miningPol := WalletPolicy{
		Role:              RoleMining,
		ApprovalMode:      ApprovalBoundedAuto,
		AutoApproveMaxUSD: 25.0,
		Operations:        AllMiningOperations(),
		Programs:          []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram, onchain.LV1XDevnetProgramID},
		SOLCap: AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 60_000_000,
			MaxDailyRaw: 150_000_000,
		},
		Mining: &MiningPolicy{
			Cluster:               "devnet",
			ProgramID:             onchain.LV1XDevnetProgramID,
			LedgerID:              onchain.LV1XDevnetLedgerID,
			ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
			MaxSOLPerRoundLamport: 50_000_000,
			MaxDailySOLLamports:   150_000_000,
			AllowedStrategies:     []string{"wide", "center"},
			AutoClaim:             true,
		},
	}
	mineWallet, err = svc.UpdatePolicy(ctx, mineWallet.ID, miningPol, true, mineWallet.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy RoleMining: %v", err)
	}

	// Mainnet LV1X policy update MUST be hard-rejected while LV1XMainnetProgramID is empty.
	badMainnet := mineWallet.Policy
	badMainnet.Mining = &MiningPolicy{
		Cluster:               "mainnet-beta",
		ProgramID:             onchain.LV1XDevnetProgramID,
		LedgerID:              onchain.LV1XDevnetLedgerID,
		ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
		MaxSOLPerRoundLamport: 20_000_000,
		AllowMainnet:          true,
	}
	if _, err := svc.UpdatePolicy(ctx, mineWallet.ID, badMainnet, true, mineWallet.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for Mainnet LV1X policy, got %v", err)
	}

	// Generic trading bots CANNOT use RoleMining wallets even with Auto policy.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    mineWallet.ID,
		Operation:   "solana.nativeTransfer",
		Destination: mineWallet.PublicKey,
		AmountRaw:   10_000_000,
		IsBotOrAuto: true,
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for generic bot on RoleMining wallet, got %v", err)
	}

	// RoleVault CANNOT execute mining operations.
	vaultW, err := svc.CreateNativeWallet(ctx, "Vault", RoleVault, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  vaultW.ID,
		Operation: MiningOpCommit,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 20_000_000,
		},
	}); err == nil {
		t.Fatal("expected RoleVault mining denial")
	}

	// Unallowed strategy rejected.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  mineWallet.ID,
		Operation: MiningOpCommit,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 20_000_000,
			StrategyID:     "trend_up",
		},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for unallowed strategy, got %v", err)
	}

	// Valid bounded_auto commit executes cleanly within per-round cap (30M <= 50M).
	rev1, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    mineWallet.ID,
		Operation:   MiningOpCommit,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 30_000_000,
			StrategyID:     "wide",
		},
	})
	if err != nil {
		t.Fatalf("PrepareReview mining.lv1x.commit failed: %v", err)
	}
	if !allowMiningAutonomousExecution(mineWallet, rev1, "task:tsk-mine-1") {
		t.Fatal("expected allowMiningAutonomousExecution=true for RoleMining bounded_auto")
	}
	led1, err := svc.ExecuteNativeReview(ctx, mineWallet.ID, rev1.ID, "task:tsk-mine-1")
	if err != nil || led1.State != LedgerConfirmed {
		t.Fatalf("ExecuteNativeReview failed: state=%s err=%v", led1.State, err)
	}

	// Second commit in SAME round exceeding 50M total (30M + 25M = 55M > 50M) MUST be denied.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  mineWallet.ID,
		Operation: MiningOpCommit,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      1,
			AmountLamports: 25_000_000,
			StrategyID:     "center",
		},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected per-round cap rejection (55M > 50M), got %v", err)
	}

	// Semi-Auto permits unattended AutoClaim, but refuses unattended Commit.
	semiPol := mineWallet.Policy
	semiPol.ApprovalMode = ApprovalSemiAuto
	semiWallet, err := svc.UpdatePolicy(ctx, mineWallet.ID, semiPol, false, mineWallet.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("tighten to Semi-Auto: %v", err)
	}
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    semiWallet.ID,
		Operation:   MiningOpCommit,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      102,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 20_000_000,
		},
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for Semi-Auto unattended commit, got %v", err)
	}
	claimRev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    semiWallet.ID,
		Operation:   MiningOpClaim,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:   101,
			ShardID:     0,
			Page:        0,
			EntriesMask: 0x01,
		},
	})
	if err != nil {
		t.Fatalf("expected Semi-Auto unattended AutoClaim allowed, got %v", err)
	}
	if !allowMiningAutonomousExecution(semiWallet, claimRev, "mining-engine") {
		t.Fatal("expected allowMiningAutonomousExecution=true for Semi-Auto AutoClaim")
	}

	// Reveal, ClaimSAT, ClaimBTC, Stake/Activate/ClaimStaking/Unstake, and Session Deposit.
	saltHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	for _, tc := range []struct {
		name string
		op   string
		req  MiningLV1XRequest
		auto bool
	}{
		{"reveal", MiningOpReveal, MiningLV1XRequest{RoundSlot: 101, ShardID: 0, Page: 0, EntryRow: 0, Selection: 2, SaltHex: saltHex}, true},
		{"claim_sat", MiningOpClaimSat, MiningLV1XRequest{RoundSlot: 101, ShardID: 0, Page: 0, EntryRow: 0, CreateATA: true}, true},
		{"claim_btc", MiningOpClaimBtc, MiningLV1XRequest{RoundSlot: 101, ShardID: 0, Page: 0, EntryRow: 0, BtcMint: onchain.LV1XDevnetBTCMint, CreateATA: true}, true},
		{"claim_staking", MiningOpClaimStaking, MiningLV1XRequest{BtcMint: onchain.LV1XDevnetBTCMint, CreateATA: true}, true},
		{"stake", MiningOpStake, MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms}, false},
		{"activate", MiningOpActivateStake, MiningLV1XRequest{}, false},
		{"unstake", MiningOpUnstake, MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms, CreateATA: true}, false},
		{"sess_deposit", MiningOpSession, MiningLV1XRequest{SessionAction: "deposit", SessionRef: 7, AmountLamports: 15_000_000}, false},
	} {
		rev, pErr := svc.PrepareReview(ctx, PrepareReviewRequest{
			WalletID:    semiWallet.ID,
			Operation:   tc.op,
			IsBotOrAuto: tc.auto,
			MiningLV1X:  &tc.req,
		})
		if pErr != nil {
			t.Fatalf("%s PrepareReview failed: %v", tc.name, pErr)
		}
		actor := "owner"
		if tc.auto {
			actor = "mining-engine"
		}
		led, eErr := svc.ExecuteNativeReview(ctx, semiWallet.ID, rev.ID, actor)
		if eErr != nil || led.State != LedgerConfirmed {
			t.Fatalf("%s ExecuteNativeReview failed: state=%s err=%v", tc.name, led.State, eErr)
		}
	}

	// Autonomous Stake on Semi-Auto MUST be rejected.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    semiWallet.ID,
		Operation:   MiningOpStake,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms},
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for autonomous stake, got %v", err)
	}
}

func buildTestLV1XManifestEnvelope(t *testing.T, priv ed25519.PrivateKey, _ ed25519.PublicKey, keyID string, payload onchain.LV1XManifestPayload) []byte {
	t.Helper()
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := make([]byte, 0, len(onchain.LV1XManifestDomain)+len(payloadRaw))
	msg = append(msg, []byte(onchain.LV1XManifestDomain)...)
	msg = append(msg, payloadRaw...)
	sig := ed25519.Sign(priv, msg)
	env := onchain.LV1XManifestEnvelope{
		Version:   1,
		KeyID:     keyID,
		Payload:   json.RawMessage(payloadRaw),
		Signature: sig,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func buildTestLV1XRuntimeAccount(t *testing.T, ledgerID, mintID, btcMintID string) []byte {
	t.Helper()
	var body bytes.Buffer
	writeKey := func(b58 string) {
		pk, err := onchain.Key(b58)
		if err != nil {
			t.Fatalf("invalid key %s: %v", b58, err)
		}
		body.Write(pk)
	}
	writeU64 := func(v uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		body.Write(b[:])
	}
	writeKey(ledgerID)
	writeU64(0) // epoch
	for range 3 {
		writeKey(ledgerID) // admins[3]
	}
	for range 3 {
		writeKey(ledgerID) // recorders[3]
	}
	body.WriteByte(2) // quorumThreshold
	writeKey(mintID)
	writeKey(btcMintID)
	body.WriteByte(0)        // phase
	body.Write([]byte{0, 0}) // paused, bootstrapWinddown
	writeU64(0)              // awarded
	writeU64(0)              // pending
	writeU64(0)              // boostAwarded
	writeU64(0)              // boostPending
	body.WriteByte(0)        // boostRetired
	body.WriteByte(0)        // latestSlot None
	body.WriteByte(0)        // settledSlot None
	writeU64(0)              // bootstrapGross
	writeU64(0)              // lpSol
	writeU64(0)              // lpSat
	writeU64(0)              // operating
	writeU64(0)              // normalFrom
	writeU64(0)              // graduatedAt
	writeKey(ledgerID)       // pool
	writeKey(ledgerID)       // btcPool
	writeU64(onchain.LV1XDefaultLPSolMin)
	writeU64(onchain.LV1XDefaultLPSatMin)

	out := make([]byte, onchain.LV1XSpaceRuntime)
	copy(out[:8], onchain.LV1XMagicRuntime)
	binary.LittleEndian.PutUint32(out[8:12], uint32(body.Len()))
	copy(out[12:], body.Bytes())
	return out
}

func TestCustodyMainnetUnlockRequiresManifestPlusLiveBPFPlusOwnerStepUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	now := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keyHash := sha256.Sum256(pub)
	keyID := hex.EncodeToString(keyHash[:])
	keys := map[string]string{keyID: hex.EncodeToString(pub)}

	elf := bytes.Repeat([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, 16) // 128 bytes
	elfSum := sha256.Sum256(elf)
	elfHex := hex.EncodeToString(elfSum[:])

	authPub := onchain.LV1XDevnetLedgerID
	authPK, err := onchain.Key(authPub)
	if err != nil {
		t.Fatal(err)
	}
	progID := onchain.LV1XDevnetProgramID
	ledgerID := onchain.LV1XDevnetLedgerID
	mintID, err := onchain.LV1XMintPDA(progID, ledgerID)
	if err != nil {
		t.Fatal(err)
	}
	btcMintID := onchain.LV1XMainnetBTCMint

	progDataAddr, err := onchain.BPFLoaderProgramDataPDA(progID)
	if err != nil {
		t.Fatal(err)
	}
	progDataPK, _ := onchain.Key(progDataAddr)
	rtAddr, err := onchain.LV1XRuntimePDA(progID, ledgerID)
	if err != nil {
		t.Fatal(err)
	}

	genesisResp := SolanaMainnetGenesis
	trailingByte := byte(0)
	corruptELF := false
	overrideAuth := authPK
	runtimeLedger := ledgerID
	bh := Base58Encode([]byte{7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38})

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &req)
		var res any
		switch req.Method {
		case "getGenesisHash":
			res = genesisResp
		case "getBalance":
			res = map[string]any{"value": 5_000_000_000}
		case "getLatestBlockhash":
			res = map[string]any{"value": map[string]any{"blockhash": bh}}
		case "simulateTransaction":
			res = map[string]any{"value": map[string]any{"err": nil}}
		case "sendTransaction":
			res = "5xMainnetLV1XSig11111111111111111111111111111"
		case "getSignatureStatuses":
			res = map[string]any{"value": []any{map[string]any{"confirmationStatus": "finalized", "err": nil}}}
		case "getMultipleAccounts":
			var addrs []string
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params[0], &addrs)
			}
			progAcct := make([]byte, 36)
			binary.LittleEndian.PutUint32(progAcct[:4], 2)
			copy(progAcct[4:36], progDataPK)

			pdAcct := make([]byte, 45+len(elf)+32)
			binary.LittleEndian.PutUint32(pdAcct[:4], 3)
			binary.LittleEndian.PutUint64(pdAcct[4:12], 320_000_000)
			pdAcct[12] = 1
			copy(pdAcct[13:45], overrideAuth)
			copy(pdAcct[45:45+len(elf)], elf)
			if corruptELF {
				pdAcct[45] ^= 0xff
			}
			pdAcct[len(pdAcct)-1] = trailingByte

			rtAcct := buildTestLV1XRuntimeAccount(t, runtimeLedger, mintID, btcMintID)
			vals := make([]any, 0, len(addrs))
			for _, a := range addrs {
				switch a {
				case progID:
					vals = append(vals, map[string]any{
						"owner":    onchain.BPFLoaderUpgradeableID,
						"lamports": 1_000_000,
						"data":     []string{base64.StdEncoding.EncodeToString(progAcct), "base64"},
					})
				case progDataAddr:
					vals = append(vals, map[string]any{
						"owner":    onchain.BPFLoaderUpgradeableID,
						"lamports": 1_000_000,
						"data":     []string{base64.StdEncoding.EncodeToString(pdAcct), "base64"},
					})
				case rtAddr:
					vals = append(vals, map[string]any{
						"owner":    progID,
						"lamports": 1_000_000,
						"data":     []string{base64.StdEncoding.EncodeToString(rtAcct), "base64"},
					})
				default:
					vals = append(vals, nil)
				}
			}
			res = map[string]any{"value": vals}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	svc, err := NewService(Config{
		DataDir:            t.TempDir(),
		FastKDF:            true,
		RPCTransport:       transport,
		ManifestVerifyKeys: keys,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	w, err := svc.CreateNativeWallet(ctx, "Mainnet Miner", RoleMining, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	mainnetPol := WalletPolicy{
		Role:              RoleMining,
		ApprovalMode:      ApprovalBoundedAuto,
		AutoApproveMaxUSD: 25.0,
		Operations:        AllMiningOperations(),
		Programs:          []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram, progID},
		SOLCap: AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 60_000_000,
			MaxDailyRaw: 200_000_000,
		},
		Mining: &MiningPolicy{
			Cluster:               "mainnet-beta",
			ProgramID:             progID,
			LedgerID:              ledgerID,
			ExpectedELFSHA256:     elfHex,
			MaxSOLPerRoundLamport: 50_000_000,
			MaxDailySOLLamports:   200_000_000,
			AllowedStrategies:     []string{"wide"},
			AutoClaim:             true,
			AllowMainnet:          true,
		},
	}

	// 1. Rejected without manifest.
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied without manifest, got %v", err)
	}

	// 2. Signed pending manifest accepted by custody store, still refuses mainnet activation.
	pendingEnv := buildTestLV1XManifestEnvelope(t, priv, pub, keyID, onchain.LV1XManifestPayload{
		Schema:             onchain.LV1XManifestSchema,
		Sequence:           "1",
		IssuedAt:           now.Unix() - 60,
		ExpiresAt:          now.Unix() + 86400,
		MinFasedVersion:    "0.5.0",
		ProtocolSchemaHash: onchain.LV1XProtocolSchemaHash,
		Clusters: map[string]onchain.LV1XManifestCluster{
			"mainnet-beta": {
				Status:            "pending",
				GenesisHash:       SolanaMainnetGenesis,
				ExpectedELFBytes:  len(elf),
				ExpectedELFSHA256: elfHex,
			},
		},
	})
	if _, err := svc.SetVerifiedMiningManifest(ctx, pendingEnv); err != nil {
		t.Fatalf("SetVerifiedMiningManifest(pending) failed: %v", err)
	}
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied while mainnet manifest is pending, got %v", err)
	}

	// 3. Tampered signature / JSON rejected fail-closed.
	badEnv := append([]byte(nil), pendingEnv...)
	badEnv = bytes.Replace(badEnv, []byte(`"pending"`), []byte(`"active"`), 1)
	if _, err := svc.SetVerifiedMiningManifest(ctx, badEnv); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied on tampered manifest, got %v", err)
	}

	// 4. Signed active manifest seq 2 accepted cleanly.
	activeEnv := buildTestLV1XManifestEnvelope(t, priv, pub, keyID, onchain.LV1XManifestPayload{
		Schema:             onchain.LV1XManifestSchema,
		Sequence:           "2",
		IssuedAt:           now.Unix() - 60,
		ExpiresAt:          now.Unix() + 86400,
		MinFasedVersion:    "0.5.0",
		ProtocolSchemaHash: onchain.LV1XProtocolSchemaHash,
		Clusters: map[string]onchain.LV1XManifestCluster{
			"mainnet-beta": {
				Status:                    "active",
				GenesisHash:               SolanaMainnetGenesis,
				ProgramID:                 progID,
				ProgramDataAddress:        progDataAddr,
				LedgerID:                  ledgerID,
				Mint:                      mintID,
				BTCMint:                   btcMintID,
				ExpectedELFBytes:          len(elf),
				ExpectedELFSHA256:         elfHex,
				AllowedUpgradeAuthorities: []string{authPub},
			},
		},
	})
	vm, err := svc.SetVerifiedMiningManifest(ctx, activeEnv)
	if err != nil || !vm.Compatible {
		t.Fatalf("SetVerifiedMiningManifest(active) failed: vm=%+v err=%v", vm, err)
	}

	// 5. Rejections on live on-chain BPF / Runtime checks.
	genesisResp = SolanaDevnetGenesis
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected wrong genesisHash rejection, got %v", err)
	}
	genesisResp = SolanaMainnetGenesis

	corruptELF = true
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected bad ELF prefix hash rejection, got %v", err)
	}
	corruptELF = false

	trailingByte = 0xAA
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected non-zero trailing BPF byte rejection, got %v", err)
	}
	trailingByte = 0

	badAuth := make([]byte, 32)
	badAuth[0] = 9
	overrideAuth = badAuth
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected unauthorized BPF upgrade authority rejection, got %v", err)
	}
	overrideAuth = authPK

	runtimeLedger = progID
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected runtime ledger mismatch rejection, got %v", err)
	}
	runtimeLedger = ledgerID

	// 6. Owner step-up required.
	if _, err := svc.UpdatePolicy(ctx, w.ID, mainnetPol, false, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected confirmLoosen required for mainnet unlock, got %v", err)
	}

	// 7. Full live check passes -> PrepareReview + ExecuteNativeReview succeed on mainnet-beta.
	w, err = svc.UpdatePolicy(ctx, w.ID, mainnetPol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy mainnet unlock failed: %v", err)
	}
	rev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpCommit,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			Cluster:        "mainnet-beta",
			RoundSlot:      501,
			ShardID:        0,
			Selection:      1,
			AmountLamports: 20_000_000,
			StrategyID:     "wide",
		},
	})
	if err != nil {
		t.Fatalf("PrepareReview mainnet commit failed: %v", err)
	}
	led, err := svc.ExecuteNativeReview(ctx, w.ID, rev.ID, "task:tsk-mainnet-1")
	if err != nil || led.State != LedgerBroadcast {
		t.Fatalf("ExecuteNativeReview mainnet commit failed: state=%s err=%v", led.State, err)
	}
	if rec, rErr := svc.ReconcileLedgerEntry(ctx, w.ID, led.ID); rErr != nil || rec.State != LedgerConfirmed {
		t.Fatalf("ReconcileLedgerEntry mainnet commit failed: state=%s err=%v", rec.State, rErr)
	}
}

func TestCustodyAutoStakeWensatFallbackCrankBatchClaimsAndRPCAccounts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	now := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)

	bh := Base58Encode([]byte{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3})
	fallbackAcct := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
	var confirmState string
	confirmState = "finalized"
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		if r.URL.Host == "primary.devnet.test" && req.Method == "getMultipleAccounts" {
			return nil, errors.New("primary RPC down")
		}
		var res any
		switch req.Method {
		case "getGenesisHash":
			res = SolanaDevnetGenesis
		case "getBalance":
			res = map[string]any{"value": 5_000_000_000}
		case "getLatestBlockhash":
			res = map[string]any{"value": map[string]any{"blockhash": bh}}
		case "simulateTransaction":
			res = map[string]any{"value": map[string]any{"err": nil}}
		case "sendTransaction":
			res = "5xDevnetPhase2Sig1111111111111111111111111111"
		case "getSignatureStatuses":
			if confirmState == "pending" {
				res = map[string]any{"value": []any{nil}}
			} else {
				res = map[string]any{"value": []any{map[string]any{"confirmationStatus": "finalized", "err": nil}}}
			}
		case "getMultipleAccounts":
			res = map[string]any{"value": []any{map[string]any{
				"owner":    onchain.LV1XDevnetProgramID,
				"lamports": 1_234_567,
				"data":     []string{fallbackAcct, "base64"},
			}}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	svc, err := NewService(Config{
		DataDir:      t.TempDir(),
		FastKDF:      true,
		RPCTransport: transport,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	w, err := svc.CreateNativeWallet(ctx, "Devnet SelfKeeper", RoleMining, "https://primary.devnet.test/rpc?api-key=secret-key")
	if err != nil {
		t.Fatal(err)
	}
	w, err = svc.SetWalletRPC(ctx, w.ID, "https://primary.devnet.test/rpc?api-key=secret-key", "https://fallback.devnet.test/rpc?api-key=fb-secret", "owner")
	if err != nil {
		t.Fatalf("SetWalletRPC: %v", err)
	}

	// ReadWalletAccountsRPC fails over cleanly to fallback without exposing URLs.
	acctRes, err := svc.ReadWalletAccountsRPC(ctx, w.ID, []string{onchain.LV1XDevnetLedgerID})
	if err != nil {
		t.Fatalf("ReadWalletAccountsRPC: %v", err)
	}
	if len(acctRes.Accounts) != 1 || !bytes.Equal(acctRes.Accounts[0], []byte{1, 2, 3, 4}) || strings.Contains(acctRes.RPCHost, "secret") {
		t.Fatalf("unexpected ReadWalletAccountsRPC result: %+v", acctRes)
	}

	// Notice SolanaATAProgram intentionally omitted to verify solEscape=true skips ATA creation.
	pol := WalletPolicy{
		Role:              RoleMining,
		ApprovalMode:      ApprovalBoundedAuto,
		AutoApproveMaxUSD: 25.0,
		Operations:        AllMiningOperations(),
		Programs:          []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, onchain.LV1XDevnetProgramID},
		SOLCap: AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 50_000_000,
			MaxDailyRaw: 200_000_000,
		},
		Mining: &MiningPolicy{
			Cluster:               "devnet",
			ProgramID:             onchain.LV1XDevnetProgramID,
			LedgerID:              onchain.LV1XDevnetLedgerID,
			ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
			MaxSOLPerRoundLamport: 30_000_000,
			MaxDailySOLLamports:   200_000_000,
			AutoClaim:             true,
			AutoStakeWensat:       false,
			AllowFallbackCrank:    false,
		},
	}
	w, err = svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	// AutoStakeWensat=false rejects unattended Stake.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpStake,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms},
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for unattended stake when AutoStakeWensat=false, got %v", err)
	}
	// AllowFallbackCrank=false rejects fallback settle.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpFallbackSettle,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{RoundSlot: 90, FallbackStep: "begin"},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied when AllowFallbackCrank=false, got %v", err)
	}

	// Enabling AutoStakeWensat / AllowFallbackCrank requires confirmLoosen=true.
	expanded := w.Policy
	expanded.Mining.AutoStakeWensat = true
	expanded.Mining.AllowFallbackCrank = true
	if _, err := svc.UpdatePolicy(ctx, w.ID, expanded, false, w.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected confirmLoosen=true requirement for AutoStakeWensat/AllowFallbackCrank, got %v", err)
	}
	w, err = svc.UpdatePolicy(ctx, w.ID, expanded, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("enable AutoStakeWensat+AllowFallbackCrank: %v", err)
	}

	// Below 1 WENSAT (1e9) rejected; >= 1 WENSAT unattended Stake allowed; Unstake ALWAYS manual.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpStake,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms - 1},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected <1 WENSAT stake rejection, got %v", err)
	}
	stakeRev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpStake,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms},
	})
	if err != nil {
		t.Fatalf("unattended 1 WENSAT stake failed: %v", err)
	}
	stakeLed, err := svc.ExecuteNativeReview(ctx, w.ID, stakeRev.ID, "mining-engine")
	if err != nil || stakeLed.State != LedgerBroadcast {
		t.Fatalf("ExecuteNativeReview stake failed: state=%s err=%v", stakeLed.State, err)
	}
	if rec, rErr := svc.ReconcileLedgerEntry(ctx, w.ID, stakeLed.ID); rErr != nil || rec.State != LedgerConfirmed {
		t.Fatalf("ReconcileLedgerEntry(stake) failed: state=%s err=%v", rec.State, rErr)
	}
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpUnstake,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms},
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for unattended unstake, got %v", err)
	}

	// ClaimBtcBatch(solEscape=true), ClaimSatBatch, CloseNonce prepare & execute cleanly.
	for _, tc := range []struct {
		op  string
		req *MiningLV1XRequest
	}{
		{MiningOpClaimBtcBatch, &MiningLV1XRequest{RoundSlot: 90, ShardID: 0, Page: 0, EntriesMask: 0x05, SolEscape: true}},
		{MiningOpClaimSatBatch, &MiningLV1XRequest{RoundSlot: 90, ShardID: 0, Page: 0, EntriesMask: 0x03}},
		{MiningOpCloseNonce, &MiningLV1XRequest{RoundSlot: 90, Nonce: 7}},
	} {
		rev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
			WalletID:    w.ID,
			Operation:   tc.op,
			IsBotOrAuto: true,
			MiningLV1X:  tc.req,
		})
		if err != nil {
			t.Fatalf("PrepareReview(%s) failed: %v", tc.op, err)
		}
		led, err := svc.ExecuteNativeReview(ctx, w.ID, rev.ID, "mining-engine")
		if err != nil {
			t.Fatalf("ExecuteNativeReview(%s) failed: %v", tc.op, err)
		}
		if rec, rErr := svc.ReconcileLedgerEntry(ctx, w.ID, led.ID); rErr != nil || rec.State != LedgerConfirmed {
			t.Fatalf("ReconcileLedgerEntry(%s) failed: state=%s err=%v", tc.op, rec.State, rErr)
		}
	}

	// Fallback Crank 0.005 SOL/day hard cap + ReconcileLedgerEntry settlement.
	dayUTC := now.UTC().Format("2006-01-02")
	if _, err := svc.db.ExecContext(ctx,
		`INSERT INTO mining_fallback_gas(wallet_id, day_utc, spent_lamports, reserved_lamports) VALUES (?, ?, ?, 0)`,
		w.ID, dayUTC, MaxDailyFallbackGasLamports-10_000,
	); err != nil {
		t.Fatal(err)
	}
	confirmState = "pending"
	fbRev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpFallbackSettle,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{RoundSlot: 90, FallbackStep: "finish"},
	})
	if err != nil {
		t.Fatalf("PrepareReview final allowed fallback step failed: %v", err)
	}
	fbLed, err := svc.ExecuteNativeReview(ctx, w.ID, fbRev.ID, "mining-engine")
	if err != nil || fbLed.State != LedgerBroadcast {
		t.Fatalf("ExecuteNativeReview fallback settle pending state=%s err=%v", fbLed.State, err)
	}
	confirmState = "finalized"
	reconciled, err := svc.ReconcileLedgerEntry(ctx, w.ID, fbLed.ID)
	if err != nil || reconciled.State != LedgerConfirmed {
		t.Fatalf("ReconcileLedgerEntry fallback settle failed: state=%v err=%v", reconciled.State, err)
	}
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   MiningOpFallbackSettle,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{RoundSlot: 91, FallbackStep: "begin"},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected 0.005 SOL/day fallback gas cap denial, got %v", err)
	}
}
