//go:build linux

package custodyinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

func protectedEnrollmentTarget(owner int, id string) (Manifest, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return Manifest{}, err
	}
	m, err := Load(owner)
	if err != nil || !update.IDOK(id) || m.ID != id || m.Owner != owner || m.Version != 3 || !m.Enrollment || m.Location != a.Profile {
		return Manifest{}, errors.New("exact protected enrollment target required")
	}
	if err = Verify(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// PrepareProtectedEnrollmentActivation publishes only generated bindings and
// fixed service definitions after the controller verified the imported image.
func PrepareProtectedEnrollmentActivation(ctx context.Context, owner int, id, instance, digest string) (Manifest, error) {
	m, err := protectedEnrollmentTarget(owner, id)
	if err != nil || !enrollmentInstance(instance) || !validHex(digest, 32) {
		return Manifest{}, errors.New("exact protected activation required")
	}
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return Manifest{}, err
	}
	want := EnrollmentCommit{Version: 1, ManifestSHA256: sum(mustJSON(EnrollmentIdentity(m))), InstanceID: instance, Digest: digest}
	if _, err = os.Lstat(m.Root + "/enrollment-ready.json"); errors.Is(err, os.ErrNotExist) {
		if err = CheckProtectedEnrollmentSigner(owner, id); err != nil {
			return Manifest{}, err
		}
		if err = maintenance.PublishProtected(m.Root+"/enrollment-ready.json", mustJSON(want), 0644); err != nil {
			return Manifest{}, err
		}
	} else if err != nil {
		return Manifest{}, err
	} else {
		got, e := ReadEnrollmentCommit(m)
		if e != nil || got != want {
			return Manifest{}, errors.New("existing enrollment commit differs; preserving it")
		}
	}
	// Install the same fail-closed start guards used by the updater before
	// exposing either service to the already-existing native start policy.
	for _, role := range []string{"signer", "app"} {
		path := protectedGuardPath(owner, role)
		if err = maintenance.EnsureProtectedDirectory(filepath.Dir(path)); err != nil {
			return Manifest{}, err
		}
		if err = maintenance.PublishProtected(path, protectedGuard(a, role), 0644); err != nil {
			return Manifest{}, err
		}
	}
	for _, role := range []string{"signer", "app"} {
		if err = maintenance.PublishProtected("/etc/systemd/system/"+Unit(owner, role), definitions(m)["fased-"+role+".service"], 0644); err != nil {
			return Manifest{}, err
		}
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return Manifest{}, err
	}
	for _, role := range []string{"signer", "app"} {
		if err = loadedUnit(m, role); err != nil {
			return Manifest{}, err
		}
	}
	return m, nil
}

