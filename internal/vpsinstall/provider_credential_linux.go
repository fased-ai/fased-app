//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/providersecrets"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

func taskCredentialSize(f hostFile) int64 {
	if f.Size > 0 {
		return f.Size
	}
	return int64(len(f.Data))
}

func inspectTaskCredential(o hostOps, path string) (hostFile, bool, error) {
	var err error
	if o.rootParents != nil {
		err = o.rootParents(path)
	} else {
		err = parents(o, filepath.Dir(path))
	}
	if err != nil {
		return hostFile{}, false, err
	}
	f, err := o.read(path)
	if err == nil {
		if !f.Regular || f.UID != 0 || f.GID != 0 || (f.Mode.Perm() != 0600 && f.Mode.Perm() != 0644) || f.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return f, false, errors.New("preserve foreign protected credential")
		}
		return f, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return hostFile{}, false, err
	}
	if errors.Is(err, os.ErrPermission) {
		if !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm() != 0600 || f.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return f, false, errors.New("preserve foreign protected credential")
		}
		return f, false, nil
	}
	return hostFile{}, false, err
}

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
	// The host-encrypted credential file is 0600 root:root; an ordinary
	// non-root installer verifies its root ancestry, ownership, mode, and
	// bounded size via metadata without elevating to read bytes.
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
	if f, readable, e := inspectTaskCredential(o, target); e == nil {
		if size := taskCredentialSize(f); size < 32 || size > 16384 {
			return errors.New("preserve foreign protected credential")
		}
		if f.Mode.Perm() == 0644 {
			if !readable || len(f.Data) < 32 || len(f.Data) > 16384 {
				return errors.New("preserve foreign protected credential")
			}
			stageMode := filepath.Join(directory, "."+filepath.Base(target)+"-"+m.ID+".mode")
			if _, e = o.run(ctx, f.Data, "/usr/bin/install", "-m", "600", "-o", "root", "-g", "root", "--", "/dev/stdin", stageMode); e != nil {
				return e
			}
			st, stReadable, e := inspectTaskCredential(o, stageMode)
			if e != nil || st.Mode.Perm() != 0600 || taskCredentialSize(st) != int64(len(f.Data)) || (stReadable && !bytes.Equal(st.Data, f.Data)) {
				return errors.New("root staging verification failed")
			}
			if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", stageMode); e != nil {
				return e
			}
			if _, e = o.run(ctx, nil, "/usr/bin/mv", "--force", "--no-target-directory", "--", stageMode, target); e != nil {
				return e
			}
			if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", directory); e != nil {
				return e
			}
		}
		return prepareTaskCredentialDropIn(ctx, o, m)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	stage := filepath.Join(directory, "."+filepath.Base(target)+"-"+m.ID+".pending")
	var encrypted []byte
	staged := false
	if f, readable, e := inspectTaskCredential(o, stage); e == nil {
		if size := taskCredentialSize(f); size < 32 || size > 16384 {
			return errors.New("preserve protected credential staging")
		}
		staged = true
		if readable {
			encrypted = f.Data
		}
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
	if o.protectedPublish != nil {
		if len(encrypted) == 0 {
			return errors.New("preserve protected credential staging")
		}
		if e = o.protectedPublish(target, encrypted, 0600); e != nil {
			return e
		}
	} else {
		if !staged {
			if _, e = o.run(ctx, encrypted, "/usr/bin/install", "-m", "600", "-o", "root", "-g", "root", "--", "/dev/stdin", stage); e != nil {
				return e
			}
			st, readable, e := inspectTaskCredential(o, stage)
			if e != nil || st.Mode.Perm() != 0600 || taskCredentialSize(st) != int64(len(encrypted)) || (readable && !bytes.Equal(st.Data, encrypted)) {
				return errors.New("root staging verification failed")
			}
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", stage); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", stage, target); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", directory); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", directory); e != nil {
			return e
		}
	}
	return prepareTaskCredentialDropIn(ctx, o, m)
}

func validTaskCredentialDropIn(o hostOps, m Manifest, paths string) bool {
	if paths == "" {
		return true
	} // Older installations remain supported without persistence.
	ingress, ingressBody, err := tailnet.ProtectedSocketDropIn("vps", m.OwnerUID)
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, path := range strings.Fields(paths) {
		if seen[path] {
			return false
		}
		seen[path] = true
		var want []byte
		switch path {
		case taskCredentialDropInPath(m):
			want = taskCredentialDropIn(m)
		case ingress:
			if m.PublicOrigin == "" {
				return false
			}
			name, socket, e := tailnet.ProtectedSocketUnit("vps", m.OwnerUID)
			if e != nil {
				return false
			}
			f, e := rootFile(o, "/etc/systemd/system/"+name)
			if e != nil || !known(f, socket, 0644) {
				return false
			}
			want = ingressBody
		default:
			return false
		}
		f, e := rootFile(o, path)
		if e != nil || !known(f, want, 0644) {
			return false
		}
	}
	return len(seen) > 0
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
