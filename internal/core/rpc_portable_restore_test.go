package core

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestPortableRPCCredentialCrossInstanceAndPostRecoveryRestore(t *testing.T) {
	t.Parallel()
	const backupPassword = "Portable RPC cross-instance passphrase 123"
	const targetPassword = "Target instance RPC vault passphrase 456"
	const devnetHelius = "https://devnet.helius-rpc.com"

	// Instance A uses an OS-protected RPC vault on a Devnet watch wallet.
	dirA := fixtureDir(t)
	sA, err := openStoreWithTaskSecrets(dirA, &testTaskSecrets{key: bytes.Repeat([]byte{41}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	claimFixture(t, sA)
	sA.rpcAdmission = true

	addrBytes := bytes.Repeat([]byte{0x22}, 32)
	devnetAddr := onchain.Base58(addrBytes)
	vA, err := sA.SetupAccount(t.Context(), AccountSetupCommand{
		Revision: 0,
		Action:   "watch",
		Name:     "Devnet Treasury",
		Network:  "devnet",
		Address:  devnetAddr,
	})
	if err != nil || len(vA.Record.Accounts) != 1 {
		_ = sA.Close()
		t.Fatalf("SetupAccount watch failed: %+v err=%v", vA, err)
	}
	acctA := vA.Record.Accounts[0]
	vA, err = sA.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: acctA.ID,
		Revision:  vA.Record.Revision,
		Endpoint:  devnetHelius,
		Network:   "devnet",
	})
	if err != nil {
		_ = sA.Close()
		t.Fatalf("ConfigureAccountRPC failed: %v", err)
	}
	savedA, err := sA.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID: acctA.ID,
		Revision:  vA.Record.Revision,
		Action:    "save",
		Key:       disposableProviderKey,
	})
	if err != nil || !savedA.Saved || !savedA.OSProtected {
		_ = sA.Close()
		t.Fatalf("save OS-protected RPC key failed: %+v err=%v", savedA, err)
	}

	// Export both a legacy v2 backup (instance-bound) and a portable v3 backup.
	legacyA, err := sA.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  acctA.ID,
		Revision:   vA.Record.Revision,
		Ref:        savedA.Ref,
		Action:     "backup",
		Passphrase: backupPassword,
	})
	if err != nil || legacyA.Backup == nil || legacyA.Backup.Version != 2 {
		_ = sA.Close()
		t.Fatalf("legacy RPC backup failed: %+v err=%v", legacyA, err)
	}
	portableA, err := sA.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  acctA.ID,
		Revision:   vA.Record.Revision,
		Ref:        savedA.Ref,
		Action:     "backup",
		Portable:   true,
		Passphrase: backupPassword,
	})
	if err != nil || !portableA.BackupVerified || portableA.Backup == nil {
		_ = sA.Close()
		t.Fatalf("portable RPC backup failed: %+v err=%v", portableA, err)
	}
	b := portableA.Backup
	if b.Version != 3 || !b.Portable || b.InstanceID != "" || b.AccountID != "" || b.KeyVersion != 2 || b.Network != "devnet" || b.Endpoint != devnetHelius || b.Vault.OSProtected {
		_ = sA.Close()
		t.Fatalf("unexpected portable backup header: %+v", b)
	}
	rawPortable, _ := json.Marshal(b)
	for _, secret := range []string{disposableProviderKey, backupPassword, sA.instanceID, acctA.ID} {
		if bytes.Contains(rawPortable, []byte(secret)) {
			_ = sA.Close()
			t.Fatalf("portable backup leaked %q", secret)
		}
	}

	// WriteRPCBackup persists with 0600 and refuses to clobber.
	exportPath := filepath.Join(t.TempDir(), "exports", "portable-rpc.json")
	if err := WriteRPCBackup(exportPath, b); err != nil {
		_ = sA.Close()
		t.Fatalf("WriteRPCBackup portable failed: %v", err)
	}
	if err := WriteRPCBackup(exportPath, b); err == nil {
		_ = sA.Close()
		t.Fatal("WriteRPCBackup overwrote existing file")
	}
	exportedBytes, err := ReadPrivateFile(exportPath)
	if err != nil {
		_ = sA.Close()
		t.Fatal(err)
	}
	var diskBackup RPCCredentialBackup
	if err := json.Unmarshal(exportedBytes, &diskBackup); err != nil {
		_ = sA.Close()
		t.Fatal(err)
	}

	// Export public wallet + RPC configuration bundle (zero secrets).
	bundle, err := sA.ExportPortableWallets(t.Context())
	if err != nil || bundle.Version != 1 || len(bundle.Wallets) != 1 || bundle.Wallets[0].Address != devnetAddr || bundle.Wallets[0].RPCEndpoint != devnetHelius {
		_ = sA.Close()
		t.Fatalf("ExportPortableWallets failed: %+v err=%v", bundle, err)
	}
	bundleBytes, _ := json.Marshal(bundle)
	for _, secret := range []string{disposableProviderKey, backupPassword, sA.instanceID, acctA.ID} {
		if bytes.Contains(bundleBytes, []byte(secret)) {
			_ = sA.Close()
			t.Fatalf("portable wallet bundle leaked %q", secret)
		}
	}
	_ = sA.Close()

	// Instance B: brand-new install with distinct instanceID and no shared OS key.
	dirB := fixtureDir(t)
	sB, err := OpenStore(dirB)
	if err != nil {
		t.Fatal(err)
	}
	claimFixture(t, sB)
	sB.rpcAdmission = true
	if sB.instanceID == sA.instanceID {
		_ = sB.Close()
		t.Fatal("expected distinct instance IDs")
	}

	imported, err := sB.ImportPortableWallets(t.Context(), PortableWalletsImportCommand{
		Revision: 0,
		Bundle:   bundle,
	})
	if err != nil || imported.Imported != 1 || imported.Skipped != 0 || len(imported.Record.Accounts) != 1 {
		_ = sB.Close()
		t.Fatalf("ImportPortableWallets failed: %+v err=%v", imported, err)
	}
	acctB := imported.Record.Accounts[0]
	if acctB.ID == acctA.ID || acctB.RPC == nil || acctB.RPC.Endpoint != devnetHelius || acctB.RPC.Network != "devnet" {
		_ = sB.Close()
		t.Fatalf("unexpected imported account: %+v", acctB)
	}

	// Re-importing the same bundle is idempotent and skips duplicates.
	dedup, err := sB.ImportPortableWallets(t.Context(), PortableWalletsImportCommand{
		Revision: imported.Record.Revision,
		Bundle:   bundle,
	})
	if err != nil || dedup.Imported != 0 || dedup.Skipped != 1 || dedup.Record.Revision != imported.Record.Revision {
		_ = sB.Close()
		t.Fatalf("duplicate portable wallet import failed: %+v err=%v", dedup, err)
	}

	// Instance-bound v2 backup from Instance A must remain rejected on Instance B.
	if _, err := sB.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  acctB.ID,
		Revision:   imported.Record.Revision,
		Action:     "verify-backup",
		Passphrase: backupPassword,
		Backup:     legacyA.Backup,
	}); err == nil {
		_ = sB.Close()
		t.Fatal("legacy instance-bound backup verified across instances")
	}

	// Tampered/wrong portable backup inputs must be refused.
	for _, tc := range []string{"password", "network", "endpoint", "keyVersion", "binding", "ciphertext", "instanceLeak"} {
		bad := diskBackup
		bad.Vault.Ciphertext = append([]byte(nil), bad.Vault.Ciphertext...)
		pass := backupPassword
		switch tc {
		case "password":
			pass = "Wrong backup passphrase 999"
		case "network":
			bad.Network = "mainnet-beta"
		case "endpoint":
			bad.Endpoint = "https://solana-devnet.g.alchemy.com"
		case "keyVersion":
			bad.KeyVersion = 1
		case "binding":
			bad.Vault.Binding = strings.Repeat("0", 64)
		case "ciphertext":
			bad.Vault.Ciphertext[0] ^= 1
		case "instanceLeak":
			bad.InstanceID = sB.instanceID
		}
		if _, err := sB.RPCCredential(t.Context(), RPCCredentialCommand{
			AccountID:  acctB.ID,
			Revision:   imported.Record.Revision,
			Action:     "restore",
			Passphrase: pass,
			Backup:     &bad,
		}); err == nil {
			_ = sB.Close()
			t.Fatalf("tampered portable restore %q succeeded", tc)
		}
	}

	// Verify and restore portable v3 backup on Instance B.
	verifiedB, err := sB.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  acctB.ID,
		Revision:   imported.Record.Revision,
		Action:     "verify-backup",
		Passphrase: backupPassword,
		Backup:     &diskBackup,
	})
	if err != nil || !verifiedB.BackupVerified {
		_ = sB.Close()
		t.Fatalf("verify-backup on Instance B failed: %+v err=%v", verifiedB, err)
	}
	restoredB, err := sB.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  acctB.ID,
		Revision:   imported.Record.Revision,
		Action:     "restore",
		Passphrase: backupPassword,
		Backup:     &diskBackup,
	})
	if err != nil || !restoredB.Saved || !restoredB.Locked || restoredB.OSProtected {
		_ = sB.Close()
		t.Fatalf("restore on Instance B failed: %+v err=%v", restoredB, err)
	}

	// Locked restart proof on Instance B: close store, reopen cold, check locked status,
	// and execute authenticated Devnet ReadWallet without signer authority.
	_ = sB.Close()
	reopenedB, err := OpenStore(dirB)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedB.Close()

	reopenedStatus, err := reopenedB.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID: acctB.ID,
		Revision:  imported.Record.Revision,
		Action:    "status",
	})
	if err != nil || !reopenedStatus.Saved || !reopenedStatus.Locked || reopenedStatus.Stale || reopenedStatus.Ref != restoredB.Ref {
		t.Fatalf("reopened Instance B status mismatch: %+v err=%v", reopenedStatus, err)
	}
	reopenedB.rpcAdmission = true
	server := loopbackSolanaRPCServer(t, "devnet", "devnet.helius-rpc.com", disposableProviderKey, devnetAddr)
	rt := loopbackRPCTLSTransport(t, server, "devnet.helius-rpc.com")
	reopenedB.walletReadFixture = func(ctx context.Context, a Account, addr string, key []byte, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
		transport := &providerRPCTransport{origin: a.RPC.Endpoint, provider: "helius", key: key, valid: valid, next: rt}
		return walletRPC(ctx, a.RPC.Endpoint, transport, valid, false, addr, a.Network, selection)
	}
	snap, err := reopenedB.ReadWallet(t.Context(), WalletReadCommand{
		AccountID:  acctB.ID,
		Revision:   imported.Record.Revision,
		Ref:        reopenedStatus.Ref,
		Passphrase: backupPassword,
	})
	if err != nil || !snap.LiveRPC || snap.FixtureOnly || snap.Lamports != "9007199254740993" || len(snap.Activity) != 1 {
		t.Fatalf("post-restart Devnet ReadWallet failed: %+v err=%v", snap, err)
	}

	// Post-recovery signer Epoch rotation on a Devnet managed wallet:
	// Epoch 1 portable v3 RPC backup restores cleanly onto Epoch 2 managed wallet,
	// while legacy v2 backup bound to Epoch 1 is refused.
	dirM := fixtureDir(t)
	sM, err := OpenStore(dirM)
	if err != nil {
		t.Fatal(err)
	}
	defer sM.Close()
	claimFixture(t, sM)
	sM.rpcAdmission = true
	epoch1 := testRef()
	epoch1.Version = 2
	epoch1.Network = "devnet"
	epoch1.Epoch = strings.Repeat("1", 32)
	epoch2 := epoch1
	epoch2.Epoch = strings.Repeat("2", 32)
	activeRef := epoch1
	sM.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		return signer.Response{Status: signer.Status{
			FixtureOnly: true,
			Locked:      true,
			Account:     &activeRef,
		}}, nil
	}
	recM := defaultAccounts()
	recM.Accounts = []Account{
		{
			Version:   3,
			ID:        strings.Repeat("d", 32),
			Name:      "Managed Devnet Epoch1",
			Mode:      "fixture-managed",
			Network:   "devnet",
			PublicKey: epoch1.PublicKey,
			Signer:    &epoch1,
		},
	}
	vM, err := sM.UpdateAccounts(t.Context(), recM)
	if err != nil {
		t.Fatal(err)
	}
	vM, err = sM.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: strings.Repeat("d", 32),
		Revision:  vM.Record.Revision,
		Endpoint:  devnetHelius,
		Network:   "devnet",
	})
	if err != nil {
		t.Fatal(err)
	}
	savedM1, err := sM.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  strings.Repeat("d", 32),
		Revision:   vM.Record.Revision,
		Action:     "save",
		Key:        disposableProviderKey,
		Passphrase: targetPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	epoch1Legacy, err := sM.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  strings.Repeat("d", 32),
		Revision:   vM.Record.Revision,
		Ref:        savedM1.Ref,
		Action:     "backup",
		Passphrase: targetPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	epoch1Portable, err := sM.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  strings.Repeat("d", 32),
		Revision:   vM.Record.Revision,
		Ref:        savedM1.Ref,
		Action:     "backup",
		Portable:   true,
		Passphrase: targetPassword,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate wallet recovery creating Epoch 2 and attaching Epoch 2 account record.
	activeRef = epoch2
	vM, err = sM.SetupAccount(t.Context(), AccountSetupCommand{
		Revision:  vM.Record.Revision,
		Action:    "attach",
		Name:      "Managed Devnet Epoch2",
		Network:   "devnet",
		Reference: &epoch2,
	})
	if err != nil || len(vM.Record.Accounts) != 2 {
		t.Fatalf("attach Epoch 2 failed: %+v err=%v", vM, err)
	}
	epoch2Acct := vM.Record.Accounts[1]
	vM, err = sM.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: epoch2Acct.ID,
		Revision:  vM.Record.Revision,
		Endpoint:  devnetHelius,
		Network:   "devnet",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sM.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  epoch2Acct.ID,
		Revision:   vM.Record.Revision,
		Action:     "restore",
		Passphrase: targetPassword,
		Backup:     epoch1Legacy.Backup,
	}); err == nil {
		t.Fatal("legacy Epoch 1 RPC backup restored onto Epoch 2 wallet")
	}
	restoredM2, err := sM.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  epoch2Acct.ID,
		Revision:   vM.Record.Revision,
		Action:     "restore",
		Passphrase: targetPassword,
		Backup:     epoch1Portable.Backup,
	})
	if err != nil || !restoredM2.Saved || restoredM2.Stale {
		t.Fatalf("portable Epoch 1 RPC backup failed to restore on Epoch 2: %+v err=%v", restoredM2, err)
	}
}

