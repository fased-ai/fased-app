//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

// InstallProtected is the semantic, registered helper entry. Its caller passes
// only a core already independently authenticated by bootstrap.PrepareMaintenance.
// These Go arguments are never decoded as file paths or commands from IPC.
func InstallProtected(ctx context.Context, owner int, opt Options, out io.Writer) (Manifest, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return Manifest{}, err
	}
	if a.Profile != "remote" {
		return Manifest{}, errors.New("Remote installation authority required")
	}
	o, err := protectedRemoteOps(a, out, nil)
	if err != nil {
		return Manifest{}, err
	}
	return install(ctx, opt, o)
}

// LoadProtected reads the same root-bound Remote manifest under the registered
// helper identity; it grants no lifecycle or application authority.
func LoadProtected(owner int) (Manifest, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil || a.Profile != "remote" {
		return Manifest{}, errors.New("protected Remote authority required")
	}
	o, err := protectedRemoteOps(a, io.Discard, nil)
	if err != nil {
		return Manifest{}, err
	}
	return load(o, owner)
}

func RunProtected(ctx context.Context, owner int, action string, peer *tailnet.Identity, out io.Writer) (json.RawMessage, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return nil, err
	}
	if a.Profile != "remote" {
		return nil, errors.New("Remote lifecycle authority required")
	}
	o, err := protectedRemoteOps(a, out, peer)
	if err != nil {
		return nil, err
	}
	m, err := load(o, owner)
	if err != nil {
		return nil, err
	}
	var payload any
	err = locked(ctx, o, m, func(o hostOps) error {
		switch action {
		case "remote-start":
			return start(ctx, o, m)
		case "remote-stop":
			if err := verify(ctx, o, m); err != nil {
				return err
			}
			_, err := o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", m.Unit)
			return err
		case "remote-status":
			v, err := status(ctx, o, m)
			payload = protectedRemoteStatus{Status: v, RemoteReady: v.RemoteReady}
			return err
		case "remote-enrollment-validate":
			if err := verifyEnrollmentFiles(o, m, true); err != nil {
				return err
			}
			if err := verifyAccount(ctx, o, m); err != nil {
				return err
			}
			if err := loaded(ctx, o, m); err != nil {
				return err
			}
			return enrollmentNetworkSettled(ctx, o, m)
		case "remote-enrollment-status":
			v, err := enrollmentStatus(ctx, o, m)
			payload = v
			return err
		case "remote-enrollment-prepared-status":
			if err := verifyFreshRemoteEnrollmentInventory(o, owner); err != nil {
				return err
			}
			if err := enrollmentNetworkSettled(ctx, o, m); err != nil {
				return err
			}
			v, err := status(ctx, o, m)
			if err == nil {
				err = verifyFreshRemoteJournals(o, m, filepath.Join(maintenance.OwnerRoot(owner), "remote"), v.InstanceID, 0)
			}
			payload = v
			return err
		case "remote-enrollment-suspend", "remote-enrollment-restore":
			step := "suspend"
			if action == "remote-enrollment-restore" {
				step = "restore"
			}
			running, autostart, err := enrollment.ProtectedRemoteIntent(owner, enrollment.ProtectedSource{Data: m.Data, Listen: m.Listen, PublicOrigin: m.PublicOrigin, UID: m.AppUID, GID: m.AppGID, ApplicationSequence: m.ApplicationSequence, ApplicationReleaseSHA256: m.ApplicationReleaseSHA256}, step)
			if err != nil {
				return err
			}
			if err = verify(ctx, o, m); err != nil {
				return err
			}
			if err = loaded(ctx, o, m); err != nil {
				return err
			}
			if step == "suspend" {
				_, err = o.run(ctx, nil, "/usr/bin/systemctl", "disable", "--now", "--", m.Unit)
				return err
			}
			verb := "disable"
			if autostart {
				verb = "enable"
			}
			if _, err = o.run(ctx, nil, "/usr/bin/systemctl", verb, "--", m.Unit); err != nil || !running {
				return err
			}
			_, err = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", m.Unit)
			return err
		case "remote-setup-link":
			var b bytes.Buffer
			if err := setupLink(ctx, o, m, &b); err != nil {
				return err
			}
			payload = b.String()
			return nil
		default:
			return errors.New("unsupported protected Remote lifecycle")
		}
	})
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	return json.Marshal(payload)
}

