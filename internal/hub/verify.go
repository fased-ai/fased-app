package hub

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/fased-ai/fased-app/internal/fasedscript"
)

// CanonicalManifestBytes builds the canonical Ed25519 message covered by SignatureHex.
func CanonicalManifestBytes(pkg Package) []byte {
	payloadHash := pkg.PayloadSHA256
	if payloadHash == "" {
		payloadHash = HashPayload(pkg.Payload)
	}
	return []byte(fmt.Sprintf(
		"fased-hub-v1|%s|%s|%s|%s|%s",
		pkg.ID,
		pkg.Kind,
		pkg.Version,
		pkg.Name,
		payloadHash,
	))
}

// SignPackage computes PayloadSHA256 and attaches an Ed25519 SignatureHex & AuthorPubKeyHex.
func SignPackage(priv ed25519.PrivateKey, pkg *Package) error {
	if pkg == nil || len(priv) != ed25519.PrivateKeySize {
		return errors.New("valid Ed25519 private key and package required")
	}
	pub := priv.Public().(ed25519.PublicKey)
	pkg.AuthorPubKeyHex = hex.EncodeToString(pub)
	if err := ValidatePackageFields(pkg); err != nil {
		return err
	}
	msg := CanonicalManifestBytes(*pkg)
	sig := ed25519.Sign(priv, msg)
	pkg.SignatureHex = hex.EncodeToString(sig)
	return nil
}

// VerifyPackage validates manifest bounds, Ed25519 signature, and for fasedscript
// packages validates AST/sandbox safety via fasedscript.Compile.
func VerifyPackage(pkg *Package) error {
	if err := ValidatePackageFields(pkg); err != nil {
		return err
	}
	pub, err := hex.DecodeString(pkg.AuthorPubKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrSignatureInvalid
	}
	sig, err := hex.DecodeString(pkg.SignatureHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrSignatureInvalid
	}
	if !ed25519.Verify(pub, CanonicalManifestBytes(*pkg), sig) {
		return ErrSignatureInvalid
	}
	if pkg.Kind == KindFasedScript {
		if _, err := fasedscript.Compile(pkg.Payload); err != nil {
			return fmt.Errorf("fasedscript AST verification failed: %w", err)
		}
	}
	pkg.Verified = true
	return nil
}
