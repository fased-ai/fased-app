//go:build linux

package custodyinstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// PackageProvisionInput is assembled by the fixed helper from its verified
// candidate. It contains no caller-selected path, executable, or OS identity.
type PackageProvisionInput struct {
	Version, Source, ReleaseSHA256, ProvenanceSHA256 string
	Core, Signer                                     update.Artifact
	Desktop                                          *update.Artifact
	Native                                           update.NativePackage
	Bundle                                           *update.LinuxBundle `json:"Bundle,omitempty"`
	Sequence                                         uint64
	Listen                                           string
	PublicOrigin                                     string `json:",omitempty"`
}

func (i PackageProvisionInput) installerDigest() string {
	if i.Bundle != nil && i.Native.InstallerSHA256 == "" {
		return i.Bundle.InstallerSHA256
	}
	if i.Bundle == nil {
		return i.Native.InstallerSHA256
	}
	return ""
}

type packageProvisionIntent struct {
	Version  int                   `json:"version"`
	Owner    int                   `json:"owner"`
	Location string                `json:"location"`
	Instance string                `json:"instance"`
	Input    PackageProvisionInput `json:"input"`
}

// ProvisionProtectedPackage installs one fresh instance from the
// original independently verified candidate. Existing roots are never adopted.
// It holds the instance lock through publication and releases it before
// starting the service, whose recovery unit takes the same lock.
func ProvisionProtectedPackage(ctx context.Context, owner int, location, instance string, input PackageProvisionInput, lock *os.File) error {
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil || lock == nil || !packageUpdateConnected || (location != "local" && location != "remote") || !validListen(input.Listen) || !update.IDOK(instance) {
		return errors.New("one exact fresh package is required")
	}
	receipt, err := maintenance.ReadPackageCandidateReceipt(owner, location, instance)
	if err != nil || receipt.Scope != "fresh" || receipt.InstallerSHA256 != a.ImageSHA256 || receipt.EngineSHA256 != input.Core.SHA256 ||
		receipt.ReleaseSHA256 != input.ReleaseSHA256 || receipt.Sequence != input.Sequence || receipt.ProductVersion != input.Version || input.installerDigest() != a.ImageSHA256 ||
		input.Core.File != "fased-app" || input.Signer.File != "fased-signer" || input.Core.Size <= 0 || input.Signer.Size <= 0 {
		return errors.New("the original verified fresh package is required")
	}
	if location == "remote" {
		approval, e := privateaccess.ReadProtectedPackageInitialApproval(owner, instance)
		if e != nil || !approval.MatchesPackage(owner, instance, input.PublicOrigin, input.Listen, input.Sequence, input.ReleaseSHA256, input.Core.SHA256) {
			return errors.New("the original private device approval is required")
		}
	} else if input.PublicOrigin != "" {
		return errors.New("Local package cannot select a private Remote origin")
	}
	state, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return err
	}
	root, err := PackageRoot(owner, location, instance)
	if err != nil {
		return err
	}
	provenance, err := packageProvisionProvenance(owner, location, instance)
	if err != nil {
		return err
	}
	stage := filepath.Join(filepath.Dir(root), "."+instance+".pending")
	intent := packageProvisionIntent{Version: 1, Owner: owner, Location: location, Instance: instance, Input: input}
	intentBytes, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	intentPath := filepath.Join(state, "provision.json")
	if _, err = os.Lstat(intentPath); errors.Is(err, os.ErrNotExist) {
		for _, path := range []string{root, stage} {
			if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				return errors.New("an unclassified package installation exists; preserve it")
			}
		}
	} else if err != nil {
		return err
	}
	if err = maintenance.PublishProtected(intentPath, intentBytes, 0644); err != nil {
		return err
	}
	for _, dir := range []string{"/var/lib/fased-app-package", "/var/lib/fased-app-package/" + strconv.Itoa(owner), filepath.Dir(root)} {
		if err = maintenance.EnsureProtectedDirectory(dir); err != nil {
			return err
		}
	}
	s := &System{protectedOwner: owner, protectedID: instance, protectedPurpose: "prepare", Out: io.Discard}
	appName, signerName := packageAccountName(owner, location, instance, "app"), packageAccountName(owner, location, instance, "signer")
	app, err := accountWithMarker(ctx, appName, "Fased "+instance+" app", s)
	if err != nil {
		return err
	}
	key, err := accountWithMarker(ctx, signerName, "Fased "+instance+" signer", s)
	if err != nil {
		return err
	}
	for _, name := range []string{appName, signerName} {
		if err = requireLockedPackageAccount(ctx, name); err != nil {
			return err
		}
	}
	m := Manifest{Layout: PackageLayout, Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: provenance,
		Enrollment: true, Location: location, PublicOrigin: input.PublicOrigin, ID: instance, Owner: owner, Root: root, Listen: input.Listen, CoreHash: input.Core.SHA256, SignerHash: input.Signer.SHA256, App: app, Signer: key}
	if err = m.validatePackage(); err != nil {
		return err
	}
	inputDir := filepath.Join(state, "provision")
	components, err := describeProtectedPair(ctx, m, inputDir, input.Version, input.Source, readMaintenanceRole, maintenance.ProtectedDigest)
	if err != nil {
		return err
	}
	pack := update.MaintenancePackage{Version: input.Version, Source: input.Source, Sequence: input.Sequence, ReleaseSHA256: input.ReleaseSHA256,
		ProvenanceSHA256: input.ProvenanceSHA256, Components: components, NativeDesktop: input.Desktop, Bundle: input.Bundle}
	if input.Native.InstallerSHA256 != "" {
		pack.NativePackage = &input.Native
	}
	if pack.Validate() != nil || !packagePairMatches(pack, m) {
		return errors.New("the verified package pair differs from its provisioning intent")
	}
	if _, err = os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		if err = maintenance.EnsureProtectedDirectory(stage); err != nil {
			return err
		}
		if err = preparePackageTaskCredential(ctx, stage, s.Run, maintenance.ProtectedDigest, maintenance.PublishProtected); err != nil {
			return err
		}
		for _, item := range []struct {
			name string
			who  Identity
			mode os.FileMode
		}{{"app-state", app, 0700}, {"signer-state", key, 0700}, {"signer-ipc", key, 0755}} {
			if err = directory(ctx, filepath.Join(stage, item.name), item.who.UID, item.who.GID, item.mode, s); err != nil {
				return err
			}
		}
		if input.Desktop != nil {
			if err = maintenance.CopyProtectedBounded(filepath.Join(inputDir, "fased-desktop"), filepath.Join(stage, "fased-desktop"), 0755, input.Desktop.SHA256, input.Desktop.Size); err != nil {
				return err
			}
		}
		probe := filepath.Join(stage, "app-readonly-probe")
		if _, e := os.Lstat(probe); errors.Is(e, os.ErrNotExist) {
			if _, e = s.Run(ctx, nil, "/usr/bin/install", "-m", "0600", "-o", strconv.Itoa(app.UID), "-g", strconv.Itoa(app.GID), "--", "/dev/null", probe); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		if err = verifyProbe(probe, app); err != nil {
			return err
		}
		for _, item := range []struct {
			name, hash string
			size       int64
		}{{"fased-app", input.Core.SHA256, input.Core.Size}, {"fased-signer", input.Signer.SHA256, input.Signer.Size}} {
			if err = maintenance.CopyProtectedBounded(filepath.Join(inputDir, item.name), filepath.Join(stage, item.name), 0755, item.hash, item.size); err != nil {
				return err
			}
		}
		helper := maintenance.ImagePath(a.ImageSHA256)
		helperHash, helperSize, e := maintenance.ProtectedDigest(helper, 0555, 128<<20)
		if e != nil || helperHash != a.ImageSHA256 {
			return errors.New("the original protected package helper is unavailable")
		}
		if err = maintenance.CopyProtectedExecutable(helper, filepath.Join(stage, "fased-install"), helperHash, helperSize); err != nil {
			return err
		}
		files := definitions(m)
		files["identity.json"], files["install.json"] = mustJSON(identityConfig(m)), mustJSON(m)
		for _, name := range []string{"identity.json", "install.json", "fased-app.service", "fased-signer.service"} {
			if err = maintenance.PublishProtected(filepath.Join(stage, name), files[name], 0644); err != nil {
				return err
			}
		}
		if err = unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, root, unix.RENAME_NOREPLACE); err != nil {
			return errors.New("package instance publication conflicted; preserve both")
		}
		d, e := os.Open(filepath.Dir(root))
		if e != nil {
			return e
		}
		if err = errors.Join(d.Sync(), d.Close()); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	actual, err := LoadPackageInstance(owner, root)
	if err != nil || actual != m || VerifyPackageInstance(actual) != nil {
		return errors.New("the original provisioned package differs; preserve it")
	}
	if err = provisionNativeBootstrap(ctx, m, pack); err != nil {
		return err
	}
	for _, role := range []string{"app", "signer"} {
		if err = maintenance.PublishProtected(filepath.Join("/etc/systemd/system", instanceUnit(m, role)), definitions(m)["fased-"+role+".service"], 0644); err != nil {
			return err
		}
	}
	if err = publishPackageTaskCredentialUnit(m); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err = InitializeProtectedPackageJournal(ctx, owner, location, instance, pack); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "enable", "--", instanceUnit(m, "app"), instanceUnit(m, "signer")); err != nil {
		return err
	}
	if err = startProvisionedPackage(ctx, m, lock, protectedSystemctl, waitPackageOwnerHealth, bindProvisionedPackageOwner); err != nil {
		return err
	}
	if m.Location == "remote" {
		return enableProtectedPackageRemote(ctx, m, os.Stderr)
	}
	return nil
}

