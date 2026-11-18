//go:build linux

package custodyinstall

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/providersecrets"
)

const packageTaskCredential = providersecrets.CredentialName + ".cred"
const packageTaskCredentialLimit = 16 << 10

func packageTaskCredentialDropIn(m Manifest) string {
	return filepath.Join("/etc/systemd/system", instanceUnit(m, "app")+".d", "30-fased-provider-credentials.conf")
}

func packageTaskCredentialUnit(m Manifest) []byte {
	return []byte("[Service]\nLoadCredentialEncrypted=" + providersecrets.CredentialName + ":" + filepath.Join(m.Root, packageTaskCredential) + "\n")
}

// Called under the original provisioning lock, before the package root is
// published. A retry after publication must never create a replacement key.
func preparePackageTaskCredential(ctx context.Context, stage string, run func(context.Context, []byte, ...string) ([]byte, error), digest func(string, os.FileMode, int64) (string, int64, error), publish func(string, []byte, os.FileMode) error) error {
	path := filepath.Join(stage, packageTaskCredential)
	_, size, err := digest(path, 0600, packageTaskCredentialLimit)
	if err == nil {
		if size < 32 {
			return errors.New("invalid protected provider credential; preserving it")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var secret [32]byte
	defer clear(secret[:])
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	encrypted, err := run(ctx, secret[:], "/usr/bin/systemd-creds", "encrypt", "--with-key=host", "--name="+providersecrets.CredentialName, "-", "-")
	if err != nil || len(encrypted) < 32 || len(encrypted) > packageTaskCredentialLimit {
		return errors.New("could not seal the package provider credential with systemd")
	}
	return publish(path, encrypted, 0600)
}

func publishPackageTaskCredentialUnit(m Manifest) error {
	if _, size, err := maintenance.ProtectedDigest(filepath.Join(m.Root, packageTaskCredential), 0600, packageTaskCredentialLimit); err != nil || size < 32 {
		return errors.New("the original package provider credential is unavailable; preserve it and its grants")
	}
	path := packageTaskCredentialDropIn(m)
	if err := maintenance.EnsureProtectedDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return maintenance.PublishProtected(path, packageTaskCredentialUnit(m), 0644)
}

func allowedPackageServiceDropIns(m Manifest, role, value string, read readHost) bool {
	var other []string
	seenCredential := false
	for _, path := range strings.Fields(value) {
		if path != packageTaskCredentialDropIn(m) {
			other = append(other, path)
			continue
		}
		if role != "app" || seenCredential || read == nil {
			return false
		}
		file, err := read(path)
		if err != nil || !known(file, packageTaskCredentialUnit(m), 0644) {
			return false
		}
		seenCredential = true
	}
	return allowedPackageDropIns(strings.Join(other, " "), read)
}
