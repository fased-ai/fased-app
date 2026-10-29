package core

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"

	"github.com/fased-ai/fased-app/internal/update"
)

// Enrollment images contain app-owned data and encrypted credentials, never
// signer material. The caller must keep the export closer until source fencing.
type EnrollmentImage struct {
	Version    int              `json:"version"`
	InstanceID string           `json:"instanceId"`
	Schema     int              `json:"schema"`
	Files      []EnrollmentFile `json:"files"`
}

type EnrollmentFile struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

const (
	MaxEnrollmentFiles        = 256
	MaxEnrollmentFileBytes    = 64 << 20
	MaxEnrollmentImageBytes   = 128 << 20
	MaxEnrollmentEncodedBytes = (MaxEnrollmentImageBytes*4+2)/3 + 128<<10
)

// EnrollmentImageDigest is an unambiguous content identity, not authority or
// validity. ValidateEnrollmentImage must succeed separately.
func EnrollmentImageDigest(image EnrollmentImage) string {
	h := sha256.New()
	h.Write([]byte("fased-same-instance-enrollment-image:v1\x00"))
	write := func(b []byte) { _ = binary.Write(h, binary.BigEndian, uint64(len(b))); h.Write(b) }
	_ = binary.Write(h, binary.BigEndian, int64(image.Version))
	write([]byte(image.InstanceID))
	_ = binary.Write(h, binary.BigEndian, int64(image.Schema))
	_ = binary.Write(h, binary.BigEndian, uint64(len(image.Files)))
	for _, f := range image.Files {
		write([]byte(f.Path))
		write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DecodeEnrollmentImage reads a bounded private app-state frame. Callers must
// still validate its full schema/content and bind its instance and digest.
func DecodeEnrollmentImage(raw []byte) (EnrollmentImage, error) {
	var image EnrollmentImage
	err := update.StrictJSONBounded(raw, &image, MaxEnrollmentEncodedBytes)
	if err != nil {
		return EnrollmentImage{}, err
	}
	return image, nil
}

func ExportEnrollmentImage(ctx context.Context, dir string) (EnrollmentImage, io.Closer, error) {
	return exportEnrollmentImage(ctx, dir)
}

func ValidateEnrollmentImage(image EnrollmentImage) error { return validateEnrollmentImage(image) }

func ImportEnrollmentImage(ctx context.Context, dir string, image EnrollmentImage) error {
	return importEnrollmentImage(ctx, dir, image)
}
