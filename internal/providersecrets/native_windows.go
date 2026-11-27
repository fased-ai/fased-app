//go:build windows

package providersecrets

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func applyOwnerOnlyWindowsDACL(path string) error {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func windowsVaultDir() (string, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", ErrUnavailable
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "Fased App", "vault"), nil
}

func windowsVaultFile(instance string) (string, error) {
	if instance == "" || len(instance) > 256 || strings.ContainsAny(instance, "\r\n\x00/\\") {
		return "", ErrUnavailable
	}
	dir, err := windowsVaultDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("fased-dpapi-provider-key:" + instance))
	return filepath.Join(dir, hex.EncodeToString(sum[:16])+".dpapi"), nil
}

func dpapiEntropy(instance string) []byte {
	return []byte("fased-provider-task-access:" + instance)
}

func dpapiProtect(plain, entropy []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, ErrUnavailable
	}
	inBlob := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var entBlob *windows.DataBlob
	if len(entropy) > 0 {
		entBlob = &windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	}
	var outBlob windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, entBlob, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob); err != nil {
		return nil, ErrUnavailable
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))
	out := make([]byte, outBlob.Size)
	copy(out, unsafe.Slice(outBlob.Data, outBlob.Size))
	return out, nil
}

func dpapiUnprotect(cipherBytes, entropy []byte) ([]byte, error) {
	if len(cipherBytes) == 0 {
		return nil, ErrUnavailable
	}
	inBlob := windows.DataBlob{Size: uint32(len(cipherBytes)), Data: &cipherBytes[0]}
	var entBlob *windows.DataBlob
	if len(entropy) > 0 {
		entBlob = &windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	}
	var outBlob windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, entBlob, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob); err != nil {
		return nil, ErrUnavailable
	}
	plainSlice := unsafe.Slice(outBlob.Data, outBlob.Size)
	defer func() {
		clear(plainSlice)
		windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))
	}()
	out := make([]byte, outBlob.Size)
	copy(out, plainSlice)
	return out, nil
}

// Key retrieves or creates the per-instance 32-byte encryption key sealed by
// Windows DPAPI (current OS user scope, UI forbidden) in a separate vault path.
func (Native) Key(_ context.Context, instance string, create bool) ([]byte, error) {
	path, err := windowsVaultFile(instance)
	if err != nil {
		return nil, ErrUnavailable
	}
	entropy := dpapiEntropy(instance)
	if raw, readErr := os.ReadFile(path); readErr == nil {
		if len(raw) == 0 || len(raw) > 4096 {
			return nil, ErrUnavailable
		}
		key, decErr := dpapiUnprotect(raw, entropy)
		if decErr != nil || len(key) != 32 {
			clear(key)
			return nil, ErrUnavailable
		}
		return key, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, ErrUnavailable
	}
	if !create {
		return nil, ErrMissing
	}

	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	sealed, err := dpapiProtect(key, entropy)
	if err != nil {
		clear(key)
		return nil, ErrUnavailable
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		clear(key)
		return nil, ErrUnavailable
	}
	if err = applyOwnerOnlyWindowsDACL(dir); err != nil {
		clear(key)
		return nil, ErrUnavailable
	}
	tmp, err := os.CreateTemp(dir, ".vault-*")
	if err != nil {
		clear(key)
		return nil, ErrUnavailable
	}
	tmpName := tmp.Name()
	if _, err = tmp.Write(sealed); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		clear(key)
		return nil, ErrUnavailable
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		clear(key)
		return nil, ErrUnavailable
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		clear(key)
		return nil, ErrUnavailable
	}
	if err = applyOwnerOnlyWindowsDACL(tmpName); err != nil {
		_ = os.Remove(tmpName)
		clear(key)
		return nil, ErrUnavailable
	}
	if err = os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}