func protectedRemoteOps(a maintenance.Authority, out io.Writer, peer *tailnet.Identity) (hostOps, error) {
	o := liveOps(nil)
	o.uid, o.euid, o.gid = a.Owner, a.Owner, a.Group
	o.enrollmentJournal = func() ([]byte, error) { return enrollment.ProtectedOwnerJournal(a.Owner) }
	dir := filepath.Join(maintenance.OwnerRoot(a.Owner), "remote")
	if err := core.EnsurePrivateDirectory(dir); err != nil {
		return o, err
	}
	o.readJournal = func(name string) ([]byte, error) { return core.ReadPrivateFile(filepath.Join(dir, name)) }
	o.writeJournal = func(name string, v any) error { return core.WritePrivateJSON(filepath.Join(dir, name), v) }
	o.lock = func() (io.Closer, error) { return core.LockInstance(filepath.Join(dir, "install.lock")) }
	o.protectedPublish = maintenance.PublishProtected
	o.run = func(ctx context.Context, in []byte, args ...string) ([]byte, error) {
		return runProtectedRemoteOS(ctx, o, in, out, args...)
	}
	o.readinessRun = o.run
	o.applicationContract = func(ctx context.Context, m Manifest) ([]byte, error) {
		args := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(a.Owner), "--regid", strconv.Itoa(a.Group), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", m.Core, "version", "--json"}
		return o.run(ctx, nil, args...)
	}
	o.helper = func(ctx context.Context, args []string, writer io.Writer) error {
		var input []byte
		if peer != nil {
			input, _ = json.Marshal(peer)
		}
		b, err := o.run(ctx, input, args...)
		if err == nil {
			_, err = writer.Write(b)
		}
		return err
	}
	return o, nil
}

func runProtectedRemoteOS(ctx context.Context, o hostOps, in []byte, out io.Writer, args ...string) ([]byte, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 || len(args) == 0 {
		return nil, errors.New("protected Remote operation required")
	}
	switch args[0] {
	case "/usr/bin/install", "/usr/bin/mv", "/usr/bin/ln", "/usr/bin/rm", "/usr/bin/sync", "/usr/bin/systemctl", "/usr/bin/systemd-creds", "/usr/sbin/useradd":
	case "/usr/bin/passwd":
		if !fixedRemotePasswordProbe(o.uid, args) {
			return nil, errors.New("only the owned Remote account status is available")
		}
	case "/usr/bin/setpriv":
		// Every application child drops to a real non-root identity and loses
		// groups/capabilities before executing its root-protected image.
		if len(args) < 13 || args[1] != "--reuid" || args[3] != "--regid" || args[5] != "--clear-groups" || args[6] != "--no-new-privs" {
			return nil, errors.New("fixed non-root process identity required")
		}
		uid, e1 := strconv.Atoi(args[2])
		gid, e2 := strconv.Atoi(args[4])
		if e1 != nil || e2 != nil || uid <= 0 || gid <= 0 {
			return nil, errors.New("application processes cannot run as root")
		}
	default:
		return nil, errors.New("unknown protected Remote OS operation")
	}
	if err := trustedTool(o, args[0]); err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/nonexistent", "LANG=C"}
	c.Stdin = bytes.NewReader(in)
	c.Stderr = out
	var b protectedRemoteOutput
	c.Stdout = &b
	err := c.Run()
	return b.b, err
}

func fixedRemotePasswordProbe(owner int, args []string) bool {
	return owner > 0 && len(args) == 3 && args[0] == "/usr/bin/passwd" && args[1] == "-S" && args[2] == accountName(owner)
}

type protectedRemoteOutput struct{ b []byte }

func (b *protectedRemoteOutput) Write(p []byte) (int, error) {
	if len(b.b)+len(p) > 64<<10 {
		return 0, errors.New("oversized Remote helper response")
	}
	b.b = append(b.b, p...)
	return len(p), nil
}

type protectedRemoteStatus struct {
	Status      ServiceStatus `json:"status"`
	RemoteReady bool          `json:"remoteReady"`
}

func protectedRemoteCall(ctx context.Context, m Manifest, action string, diagnostics, out io.Writer, peer ...tailnet.Identity) (bool, error) {
	if m.OwnerUID != os.Getuid() {
		return true, errors.New("Remote installation owner required")
	}
	if _, err := maintenance.ReadAuthority(m.OwnerUID); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return true, err
	}
	rq := maintenance.Request{Version: 1, Action: action}
	if len(peer) > 1 {
		return true, errors.New("one verified Remote owner required")
	}
	if len(peer) == 1 {
		rq.Peer = &peer[0]
	}
	b, err := maintenance.Call(ctx, rq, diagnostics)
	if err != nil {
		return true, err
	}
	if out != nil {
		var r maintenance.Response
		var text string
		if update.StrictJSON(b, &r) != nil || json.Unmarshal(r.Payload, &text) != nil {
			return true, errors.New("invalid private setup response")
		}
		_, err = io.WriteString(out, text)
	}
	return true, err
}