func TestPortableVenueCredentialBackupAndCrossInstanceRestore(t *testing.T) {
	t.Parallel()
	sA := fixtureStore(t)
	claimFixture(t, sA)
	hA, tlsA, cA := setupCredentialSwapHarness(t, sA, true)

	// Export portable Jupiter backup from Instance A.
	backed, err := hA.store.VenueCredential(t.Context(), VenueCredentialCommand{
		AccountID:  cA.AccountID,
		Revision:   cA.Revision,
		Ref:        cA.Ref,
		Action:     "backup",
		Passphrase: venuePassword,
	})
	if err != nil || !backed.BackupVerified || backed.Backup == nil || !backed.Backup.Portable || backed.Backup.Purpose != "jupiter" || backed.Backup.Network != "mainnet-beta" {
		t.Fatalf("portable venue backup failed: %+v err=%v", backed, err)
	}
	raw, _ := json.Marshal(backed.Backup)
	for _, secret := range []string{tlsA.JupiterKey, tlsA.RPCKey, venuePassword, sA.instanceID, cA.AccountID} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("venue backup leaked %q", secret)
		}
	}

	backupPath := filepath.Join(t.TempDir(), "exports", "jupiter-backup.json")
	if err := WriteVenueBackup(backupPath, backed.Backup); err != nil {
		t.Fatalf("WriteVenueBackup failed: %v", err)
	}
	if err := WriteVenueBackup(backupPath, backed.Backup); err == nil {
		t.Fatal("WriteVenueBackup overwrote existing file")
	}
	onDisk, err := ReadPrivateFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	var diskBackup VenueCredentialBackup
	if err := json.Unmarshal(onDisk, &diskBackup); err != nil {
		t.Fatal(err)
	}

	// Removing/rotating RPC key makes venue stale, yet backup of retained Jupiter key still succeeds.
	rpcStatusA, err := hA.store.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID: cA.AccountID,
		Revision:  cA.Revision,
		Action:    "status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hA.store.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID:  cA.AccountID,
		Revision:   cA.Revision,
		Ref:        rpcStatusA.Ref,
		Action:     "save",
		Key:        tlsA.RPCKey + "-rotated",
		Passphrase: venuePassword,
	}); err != nil {
		t.Fatal(err)
	}
	staleBackup, err := hA.store.VenueCredential(t.Context(), VenueCredentialCommand{
		AccountID:  cA.AccountID,
		Revision:   cA.Revision,
		Ref:        cA.Ref,
		Action:     "backup",
		Passphrase: venuePassword,
	})
	if err != nil || !staleBackup.BackupVerified || !staleBackup.Stale {
		t.Fatalf("backup failed when venue became stale after RPC rotation: %+v err=%v", staleBackup, err)
	}

	// Instance B: distinct OS-protected RPC vault, no Jupiter key saved yet.
	dirB := fixtureDir(t)
	sB, err := openStoreWithTaskSecrets(dirB, &testTaskSecrets{key: bytes.Repeat([]byte{77}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	claimFixture(t, sB)
	hB, tlsB, cB := setupCredentialSwapHarness(t, sB, false)
	if _, err := hB.store.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID: cB.AccountID,
		Revision:  cB.Revision,
		Action:    "save",
		Key:       tlsB.RPCKey,
	}); err != nil {
		t.Fatalf("save OS-protected RPC vault on Instance B failed: %v", err)
	}

	// Negative matrix on Instance B.
	for _, tc := range []string{"password", "network", "binding", "ciphertext", "portableFalse"} {
		bad := diskBackup
		bad.Ciphertext = append([]byte(nil), bad.Ciphertext...)
		pass := venuePassword
		switch tc {
		case "password":
			pass = "Wrong venue backup password 999"
		case "network":
			bad.Network = "devnet"
		case "binding":
			bad.Binding = strings.Repeat("0", 64)
		case "ciphertext":
			bad.Ciphertext[0] ^= 1
		case "portableFalse":
			bad.Portable = false
		}
		if _, err := hB.store.VenueCredential(t.Context(), VenueCredentialCommand{
			AccountID:  cB.AccountID,
			Revision:   cB.Revision,
			Action:     "restore",
			Passphrase: pass,
			Backup:     &bad,
		}); err == nil {
			t.Fatalf("invalid venue backup %q accepted", tc)
		}
	}

	verifiedB, err := hB.store.VenueCredential(t.Context(), VenueCredentialCommand{
		AccountID:  cB.AccountID,
		Revision:   cB.Revision,
		Action:     "verify-backup",
		Passphrase: venuePassword,
		Backup:     &diskBackup,
	})
	if err != nil || !verifiedB.BackupVerified || verifiedB.Saved {
		t.Fatalf("verify-backup on Instance B failed: %+v err=%v", verifiedB, err)
	}

	restoredB, err := hB.store.VenueCredential(t.Context(), VenueCredentialCommand{
		AccountID:  cB.AccountID,
		Revision:   cB.Revision,
		Action:     "restore",
		Passphrase: venuePassword,
		Backup:     &diskBackup,
	})
	if err != nil || !restoredB.Saved || !restoredB.Locked || !restoredB.RPCReady {
		t.Fatalf("venue restore on Instance B failed: %+v err=%v", restoredB, err)
	}

	// Refuse overwriting existing Jupiter key unless explicitly removed first.
	if _, err := hB.store.VenueCredential(t.Context(), VenueCredentialCommand{
		AccountID:  cB.AccountID,
		Revision:   cB.Revision,
		Ref:        restoredB.Ref,
		Action:     "restore",
		Passphrase: venuePassword,
		Backup:     &diskBackup,
	}); err == nil {
		t.Fatal("venue restore overwrote existing key")
	}

	// Locked restart proof on Instance B.
	_ = hB.store.Close()
	reopenedB, err := openStoreWithTaskSecrets(dirB, &testTaskSecrets{key: bytes.Repeat([]byte{77}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedB.Close()
	reopenedB.rpcAdmission = true
	reopenedB.custodyNetwork = "mainnet-beta"
	reopenedB.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: hB.vault.Status()}, nil
	}
	hB2 := &swapHarness{store: reopenedB, vault: hB.vault, chain: tlsB.Chain, password: hB.password, profile: hB.profile}
	hB2.connect()
	if err := reopenedB.configureCredentialVenue(func(a onchain.CredentialAccess) (swapVenue, error) {
		return onchain.NewControlledAuthenticatedClient(tlsB.Transport, a)
	}); err != nil {
		t.Fatal(err)
	}

	unlocked, err := reopenedB.VenueCredential(t.Context(), VenueCredentialCommand{
		AccountID:  cB.AccountID,
		Revision:   cB.Revision,
		Ref:        restoredB.Ref,
		Action:     "unlock",
		Passphrase: venuePassword,
		Seconds:    60,
	})
	if err != nil || unlocked.Locked || unlocked.UnlockUntil == 0 {
		t.Fatalf("unlock after locked restart failed: %+v err=%v", unlocked, err)
	}
	recB, _ := reopenedB.accountsRecord(t.Context())
	acctB, _ := swapAccount(recB, cB.AccountID)
	access, err := reopenedB.venueAccess(acctB)
	if err != nil {
		t.Fatal(err)
	}
	borrowedJupiter, err := access.Borrow(t.Context(), "jupiter")
	if err != nil || string(borrowedJupiter) != tlsA.JupiterKey {
		t.Fatalf("borrowed Jupiter key mismatch after restore: got=%q err=%v", string(borrowedJupiter), err)
	}
	clear(borrowedJupiter)
}

func TestPortableWalletsBundleValidationAndSecurity(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	claimFixture(t, s)
	s.rpcAdmission = false

	devnetAddr := onchain.Base58(bytes.Repeat([]byte{0x11}, 32))
	validBundle := PortableWalletsBundle{
		Version:    1,
		ExportedAt: time.Now().Unix(),
		Wallets: []PortableWalletEntry{
			{
				Name:        "Devnet Watch",
				Network:     "devnet",
				Address:     devnetAddr,
				RPCEndpoint: "https://devnet.helius-rpc.com",
			},
		},
	}
	if _, err := s.ImportPortableWallets(t.Context(), PortableWalletsImportCommand{Revision: 0, Bundle: validBundle}); err == nil {
		t.Fatal("ImportPortableWallets admitted when rpcAdmission disabled")
	}
	s.rpcAdmission = true

	// Test fixture accounts are omitted by ExportPortableWallets.
	r := defaultAccounts()
	r.Accounts = []Account{publicRecord()}
	if _, err := s.UpdateAccounts(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	exp, err := s.ExportPortableWallets(t.Context())
	if err != nil || len(exp.Wallets) != 0 {
		t.Fatalf("expected 0 exported wallets for disposable fixture, got %+v err=%v", exp, err)
	}

	// Reject bad revisions, bad version, bad address, and custom untrusted HTTP endpoints.
	if _, err := s.ImportPortableWallets(t.Context(), PortableWalletsImportCommand{Revision: 0, Bundle: validBundle}); err == nil {
		t.Fatal("stale revision accepted")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*PortableWalletsBundle)
	}{
		{"version", func(b *PortableWalletsBundle) { b.Version = 2 }},
		{"empty", func(b *PortableWalletsBundle) { b.Wallets = nil }},
		{"address", func(b *PortableWalletsBundle) { b.Wallets[0].Address = "not-a-solana-address" }},
		{"endpoint", func(b *PortableWalletsBundle) { b.Wallets[0].RPCEndpoint = "http://127.0.0.1:8899" }},
		{"crossNetworkEndpoint", func(b *PortableWalletsBundle) { b.Wallets[0].RPCEndpoint = "https://mainnet.helius-rpc.com" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := PortableWalletsBundle{
				Version: validBundle.Version,
				Wallets: append([]PortableWalletEntry(nil), validBundle.Wallets...),
			}
			tc.mutate(&bad)
			if _, err := s.ImportPortableWallets(t.Context(), PortableWalletsImportCommand{Revision: 1, Bundle: bad}); err == nil {
				t.Fatalf("invalid portable bundle %q accepted", tc.name)
			}
		})
	}
}
