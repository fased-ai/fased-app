//go:build linux

package vpsinstall

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/providersecrets"
)

// The existing installer executes only fixed OS utilities with privilege. The
// core receives a read-only decrypted credential and remains a locked app user.
// Missing support leaves background persistence unavailable, never unprotected.
func prepareTaskCredential(ctx context.Context, o hostOps, m Manifest) error {
	if e := m.validate(); e != nil {
		return e
	}
	tool, e := o.read("/usr/bin/systemd-creds")
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || !tool.Regular || tool.UID != 0 || tool.Mode.Perm()&0022 != 0 || tool.Mode.Perm()&0111 == 0 {
		return errors.New("protected credential OS tool unavailable")
	}
	if e = trustedTool(o, "/usr/bin/systemd-creds"); e != nil {
		return e
	}
	// Only the host-encrypted envelope is readable for ordinary-owner integrity
	// verification. Its plaintext wrapping key stays in systemd's root-only
	// store; no privileged read interface or shared OS directory is relaxed.
	directory := taskCredentialDirectory
	entry, e := o.read(directory)
	if errors.Is(e, os.ErrNotExist) {
		if _, e = o.run(ctx, nil, "/usr/bin/install", "-d", "-m", "0755", "-o", "root", "-g", "root", "--", directory); e != nil {
			return e
		}
	} else if e != nil || !entry.Directory || entry.UID != 0 || entry.Mode.Perm() != 0755 {
		return errors.New("preserve foreign credential directory")
	}
	target := filepath.Join(directory, taskCredentialID(m))
	if f, e := rootFile(o, target); e == nil {
		if !f.Regular || f.Mode.Perm() != 0644 || len(f.Data) < 32 || len(f.Data) > 16384 {
			return errors.New("preserve foreign protected credential")
		}
		return prepareTaskCredentialDropIn(ctx, o, m)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	stage := filepath.Join(directory, "."+filepath.Base(target)+"-"+m.ID+".pending")
	var encrypted []byte
	if f, e := rootFile(o, stage); e == nil {
		if !f.Regular || f.Mode.Perm() != 0644 || len(f.Data) < 32 || len(f.Data) > 16384 {
			return errors.New("preserve protected credential staging")
		}
		encrypted = f.Data
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	} else {
		secret := make([]byte, 32)
		defer clear(secret)
		if _, e = rand.Read(secret); e != nil {
			return e
		}
		encrypted, e = o.run(ctx, secret, "/usr/bin/systemd-creds", "encrypt", "--with-key=host", "--name="+providersecrets.CredentialName, "-", "-")
		if e != nil {
			return e
		}
		if len(encrypted) < 32 || len(encrypted) > 16384 {
			return errors.New("invalid protected credential envelope")
		}
	}
	// Keep this encrypted OS key on removal/repair just like retained app state.
	if e = publish(ctx, o, target, m.ID, encrypted, 0644); e != nil {
		return e
	}
	return prepareTaskCredentialDropIn(ctx, o, m)
}

func validTaskCredentialDropIn(o hostOps, m Manifest, paths string) bool {
	if paths == "" {
		return true
	} // Older installations remain supported without persistence.
	if paths != taskCredentialDropInPath(m) {
		return false
	}
	f, e := rootFile(o, paths)
	return e == nil && known(f, taskCredentialDropIn(m), 0644)
}

func prepareTaskCredentialDropIn(ctx context.Context, o hostOps, m Manifest) error {
	dir := filepath.Dir(taskCredentialDropInPath(m))
	entry, e := o.read(dir)
	if errors.Is(e, os.ErrNotExist) {
		if _, e = o.run(ctx, nil, "/usr/bin/install", "-d", "-m", "0755", "-o", "root", "-g", "root", "--", dir); e != nil {
			return e
		}
	} else if e != nil || !entry.Directory || entry.UID != 0 || entry.Mode.Perm()&0022 != 0 {
		return errors.New("preserve foreign service credential directory")
	}
	if e = publish(ctx, o, taskCredentialDropInPath(m), m.ID, taskCredentialDropIn(m), 0644); e != nil {
		return e
	}
	_, e = o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload")
	return e
}

func removeTaskCredentialDropIn(ctx context.Context, o hostOps, m Manifest) error {
	path := taskCredentialDropInPath(m)
	f, e := rootFile(o, path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || !known(f, taskCredentialDropIn(m), 0644) {
		return errors.New("preserve foreign service credential override")
	}
	if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", path); e != nil {
		return e
	}
	_, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path))
	// Retain the encrypted OS credential with user data; an empty unit directory
	// is harmless and may contain unrelated policy, so never remove it broadly.
	return e
}
