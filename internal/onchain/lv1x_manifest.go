package onchain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	LV1XManifestDomain       = "wen-lean-v1-deployment-manifest:v1\x00"
	LV1XManifestSchema       = "wen-lean-v1-deployment-manifest/1"
	LV1XClientCompatVersion  = "0.5.0"
	LV1XManifestDefaultURL   = "https://wen.network/.well-known/wen-lean-v1.json"
	LV1XManifestMirrorURL    = "https://app.wen.network/.well-known/wen-lean-v1.json"
	lv1xManifestMaxSkewSec   = int64(300)
	lv1xManifestMaxBytesSize = 128 * 1024
)

// Pinned offline Ed25519 trust keys published in WEN Phase 1.
var lv1xManifestPinnedKeysHex = map[string]string{
	"2a7999659ee95287425ea6beba53b849857ca93e2ccb014ef699bd77bb7b53cb": "356d752909532f1899547ccf7c6b8cca267ab8ae0e777fcc5780d100b647c04f",
}

// LV1XManifestEnvelope is the outer Ed25519-signed wrapper verified against pinned WEN trust keys.
type LV1XManifestEnvelope struct {
	Version   int             `json:"version"`
	KeyID     string          `json:"keyId"`
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"signature"`
}

// LV1XManifestCluster describes one deployed LV1X cluster in a signed deployment manifest.
type LV1XManifestCluster struct {
	Status                    string   `json:"status"` // pending | active | deprecated
	GenesisHash               string   `json:"genesisHash"`
	ProgramID                 string   `json:"programId"`
	ProgramDataAddress        string   `json:"programDataAddress"`
	LedgerID                  string   `json:"ledgerId"`
	Mint                      string   `json:"mint"`
	BTCMint                   string   `json:"btcMint"`
	ExpectedELFBytes          int      `json:"expectedElfBytes"`
	ExpectedELFSHA256         string   `json:"expectedElfSha256"`
	DeploymentSlot            string   `json:"deploymentSlot"`
	AllowedUpgradeAuthorities []string `json:"allowedUpgradeAuthorities"`
	AllowImmutable            bool     `json:"allowImmutable"`
	MinStakeAtoms             string   `json:"minStakeAtoms"`
	LookupTable               string   `json:"lookupTable"`
	ReleaseNotesURL           string   `json:"releaseNotesUrl"`
}

// LV1XManifestPayload captures the signed inner deployment payload.
type LV1XManifestPayload struct {
	Schema             string                         `json:"schema"`
	Sequence           string                         `json:"sequence"`
	IssuedAt           int64                          `json:"issuedAt"`
	ExpiresAt          int64                          `json:"expiresAt"`
	MinFasedVersion    string                         `json:"minFasedVersion"`
	ProtocolSchemaHash string                         `json:"protocolSchemaHash"`
	Clusters           map[string]LV1XManifestCluster `json:"clusters"`
}

// LV1XManifestVerifyOptions parameterizes anti-rollback and client compatibility verification.
type LV1XManifestVerifyOptions struct {
	NowUnix       int64
	MinSequence   uint64
	ClientVersion string
	ExtraKeysHex  map[string]string // test-only override keys; production leaves nil
}

// LV1XVerifiedManifest is the parsed, signature-verified deployment manifest and compatibility verdict.
type LV1XVerifiedManifest struct {
	KeyID              string              `json:"keyId"`
	PayloadSHA256      string              `json:"payloadSha256"`
	SequenceNum        uint64              `json:"sequenceNum,string"`
	VerifiedAt         int64               `json:"verifiedAt"`
	Compatible         bool                `json:"compatible"`
	CompatibilityError string              `json:"compatibilityError,omitempty"`
	Payload            LV1XManifestPayload `json:"payload"`
	RawEnvelope        []byte              `json:"-"`
}

func decodeStrictJSONSingle(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON tokens")
	}
	return nil
}

// EffectiveClientSemver resolves Fased's runtime/compat semver for manifest minFasedVersion gating.
func EffectiveClientSemver(raw string) string {
	clean := strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	if clean == "" || strings.HasPrefix(clean, "0.0.0-") {
		return LV1XClientCompatVersion
	}
	return clean
}

func parseSemverTriple(v string) ([3]int, error) {
	clean := strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if idx := strings.IndexAny(clean, "-+"); idx >= 0 {
		clean = clean[:idx]
	}
	parts := strings.Split(clean, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return [3]int{}, fmt.Errorf("invalid semver %q", v)
	}
	var out [3]int
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return [3]int{}, fmt.Errorf("invalid semver component %q", parts[i])
		}
		out[i] = n
	}
	return out, nil
}

func semverGTE(a, b string) bool {
	va, errA := parseSemverTriple(a)
	vb, errB := parseSemverTriple(b)
	if errA != nil || errB != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if va[i] > vb[i] {
			return true
		}
		if va[i] < vb[i] {
			return false
		}
	}
	return true
}

// VerifyLV1XManifest strictly parses and verifies a signed WEN LV1X deployment manifest.
// Cryptographic or anti-rollback violations return an error; schema/version mismatches set Compatible=false.
func VerifyLV1XManifest(raw []byte, opts LV1XManifestVerifyOptions) (LV1XVerifiedManifest, error) {
	if len(raw) == 0 || len(raw) > lv1xManifestMaxBytesSize {
		return LV1XVerifiedManifest{}, errors.New("manifest size out of bounds")
	}
	var env LV1XManifestEnvelope
	if err := decodeStrictJSONSingle(raw, &env); err != nil {
		return LV1XVerifiedManifest{}, fmt.Errorf("manifest envelope decode failed: %w", err)
	}
	if env.Version != 1 {
		return LV1XVerifiedManifest{}, fmt.Errorf("unsupported manifest envelope version %d", env.Version)
	}
	keyID := strings.ToLower(strings.TrimSpace(env.KeyID))
	pubHex, ok := lv1xManifestPinnedKeysHex[keyID]
	if !ok && len(opts.ExtraKeysHex) > 0 {
		pubHex, ok = opts.ExtraKeysHex[keyID]
	}
	if !ok {
		return LV1XVerifiedManifest{}, fmt.Errorf("untrusted manifest keyId %q", keyID)
	}
	pubBytes, err := hex.DecodeString(strings.TrimSpace(pubHex))
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return LV1XVerifiedManifest{}, errors.New("invalid pinned manifest Ed25519 public key")
	}
	pubSum := sha256.Sum256(pubBytes)
	if hex.EncodeToString(pubSum[:]) != keyID {
		return LV1XVerifiedManifest{}, errors.New("manifest keyId does not match public key digest")
	}
	if len(env.Signature) != ed25519.SignatureSize {
		return LV1XVerifiedManifest{}, errors.New("invalid Ed25519 signature length")
	}
	if len(env.Payload) == 0 {
		return LV1XVerifiedManifest{}, errors.New("empty manifest payload")
	}
	signedBytes := make([]byte, 0, len(LV1XManifestDomain)+len(env.Payload))
	signedBytes = append(signedBytes, LV1XManifestDomain...)
	signedBytes = append(signedBytes, env.Payload...)
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), signedBytes, env.Signature) {
		return LV1XVerifiedManifest{}, errors.New("manifest Ed25519 signature verification failed")
	}

	var payload LV1XManifestPayload
	if err := decodeStrictJSONSingle(env.Payload, &payload); err != nil {
		return LV1XVerifiedManifest{}, fmt.Errorf("manifest payload decode failed: %w", err)
	}
	if strings.TrimSpace(payload.Schema) != LV1XManifestSchema {
		return LV1XVerifiedManifest{}, fmt.Errorf("unexpected manifest schema %q", payload.Schema)
	}
	seqNum, err := strconv.ParseUint(strings.TrimSpace(payload.Sequence), 10, 64)
	if err != nil || seqNum == 0 {
		return LV1XVerifiedManifest{}, fmt.Errorf("invalid manifest sequence %q", payload.Sequence)
	}
	if opts.MinSequence > 0 && seqNum < opts.MinSequence {
		return LV1XVerifiedManifest{}, fmt.Errorf("manifest sequence rollback rejected (%d < %d)", seqNum, opts.MinSequence)
	}
	nowUnix := opts.NowUnix
	if nowUnix <= 0 {
		nowUnix = time.Now().UTC().Unix()
	}
	if payload.IssuedAt <= 0 || payload.ExpiresAt <= payload.IssuedAt {
		return LV1XVerifiedManifest{}, errors.New("invalid manifest timestamps")
	}
	if payload.IssuedAt > nowUnix+lv1xManifestMaxSkewSec {
		return LV1XVerifiedManifest{}, errors.New("manifest issuedAt is in the future")
	}
	if nowUnix > payload.ExpiresAt {
		return LV1XVerifiedManifest{}, errors.New("manifest expired")
	}
	for cname, cl := range payload.Clusters {
		switch cl.Status {
		case "pending", "active", "deprecated":
		default:
			return LV1XVerifiedManifest{}, fmt.Errorf("invalid cluster %s status %q", cname, cl.Status)
		}
		if _, err := Key(cl.GenesisHash); err != nil {
			return LV1XVerifiedManifest{}, fmt.Errorf("invalid cluster %s genesisHash: %w", cname, err)
		}
		if cl.Status == "active" {
			if _, err := Key(cl.ProgramID); err != nil {
				return LV1XVerifiedManifest{}, fmt.Errorf("active cluster %s invalid programId: %w", cname, err)
			}
			if _, err := Key(cl.LedgerID); err != nil {
				return LV1XVerifiedManifest{}, fmt.Errorf("active cluster %s invalid ledgerId: %w", cname, err)
			}
			if _, err := Key(cl.Mint); err != nil {
				return LV1XVerifiedManifest{}, fmt.Errorf("active cluster %s invalid mint: %w", cname, err)
			}
			if _, err := Key(cl.BTCMint); err != nil {
				return LV1XVerifiedManifest{}, fmt.Errorf("active cluster %s invalid btcMint: %w", cname, err)
			}
		}
		if cl.ExpectedELFBytes <= 0 || len(cl.ExpectedELFSHA256) != 64 {
			return LV1XVerifiedManifest{}, fmt.Errorf("cluster %s missing ELF pin", cname)
		}
		if _, err := hex.DecodeString(cl.ExpectedELFSHA256); err != nil {
			return LV1XVerifiedManifest{}, fmt.Errorf("cluster %s invalid ELF sha256", cname)
		}
	}

	payloadSum := sha256.Sum256(env.Payload)
	compat := true
	compatErr := ""
	clientVer := EffectiveClientSemver(opts.ClientVersion)
	if !strings.EqualFold(strings.TrimSpace(payload.ProtocolSchemaHash), LV1XProtocolSchemaHash) {
		compat = false
		compatErr = fmt.Sprintf("Fased Update Required: WEN protocol schema changed (%s)", payload.ProtocolSchemaHash)
	} else if !semverGTE(clientVer, payload.MinFasedVersion) {
		compat = false
		compatErr = fmt.Sprintf("Fased Update Required: WEN manifest requires >= v%s (current v%s)", payload.MinFasedVersion, clientVer)
	}

	cpRaw := append([]byte(nil), raw...)
	return LV1XVerifiedManifest{
		KeyID:              keyID,
		PayloadSHA256:      hex.EncodeToString(payloadSum[:]),
		SequenceNum:        seqNum,
		VerifiedAt:         nowUnix,
		Compatible:         compat,
		CompatibilityError: compatErr,
		Payload:            payload,
		RawEnvelope:        cpRaw,
	}, nil
}
