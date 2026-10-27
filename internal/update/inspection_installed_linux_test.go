//go:build linux

package update

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstalledInspectionFrameRefusal(t *testing.T) {
	base := newInspectionTest(t)
	in := base.in
	for _, name := range []string{"valid", "profile", "capability", "provision", "channel", "kind", "unknown", "duplicate", "oversized"} {
		t.Run(name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			frame := inspectionFrame{1, inspectionProfile, inspectionCapability, in.role.Provision, NewProbe(), "confirm", tj(map[string]string{"exchange": "x"})}
			channel := frame.Channel
			switch name {
			case "profile":
				frame.Profile = "production"
			case "capability":
				frame.Capability = "legacy"
			case "provision":
				frame.Provision = NewProbe()
			case "channel":
				frame.Channel = NewProbe()
			case "kind":
				frame.Kind = "inspect"
			}
			body := tj(frame)
			if name == "unknown" {
				body = append([]byte(`{"execute":true,`), body[1:]...)
			}
			if name == "duplicate" {
				body = append([]byte(`{"version":1,`), body[1:]...)
			}
			go func() {
				defer a.Close()
				if name == "oversized" {
					a.Write([]byte{0, 2, 0, 0})
					return
				}
				a.Write([]byte{byte(len(body) >> 24), byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))})
				a.Write(body)
			}()
			var result struct {
				Exchange string `json:"exchange"`
			}
			e := inspectionReadFrame(b, in, channel, "confirm", &result, time.Now().Add(time.Second))
			if (e == nil) != (name == "valid") {
				t.Fatal("strict frame", name, e)
			}
		})
	}
}
func TestInstalledInspectionGuardBeforeState(t *testing.T) {
	for _, root := range []string{t.TempDir(), "/tmp/foreign", "/var/lib/fased-app-custody-0", "/var/lib/fased-app-custody-01"} {
		if _, e := VerifyInstalledInspectionGuard(root, "owner"); e == nil {
			t.Fatal("foreign/no-fence startup")
		}
	}
}

// This opt-in generator is only compiled into the Go test executable. It holds
// disposable signing/TLS keys in memory and exports public fixture material.
func TestInstalledInspectionFixtureExport(t *testing.T) {
	dir := os.Getenv("FASED_INSTALLED_INSPECTION_EXPORT")
	if dir == "" {
		return
	}
	type config struct {
		Catalog inspectionCatalog `json:"catalog"`
		Core    CoreSnapshot      `json:"core"`
		Signer  SignerSnapshot    `json:"signer"`
	}
	var c config
	b, e := os.ReadFile(filepath.Join(dir, "export-config.json"))
	if e != nil || StrictJSON(b, &c) != nil {
		t.Fatal("export config", e)
	}
	f := newAdmissionFixture(t)
	f.seed.InstalledFloor = c.Signer.InstalledSequence
	f.seed.ReservedFloor = c.Signer.SequenceFloor + 1
	f.seed.PreserveSHA256 = SnapshotDigest("fixture-financial-before:v1", c)
	f.unit.Sequence = c.Signer.SequenceFloor + 2
	f.provenance.UnitSHA256 = digest(tj(f.unit))
	f.seal()
	blobs := map[string][]byte{}
	for _, n := range []string{"timestamp.json", formatVersion(f.sn.Signed.Version) + ".snapshot.json", formatVersion(f.tg.Signed.Version) + ".targets.json", "targets/fixture/" + digest(tj(TrustFixtureDescriptor{FixtureBinding: f.seed.FixtureBinding, Version: 1, Sequence: f.unit.Sequence, PreserveSHA256: f.seed.PreserveSHA256, EvidenceSHA256: digest(tj(f.evidence))})) + ".descriptor.json"} {
		blobs[filepath.Base(n)] = f.get(n)
	}
	blobs["evidence.json"] = tj(f.evidence)
	for _, s := range f.unit.Subjects {
		blobs[s.File], e = os.ReadFile(filepath.Join(f.dir, s.File))
		if e != nil {
			t.Fatal(e)
		}
	}
	index := tj(inspectionIndex{1, inspectionProfile, true, sortedInspectionFiles(blobs)})
	f.put("offline/index.json", index)
	for n, b := range blobs {
		f.put("offline/"+n, b)
	}
	all := map[string][]byte{"bundle/index.json": index}
	for n, b := range blobs {
		all["bundle/"+n] = b
	}
	cat := c.Catalog
	for i, role := range []string{"owner", "signer"} {
		slot := 0
		if i == 1 {
			slot = 2
		}
		d := inspectionRoleInput{Version: 1, Profile: inspectionProfile, FixtureOnly: true, Provision: cat.Provision, Installation: cat.Installation, ConfigSHA: cat.ConfigSHA, Role: role, UID: cat.UIDs[slot], GID: cat.GIDs[slot], SeedSHA: digest(tj(f.seed)), PolicySHA: digest(tj(f.policy)), FetchPin: handoffTransport(f.seed, f.ca), IndexSHA: digest(index), Epoch: NewProbe(), Reader: inspectionReaderV1()}
		all[role+".json"] = tj(d)
		cat.Roles[i] = digest(tj(d))
		all[role+"-seed.json"] = tj(f.seed)
		all[role+"-policy.json"] = tj(f.policy)
		all[role+"-clock.json"] = tj(inspectionClock{1, inspectionProfile, cat.Provision, role, d.Epoch, 1, f.now, true})
	}
	all["catalog.json"] = tj(cat)
	for n, b := range all {
		p := filepath.Join(dir, "inputs", n)
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(filepath.Join(dir, "fixture-ca.pem"), f.ca, 0644); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "export-ready"), []byte("public fixture ready"), 0600); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(100 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = os.Stat(filepath.Join(dir, "export-done")); e == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("bounded disposable exporter deadline")
}

func TestInstalledInspectionReviewDeadline(t *testing.T) {
	base := newInspectionTest(t)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	deadline := time.Now().Add(7 * time.Second)
	go func() {
		time.Sleep(5100 * time.Millisecond)
		_ = inspectionWrite(a, base.in, "", "status", struct {
			Nonce string `json:"nonce"`
		}{NewProbe()}, deadline)
	}()
	var body struct {
		Nonce string `json:"nonce"`
	}
	if e := inspectionReadFrame(b, base.in, "", "status", &body, deadline); e != nil {
		t.Fatal("protected review was cut off by frame-body timeout", e)
	}
}