func requireLockedPackageAccount(ctx context.Context, name string) error {
	output, err := exec.CommandContext(ctx, "/usr/bin/getent", "shadow", name).Output()
	if err != nil {
		return errors.New("locked service account could not be verified")
	}
	parts := strings.Split(strings.TrimSpace(string(output)), ":")
	if len(parts) < 2 || parts[0] != name || !(strings.HasPrefix(parts[1], "!") || strings.HasPrefix(parts[1], "*")) {
		return errors.New("service account must remain locked")
	}
	return nil
}

func startProvisionedPackage(ctx context.Context, m Manifest, lock *os.File, systemctl func(context.Context, ...string) ([]byte, error), ready, bind func(context.Context, Manifest) error) error {
	if lock == nil || systemctl == nil || ready == nil || bind == nil {
		return errors.New("the original provisioning lock and startup are required")
	}
	return packageOwnerAfterUnlock(lock, func() error {
		if _, err := systemctl(ctx, "start", "--", instanceUnit(m, "app")); err != nil {
			return err
		}
		if err := ready(ctx, m); err != nil {
			return err
		}
		return bind(ctx, m)
	})
}

// Record the running instance before installation succeeds. Browser updates
// must not depend on a later CLI command creating the owner commitment.
func bindProvisionedPackageOwner(ctx context.Context, m Manifest) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	lock, err := maintenance.WaitPackageLock(ctx, m.Owner, m.Location, m.ID)
	if err != nil {
		return err
	}
	defer lock.Close()
	const probe = "GET /api/v1/health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"
	var response bytes.Buffer
	if err = PackageOwnerChannel(ctx, m.Owner, m.Location, m.ID, lock, strings.NewReader(probe), &response); err != nil {
		return err
	}
	res, err := http.ReadResponse(bufio.NewReader(&response), nil)
	if err != nil {
		return errors.New("the installed package owner response is unavailable")
	}
	defer res.Body.Close()
	var health core.Health
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") || json.NewDecoder(io.LimitReader(res.Body, 32<<10)).Decode(&health) != nil {
		return errors.New("the installed package owner response is invalid")
	}
	want, err := packageOwnerCommit(m, health)
	if err != nil {
		return err
	}
	actual, err := ReadEnrollmentCommit(m)
	if err != nil || actual != want {
		return errors.New("the installed package owner differs from its running instance")
	}
	return nil
}