// StartProtectedEnrollmentServices preserves the source's run/autostart intent
// and verifies the live app/signer using their own unprivileged identities.
func StartProtectedEnrollmentServices(ctx context.Context, owner int, m Manifest, instance string, running, autostart bool) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil || m.Owner != owner || m.Location != a.Profile || !enrollmentInstance(instance) {
		return errors.New("exact protected activation required")
	}
	actual, err := Load(owner)
	if err != nil || actual != m || Verify(m) != nil {
		return errors.New("enrollment target changed; preserving it")
	}
	commit, err := ReadEnrollmentCommit(m)
	if err != nil || commit.InstanceID != instance {
		if err == nil {
			err = errors.New("enrollment instance differs; preserving it")
		}
		return err
	}
	if autostart {
		if _, err = protectedSystemctl(ctx, "enable", "--", Unit(owner, "signer"), Unit(owner, "app")); err != nil {
			return err
		}
	}
	if !running {
		return maintenance.PublishProtected(m.Root+"/ready.json", readyRecord(m), 0644)
	}
	for _, role := range []string{"signer", "app"} {
		if _, err = protectedSystemctl(ctx, "start", "--", Unit(owner, role)); err != nil {
			return err
		}
	}
	until := time.NewTimer(15 * time.Second)
	defer until.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}}
	defer client.CloseIdleConnections()
	for {
		if Runtime(m) == nil {
			var private core.Health
			if readMaintenanceRole(ctx, m, "app", m.Root+"/fased-app", []string{"status", "--data-dir", m.Root + "/app-state"}, &private) == nil && private.InstanceID == instance && private.Connection != nil && private.Connection.Location == m.Location && private.Connection.OwnerUID == owner {
				resp, e := client.Get("http://" + m.Listen + "/api/v1/health")
				if e == nil {
					var h core.Health
					decode := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&h)
					resp.Body.Close()
					var status signer.Status
					signerErr := readMaintenanceRole(ctx, m, "owner", m.Root+"/fased-signer", []string{"owner", "--config", m.Root + "/identity.json", signerModeFlag(m)}, &status)
					if decode == nil && resp.StatusCode == 200 && h.Status == "ok" && h.InstanceID == instance && h.APIVersion == core.APIVersion && len(h.Capabilities) > 0 && !h.Capabilities["walletSigner"] && !h.Capabilities["financialExecution"] && !h.Capabilities["updater"] && signerErr == nil && signerStatusReady(m, status) {
						return maintenance.PublishProtected(m.Root+"/ready.json", readyRecord(m), 0644)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-until.C:
			return errors.New("enrolled services are not yet ready; preserve state and retry")
		case <-ticker.C:
		}
	}
}

// WithProtectedEnrollmentSystem is an in-process seam for the fixed helper's
// reviewed cutover controller. No command or callback is decoded from IPC.
func WithProtectedEnrollmentSystem(owner int, id string, sourceUID, sourceGID int, source, instance string, f func(*System) error) error {
	if f == nil {
		return errors.New("protected enrollment operation required")
	}
	m, err := protectedEnrollmentTarget(owner, id)
	if err != nil {
		return err
	}
	if sourceUID <= 0 || sourceGID <= 0 || sourceUID == m.App.UID || sourceUID == m.Signer.UID || !filepath.IsAbs(source) || filepath.Clean(source) != source || !enrollmentInstance(instance) {
		return errors.New("fixed enrollment source identity required")
	}
	return f(&System{Out: io.Discard, protectedOwner: owner, protectedID: id, protectedPurpose: "cutover", protectedSourceUID: sourceUID, protectedSourceGID: sourceGID, protectedSource: source, protectedInstance: instance})
}

// CheckProtectedEnrollmentSigner inspects directory metadata only. It neither
// opens wallet material nor changes any retained signer state.
func CheckProtectedEnrollmentSigner(owner int, id string) error {
	m, err := protectedEnrollmentTarget(owner, id)
	if err != nil {
		return err
	}
	fd, err := unix.Open(m.Root+"/signer-state", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "signer-state")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || int(st.Uid) != m.Signer.UID || int(st.Gid) != m.Signer.GID || st.Mode&07777 != 0700 {
		return errors.New("signer state identity changed; preserving it")
	}
	entries, err := f.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) != 0 {
		return errors.New("target signer state is not empty; preserve keys and recovery state")
	}
	return nil
}

// PrepareProtectedEnrollmentStage creates only this target's fixed app-owned
// stage. An interrupted empty root-owned creation may be finished in place.
func PrepareProtectedEnrollmentStage(owner int, id string) error {
	m, err := protectedEnrollmentTarget(owner, id)
	if err != nil {
		return err
	}
	if err = CheckProtectedEnrollmentSigner(owner, id); err != nil {
		return err
	}
	if _, err = os.Lstat(m.Root + "/enrollment-ready.json"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing enrollment commit must be preserved")
	}
	if _, err = update.ReadMaintenanceRecord(owner); !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing maintenance journal must be preserved")
	}
	rootFD, err := unix.Open(m.Root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	root := os.NewFile(uintptr(rootFD), "custody-root")
	defer root.Close()
	name := fmt.Sprintf("enrollment-%s.stage", id)
	if err = unix.Mkdirat(rootFD, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	stageFD, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	stage := os.NewFile(uintptr(stageFD), "enrollment-stage")
	defer stage.Close()
	var st unix.Stat_t
	if unix.Fstat(stageFD, &st) != nil || st.Mode&07777 != 0700 {
		return errors.New("foreign enrollment stage; preserving it")
	}
	if st.Uid == 0 && st.Gid == 0 {
		entries, e := stage.ReadDir(1)
		if e != nil && !errors.Is(e, io.EOF) {
			return e
		}
		if len(entries) != 0 {
			return errors.New("unclassified enrollment stage; preserving it")
		}
		if err = unix.Fchown(stageFD, m.App.UID, m.App.GID); err != nil {
			return err
		}
	} else if int(st.Uid) != m.App.UID || int(st.Gid) != m.App.GID {
		return errors.New("foreign enrollment stage; preserving it")
	}
	if err = stage.Sync(); err != nil {
		return err
	}
	return root.Sync()
}
