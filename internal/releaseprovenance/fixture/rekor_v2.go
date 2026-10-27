package fixture

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/secure-systems-lab/go-securesystemslib/dsse"
	common "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	rekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	rekortiles "github.com/sigstore/rekor-tiles/v2/pkg/generated/protobuf"
	rekornote "github.com/sigstore/rekor-tiles/v2/pkg/note"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore/pkg/signature"
	logformat "github.com/transparency-dev/formats/log"
	"github.com/transparency-dev/merkle/rfc6962"
	"golang.org/x/mod/sumdb/note"
	"google.golang.org/protobuf/encoding/protojson"
)

// rekorV2 makes a real one-leaf RFC6962 tree and signed Rekor-tiles checkpoint.
// Its leaf commits to the DSSE PAE hash, signature and modern leaf certificate.
// No SET or trustworthy integrated-time field exists in this format.
func rekorV2(cert *x509.Certificate, payload, sig []byte, now time.Time) (*rekor.TransparencyLogEntry, map[string]*root.TransparencyLog, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	id := sha256.Sum256(public)
	digest := sha256.Sum256(dsse.PAE("application/vnd.in-toto+json", payload))
	entry := &rekortiles.Entry{ApiVersion: "0.0.2", Kind: "hashedrekord", Spec: &rekortiles.Spec{Spec: &rekortiles.Spec_HashedRekordV002{HashedRekordV002: &rekortiles.HashedRekordLogEntryV002{Data: &common.HashOutput{Algorithm: common.HashAlgorithm_SHA2_256, Digest: digest[:]}, Signature: &rekortiles.Signature{Content: sig, Verifier: &rekortiles.Verifier{KeyDetails: common.PublicKeyDetails_PKIX_ECDSA_P256_SHA_256, Verifier: &rekortiles.Verifier_X509Certificate{X509Certificate: &common.X509Certificate{RawBytes: cert.Raw}}}}}}}}
	serialized, err := protojson.Marshal(entry)
	if err != nil {
		return nil, nil, err
	}
	body, err := jsoncanonicalizer.Transform(serialized)
	if err != nil {
		return nil, nil, err
	}
	leafHash := rfc6962.DefaultHasher.HashLeaf(body)
	signer, err := signature.LoadECDSASignerVerifier(key, crypto.SHA256)
	if err != nil {
		return nil, nil, err
	}
	const origin = "rekor-v2.fixture.invalid"
	noteSigner, err := rekornote.NewNoteSigner(context.Background(), origin, signer)
	if err != nil {
		return nil, nil, err
	}
	checkpoint := logformat.Checkpoint{Origin: origin, Size: 1, Hash: leafHash}.Marshal()
	signed, err := note.Sign(&note.Note{Text: string(checkpoint)}, noteSigner)
	if err != nil {
		return nil, nil, err
	}
	log := &root.TransparencyLog{BaseURL: "https://" + origin, ID: id[:], ValidityPeriodStart: now.Add(-time.Hour), ValidityPeriodEnd: now.Add(time.Hour), HashFunc: crypto.SHA256, SignatureHashFunc: crypto.SHA256, PublicKey: &key.PublicKey}
	proof := &rekor.InclusionProof{LogIndex: 0, TreeSize: 1, RootHash: leafHash, Checkpoint: &rekor.Checkpoint{Envelope: string(signed)}}
	return &rekor.TransparencyLogEntry{LogIndex: 0, LogId: &common.LogId{KeyId: id[:]}, KindVersion: &rekor.KindVersion{Kind: "hashedrekord", Version: "0.0.2"}, CanonicalizedBody: body, InclusionProof: proof}, map[string]*root.TransparencyLog{hex.EncodeToString(id[:]): log}, nil
}
