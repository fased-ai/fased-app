// Package update verifies development update plans. It cannot install, migrate,
// unlock, execute artifacts, or authorize financial operation. Acquisition is
// explicit development-only HTTPS with independently pinned disposable trust.
package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const Purpose = "fased-app-update-development:v1"
const SignatureDomain = Purpose + "\x00"
const MaxDocument = 64 << 10
const MaxArtifact = 256 << 20

type Envelope struct {
	Version   int             `json:"version"`
	KeyID     string          `json:"keyId"`
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"signature"`
}

type TrustKey struct {
	ID        string `json:"id"`
	PublicKey []byte `json:"publicKey"`
	NotBefore int64  `json:"notBefore"`
	NotAfter  int64  `json:"notAfter"`
}

type Trust struct {
	Version int        `json:"version"`
	Purpose string     `json:"purpose"`
	Keys    []TrustKey `json:"keys"`
}

type Target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type Manifest struct {
	BootstrapFrom string      `json:"bootstrapFrom,omitempty"`
	Version       int         `json:"version"`
	Product       string      `json:"product"`
	Channel       string      `json:"channel"`
	Sequence      uint64      `json:"sequence,string"`
	IssuedAt      int64       `json:"issuedAt"`
	ExpiresAt     int64       `json:"expiresAt"`
	Target        Target      `json:"target"`
	Components    []Component `json:"components"`
}

type Artifact struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Component struct {
	Version  string   `json:"version"`
	Artifact Artifact `json:"artifact"`
	Contract Contract `json:"contract"`
}

func KeyID(publicKey []byte) string { h := sha256.Sum256(publicKey); return hex.EncodeToString(h[:]) }

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func hashOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func targetOK(t Target) bool {
	return t == (Target{"linux", "amd64"}) || t == (Target{"darwin", "arm64"}) || t == (Target{"windows", "amd64"})
}

func tokenOK(s string) bool {
	if len(s) == 0 || len(s) > 80 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._:", c)) {
			return false
		}
	}
	return true
}

// StrictJSON also rejects duplicate names (including escaped aliases), excessive
// nesting, trailing values, invalid UTF-8 and unknown typed fields.
func StrictJSON(b []byte, v any) error {
	return strictJSONBounded(b, v, MaxDocument)
}

func strictJSONBounded(b []byte, v any, limit int) error {
	if len(b) == 0 || len(b) > limit || !utf8.Valid(b) {
		return errors.New("invalid document size or encoding")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 20 {
			return errors.New("document nesting limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := k.(string)
				if !ok || seen[s] {
					return errors.New("duplicate document field")
				}
				seen[s] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			_, err = d.Token()
		case json.Delim('['):
			for d.More() {
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
		}
		return err
	}
	if err := walk(0); err != nil {
		return errors.New("invalid or ambiguous JSON")
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid document fields")
	}
	// encoding/json matches struct fields case-insensitively. Require exact field
	// spelling by comparing names against the typed round-trip at every depth.
	canonical, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var original, typed any
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&original)
	d = json.NewDecoder(bytes.NewReader(canonical))
	d.UseNumber()
	_ = d.Decode(&typed)
	if !sameNames(original, typed) {
		return errors.New("noncanonical document field name")
	}
	return nil
}

func sameNames(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !sameNames(v, w) {
				return false
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameNames(x[i], y[i]) {
				return false
			}
		}
	}
	return true
}

func ParseTrust(b []byte, now int64) (Trust, error) {
	var t Trust
	if err := StrictJSON(b, &t); err != nil {
		return t, err
	}
	if t.Version != 1 || t.Purpose != Purpose || len(t.Keys) < 1 || len(t.Keys) > 8 {
		return t, errors.New("explicit development trust required")
	}
	seen := map[string]bool{}
	for _, k := range t.Keys {
		if len(k.PublicKey) != ed25519.PublicKeySize || k.ID != KeyID(k.PublicKey) || seen[k.ID] || k.NotBefore <= 0 || k.NotAfter <= k.NotBefore {
			return t, errors.New("invalid development trust key")
		}
		seen[k.ID] = true
	}
	if now <= 0 {
		return t, errors.New("invalid verifier clock")
	}
	return t, nil
}

// Authenticate signs the exact payload bytes, not a reserialized JSON object.
// The returned digest is of those same bytes. No signing key is accepted here.
func Authenticate(b []byte, trust Trust, now int64) (Manifest, string, error) {
	return authenticate(b, trust, now, lifecycleOK)
}
func authenticate(b []byte, trust Trust, now int64, lifecycle func(*LifecycleContract) bool) (Manifest, string, error) {
	var m Manifest
	var e Envelope
	if err := StrictJSON(b, &e); err != nil {
		return m, "", err
	}
	if e.Version != 1 || !hashOK(e.KeyID) || len(e.Signature) != ed25519.SignatureSize {
		return m, "", errors.New("invalid signed envelope")
	}
	// Validate trust even for callers that constructed it programmatically.
	tb, err := json.Marshal(trust)
	if err != nil {
		return m, "", err
	}
	trust, err = ParseTrust(tb, now)
	if err != nil {
		return m, "", err
	}
	var key *TrustKey
	for i := range trust.Keys {
		if trust.Keys[i].ID == e.KeyID {
			key = &trust.Keys[i]
		}
	}
	if key == nil || now < key.NotBefore || now >= key.NotAfter || !ed25519.Verify(key.PublicKey, append([]byte(SignatureDomain), e.Payload...), e.Signature) {
		return m, "", errors.New("untrusted, expired or invalid development signature")
	}
	if err = StrictJSON(e.Payload, &m); err != nil {
		return m, "", err
	}
	if m.Version != 1 || m.Product != "fased-app" || m.Channel != "development" || m.Sequence == 0 || !targetOK(m.Target) {
		return m, "", errors.New("unsupported development manifest")
	}
	if m.IssuedAt <= 0 || m.IssuedAt > now || m.IssuedAt < key.NotBefore || m.ExpiresAt <= now || m.ExpiresAt <= m.IssuedAt || m.ExpiresAt-m.IssuedAt > 7*86400 || m.ExpiresAt > key.NotAfter {
		return m, "", errors.New("stale or invalid manifest validity")
	}
	if err = validateComponentsFor(m.Components, lifecycle); err != nil {
		return m, "", fmt.Errorf("manifest: %w", err)
	}
	return m, digest(e.Payload), nil
}
