package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHubSignedVerificationAndFasedScriptAST(t *testing.T) {
	t.Parallel()
	now := time.Unix(1795600000, 0)
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	list, err := svc.List(context.Background())
	if err != nil || !list.WENHeld || list.SATStatus != StatusDeferredWEN || len(list.Packages) < 2 {
		t.Fatalf("unexpected fixture list: %+v err=%v", list, err)
	}

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Tampered signature fails closed.
	pkg := Package{
		ID:      "hub-custom-trend",
		Name:    "Custom Trend Script",
		Kind:    KindFasedScript,
		Version: "1.0.0",
		Payload: `const m = ema(close, 9);
signal("long", close > m);`,
		SATBond: SATBondMetadata{AmountSAT: 500},
	}
	if err := SignPackage(priv, &pkg); err != nil {
		t.Fatalf("SignPackage failed: %v", err)
	}
	if _, err := svc.Publish(context.Background(), PublishRequest{
		ID:              pkg.ID,
		Name:            pkg.Name,
		Kind:            pkg.Kind,
		Version:         pkg.Version,
		AuthorPubKeyHex: pkg.AuthorPubKeyHex,
		Payload:         pkg.Payload + "\n// tampered",
		SignatureHex:    pkg.SignatureHex,
		AmountSAT:       500,
	}); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid on tampered payload, got %v", err)
	}

	// Oversized payload > 256 KiB rejected.
	if _, err := svc.Publish(context.Background(), PublishRequest{
		ID:              "hub-oversized-pack",
		Name:            "Too Big",
		Kind:            KindWatchlistPack,
		Version:         "1.0.0",
		AuthorPubKeyHex: pkg.AuthorPubKeyHex,
		Payload:         strings.Repeat("a", MaxPayloadBytes+1),
		SignatureHex:    pkg.SignatureHex,
	}); !errors.Is(err, ErrPackageTooLarge) {
		t.Fatalf("expected ErrPackageTooLarge, got %v", err)
	}

	// Invalid FasedScript fails AST compilation even when validly signed.
	badScript := Package{
		ID:      "hub-bad-fasedscript",
		Name:    "Broken Script",
		Kind:    KindFasedScript,
		Version: "1.0.0",
		Payload: `const = broken(;;`,
	}
	if err := SignPackage(priv, &badScript); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(context.Background(), PublishRequest{
		ID:              badScript.ID,
		Name:            badScript.Name,
		Kind:            badScript.Kind,
		Version:         badScript.Version,
		AuthorPubKeyHex: badScript.AuthorPubKeyHex,
		Payload:         badScript.Payload,
		SignatureHex:    badScript.SignatureHex,
	}); err == nil {
		t.Fatal("expected invalid fasedscript AST to be rejected")
	}

	// Valid signed fasedscript publishes and installs cleanly with deferred WEN hold metadata.
	pubPkg, err := svc.Publish(context.Background(), PublishRequest{
		ID:              pkg.ID,
		Name:            pkg.Name,
		Kind:            pkg.Kind,
		Version:         pkg.Version,
		AuthorPubKeyHex: pkg.AuthorPubKeyHex,
		Payload:         pkg.Payload,
		SignatureHex:    pkg.SignatureHex,
		AmountSAT:       500,
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if !pubPkg.Verified || pubPkg.SATBond.Status != StatusDeferredWEN || pubPkg.AuthorPubKeyHex == "" || hex.EncodeToString([]byte{}) != "" {
		t.Fatalf("unexpected published package: %+v", pubPkg)
	}

	inst, err := svc.Install(context.Background(), pkg.ID)
	if err != nil || !inst.Installed || !inst.Verified {
		t.Fatalf("Install failed: %+v err=%v", inst, err)
	}
}
