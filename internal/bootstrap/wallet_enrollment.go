package bootstrap

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/fased-ai/fased-app/internal/update"
)

// ValidWalletEnrollment validates public intent, not owner or spending authority.
func ValidWalletEnrollment(instance, network string) bool {
	b, err := base64.RawURLEncoding.DecodeString(instance)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == instance && (network == "devnet" || network == "mainnet-beta")
}

// WalletPayload is compiled into the authenticated installer itself. It is not
// accepted from CLI/configuration. Ordinary archives retain their old inventory.
type WalletPayload struct {
	Data                    []byte
	SHA256, Version, Source string
}

// WalletPayloadIdentity describes public executable bytes, never wallet keys.
// It is authenticated only when read from the independently verified installer.
type WalletPayloadIdentity struct {
	Version        int    `json:"version"`
	ProductVersion string `json:"productVersion"`
	Source         string `json:"source"`
	SHA256         string `json:"sha256"`
	Bytes          int64  `json:"bytes"`
}

// NativeReleaseBinding is emitted by the already authenticated target core.
// It does not authenticate an arbitrary core or installer supplied by a caller.
type NativeReleaseBinding struct {
	Version        int    `json:"version"`
	ProductVersion string `json:"productVersion"`
	Source         string `json:"source"`
	DesktopSHA256  string `json:"desktopSha256"`
	SignerSHA256   string `json:"signerSha256"`
}

func (b NativeReleaseBinding) Valid(version, source string) bool {
	return b.Version == 1 && b.ProductVersion == version && b.Source == source && sourceHash(source) && hashOK(b.DesktopSHA256) && hashOK(b.SignerSHA256)
}

// NativePairRequired classifies the authenticated product version rather than
// a legacy metadata sequence, which need not encode that version.
func NativePairRequired(version string) (bool, error) {
	sequence, err := releaseSequence(version)
	if err != nil {
		return false, err
	}
	return sequence >= 1_000_013, nil // 0.1.12
}

const MaxWalletPayload = 64 << 20

// WriteWalletPayload is a fixed internal installer operation. It accepts no
// filenames, trust, endpoint or code from its caller.
func WriteWalletPayload(out io.Writer, p *WalletPayload) error {
	if p == nil || len(p.Data) == 0 || len(p.Data) > MaxWalletPayload || !hashOK(p.SHA256) || digest(p.Data) != p.SHA256 || !sourceHash(p.Source) {
		return errors.New("matching embedded signer is unavailable")
	}
	if _, err := releaseSequence(p.Version); err != nil {
		return errors.New("embedded signer product version is invalid")
	}
	id := WalletPayloadIdentity{Version: 1, ProductVersion: p.Version, Source: p.Source, SHA256: p.SHA256, Bytes: int64(len(p.Data))}
	if err := json.NewEncoder(out).Encode(id); err != nil {
		return err
	}
	n, err := out.Write(p.Data)
	if err == nil && n != len(p.Data) {
		err = io.ErrShortWrite
	}
	return err
}

// ReadWalletPayload streams a verified installer's output into caller-owned
// staging. The caller must remove partial output on refusal and authenticate
// the installer itself; an arbitrary caller's matching header is not proof.
func ReadWalletPayload(in io.Reader, out io.Writer, version, source string) (WalletPayloadIdentity, error) {
	var id WalletPayloadIdentity
	r := bufio.NewReaderSize(in, 4096)
	header, err := r.ReadSlice('\n')
	if err != nil || len(header) > 4096 || update.StrictJSON(header, &id) != nil || id.Version != 1 || id.ProductVersion != version || id.Source != source || !sourceHash(source) || !hashOK(id.SHA256) || id.Bytes <= 0 || id.Bytes > MaxWalletPayload {
		return id, errors.New("invalid embedded signer identity")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(out, h), io.LimitReader(r, id.Bytes+1), make([]byte, 32*1024))
	if err != nil {
		return id, err
	}
	if n != id.Bytes || hex.EncodeToString(h.Sum(nil)) != id.SHA256 {
		return id, errors.New("embedded signer content differs from its bound identity")
	}
	return id, nil
}
