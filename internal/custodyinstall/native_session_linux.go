//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fased-ai/fased-app/internal/signer"
)

// The existing reader-session client primes only the enrolled Local app's own
// private CLI session. It never receives a browser token, wallet key or password.
type nativeUpdateSessionBinding struct {
	Version      int      `json:"version"`
	Installation Manifest `json:"installation"`
	ImageSHA256  string   `json:"imageSha256"`
}

func (b nativeUpdateSessionBinding) dir() string {
	return b.Installation.Root + "/reader-session-" + sum(mustJSON(b))[:24]
}
func nativeUpdateSessionProfile(m Manifest) bool {
	return m.validate() == nil && m.Version == 3 && m.Enrollment && m.NativeOwnerAccess && m.Location == "local"
}

func (s *System) PrepareNativeUpdateSession(ctx context.Context, m Manifest, image string) error {
	if !nativeUpdateSessionProfile(m) || m.Owner != os.Getuid() || os.Geteuid() != m.Owner {
		return errors.New("existing Local custody OS owner required")
	}
	lock, err := OwnerLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := Load(m.Owner)
	if err != nil || current != m || Verify(m) != nil {
		return errors.New("Local custody installation changed")
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return err
	}
	// Corrected installed clients refresh through their own exact AppUID helper.
	// Only an older immutable client needs the current source-session bridge.
	if image == m.Root+"/fased-app" {
		return nil
	}
	if err = verifyNativePolicy(ctx, m, s); err != nil {
		return err
	}
	if err = Runtime(m); err != nil {
		return errors.New("open the existing Local instance before maintenance")
	}

	body, err := source(image)
	if err != nil {
		return err
	}
	return stageNativeUpdateSession(ctx, m, body, nativeUpdateSessionOps{read: readRoot, run: s.Run, mkdir: func(ctx context.Context, path string) error { return directory(ctx, path, 0, 0, 0755, s) }})
}

type nativeUpdateSessionOps struct {
	read  readHost
	run   runHost
	mkdir func(context.Context, string) error
}

func stageNativeUpdateSession(ctx context.Context, m Manifest, body []byte, o nativeUpdateSessionOps) error {
	if !nativeUpdateSessionProfile(m) || len(body) == 0 || len(body) > 64<<20 {
		return errors.New("bounded verified Local reader required")
	}
	b := nativeUpdateSessionBinding{1, m, sum(body)}
	dir := b.dir()
	if err := o.mkdir(ctx, dir); err != nil {
		return err
	}
	image, record := dir+"/helper", dir+"/binding.json"
	if err := publish(ctx, image, m.ID, body, 0755, o.read, o.run); err != nil {
		return err
	}
	if err := publish(ctx, record, m.ID, mustJSON(b), 0644, o.read, o.run); err != nil {
		return err
	}
	args := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(m.App.UID), "--regid", strconv.Itoa(m.App.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", image, "reader-session-client", "--custody-owner", strconv.Itoa(m.Owner), "--binding", record}
	result, err := o.run(ctx, nil, args...)
	if err != nil {
		return err
	}
	if string(result) != "Ready\n" {
		return errors.New("Local reader did not confirm session readiness")
	}
	return nil
}

func NativeUpdateSessionIdentity(owner int, image, record string) (Manifest, string, error) {
	m, err := Load(owner)
	if err != nil {
		return Manifest{}, "", err
	}
	f, err := readRoot(record)
	if err != nil {
		return Manifest{}, "", err
	}
	var b nativeUpdateSessionBinding
	if strict(f.Data, &b) != nil || !known(f, mustJSON(b), 0644) || validateNativeUpdateSession(m, b, os.Getuid(), os.Geteuid(), os.Getgid(), image, record) != nil {
		return Manifest{}, "", errors.New("exact Local reader-session binding required")
	}
	instance, err := nativeUpdateSessionInstance(m, m.Root+"/app-state")
	if err != nil {
		return Manifest{}, "", err
	}
	f, err = readRoot(image)
	if err != nil || f.Mode != 0755 || sum(f.Data) != b.ImageSHA256 {
		return Manifest{}, "", errors.New("Local reader-session image changed")
	}
	return m, instance, nil
}
func validateNativeUpdateSession(m Manifest, b nativeUpdateSessionBinding, uid, euid, gid int, image, record string) error {
	if !nativeUpdateSessionProfile(m) || b.Version != 1 || b.Installation != m || !validHex(b.ImageSHA256, 32) || uid != m.App.UID || euid != uid || gid != m.App.GID || image != filepath.Join(b.dir(), "helper") || record != filepath.Join(b.dir(), "binding.json") {
		return errors.New("Local app identity and immutable reader binding required")
	}
	return nil
}

// Installed update helpers must attest their own custody image and AppUID; a
// private endpoint, OS owner or separately installed app cannot stand in for it.
func InstalledNativeUpdateSessionIdentity(owner int, image, data string) (Manifest, string, error) {
	m, err := Load(owner)
	if err != nil {
		return Manifest{}, "", err
	}
	if err = validateInstalledNativeUpdateSession(m, os.Getuid(), os.Geteuid(), os.Getgid(), image, data); err != nil {
		return Manifest{}, "", err
	}
	instance, err := nativeUpdateSessionInstance(m, data)
	return m, instance, err
}
func validateInstalledNativeUpdateSession(m Manifest, uid, euid, gid int, image, data string) error {
	if !nativeUpdateSessionProfile(m) || uid != m.App.UID || euid != uid || gid != m.App.GID || image != m.Root+"/fased-app" || data != m.Root+"/app-state" {
		return errors.New("exact installed Local custody app required")
	}
	return nil
}
func nativeUpdateSessionInstance(m Manifest, data string) (string, error) {
	if err := Verify(m); err != nil {
		return "", err
	}
	if err := signer.CheckApplication(m.Root+"/identity.json", m.Root, data); err != nil {
		return "", err
	}
	commit, err := ReadEnrollmentCommit(m)
	return commit.InstanceID, err
}