func waitPackageOwnerHealth(ctx context.Context, m Manifest) error {
	return waitPackageHealth(ctx, m, func(health core.Health) bool {
		_, err := packageOwnerCommit(m, health)
		return err == nil
	})
}

func waitPackageHealth(ctx context.Context, m Manifest, ownerHealthy func(core.Health) bool) error {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}}
	defer client.CloseIdleConnections()
	for {
		var out bytes.Buffer
		probe := []byte("GET /api/v1/health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
		if err := packageOwnerExchange(ctx, m, probe, &out, 64<<10); err == nil {
			if response, err := http.ReadResponse(bufio.NewReader(&out), nil); err == nil {
				var health core.Health
				err = json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&health)
				_ = response.Body.Close()
				if err == nil && response.StatusCode == http.StatusOK && ownerHealthy(health) {
					request, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+m.Listen+"/api/v1/health", nil)
					if e == nil {
						browser, e := client.Do(request)
						if e == nil {
							var public core.Health
							e = json.NewDecoder(io.LimitReader(browser.Body, 32<<10)).Decode(&public)
							_ = browser.Body.Close()
							if e == nil && browser.StatusCode == http.StatusOK && public.InstanceID == health.InstanceID && public.Status == "ok" && public.APIVersion == core.APIVersion {
								return nil
							}
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("the installed Local app is not ready; preserve it for recovery")
		case <-tick.C:
		}
	}
}
