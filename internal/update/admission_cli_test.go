package update

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestReleaseAdmissionCompiledCLI(t *testing.T) {
	core := os.Getenv("FASED_ADMISSION_CORE")
	if core == "" {
		t.Skip("explicit compiled fixture binary required")
	}
	root := os.Getenv("FASED_ADMISSION_EVIDENCE")
	if os.Geteuid() == 0 || !filepath.IsAbs(core) || !filepath.IsAbs(root) {
		t.Fatal("ordinary user and absolute compiled binary/evidence paths required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(root, "cli-fixtures-")
	if e != nil {
		t.Fatal(e)
	}
	f := newAdmissionFixture(t)
	checks := map[string]bool{}
	receipt := map[string]any{"result": "FAIL", "class": "ordinary-user compiled read-only release admission; disposable fixture signatures and inert artifacts", "fixtureOnly": true, "checks": checks}
	t.Cleanup(func() {
		e := os.RemoveAll(dir)
		if e != nil {
			t.Error(e)
		}
		receipt["generatedInputsRemoved"] = e == nil
		receipt["privateKeys"] = "memory only; no production identities or credentials"
		if !t.Failed() {
			receipt["result"] = "PASS"
		}
		b, _ := json.MarshalIndent(receipt, "", "  ")
		if e := os.WriteFile(filepath.Join(root, "admission-cli-result.json"), append(b, '\n'), 0600); e != nil {
			t.Error(e)
		}
	})
	pass := func(name string, ok bool) {
		t.Helper()
		if !ok {
			t.Fatal(name)
		}
		checks[name] = true
	}
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	artifactDir := filepath.Join(dir, "artifacts")
	if e := os.Mkdir(artifactDir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, a := range f.unit.Subjects {
		b, e := os.ReadFile(filepath.Join(f.dir, a.File))
		if e != nil {
			t.Fatal(e)
		}
		write(filepath.Join("artifacts", a.File), b)
	}
	f.dir = artifactDir
	seed := tj(f.seed)
	seedPath := write("seed.json", seed)
	policyPath := write("policy.json", tj(f.policy))
	evidencePath := write("evidence.json", tj(f.evidence))
	caPath := write("ca.pem", f.ca)
	statePath := filepath.Join(dir, "state.json")
	for _, name := range []string{"originals", "counters", "unknown-holds"} {
		write(name, []byte("unopened fixture "+name))
	}
	snapshot := func() map[string]string {
		t.Helper()
		m := map[string]string{}
		for _, d := range []string{dir, artifactDir} {
			entries, e := os.ReadDir(d)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				p := filepath.Join(d, entry.Name())
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				st, e := os.Stat(p)
				if e != nil {
					t.Fatal(e)
				}
				m[p] = digest(b) + ":" + st.Mode().String()
			}
		}
		return m
	}
	args := func(initial bool) []string {
		a := []string{"updates", "release-inspect", "--development", "--seed-file", seedPath, "--seed-sha256", digest(seed), "--fixture-time", strconv.FormatInt(f.now, 10), "--tls-ca-file", caPath, "--policy-file", policyPath, "--policy-sha256", digest(tj(f.policy)), "--evidence-file", evidencePath, "--artifact-dir", artifactDir}
		if initial {
			return append(a, "--initial")
		}
		b, e := os.ReadFile(statePath)
		if e != nil {
			t.Fatal(e)
		}
		return append(a, "--state-file", statePath, "--state-sha256", digest(b))
	}
	call := func(a []string, success, reportExpected bool) ReleaseAdmissionReport {
		t.Helper()
		before := snapshot()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, core, a...)
		out, e := cmd.Output()
		if success != (e == nil) {
			if x, ok := e.(*exec.ExitError); ok {
				t.Fatalf("unexpected CLI status: %s", x.Stderr)
			}
			t.Fatal("unexpected CLI status", e)
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("read-only CLI changed fixture inputs")
		}
		if !reportExpected {
			if len(out) != 0 {
				t.Fatal("invalid entrypoint produced report")
			}
			return ReleaseAdmissionReport{}
		}
		var r ReleaseAdmissionReport
		if e := json.Unmarshal(out, &r); e != nil {
			t.Fatal(e)
		}
		if !r.FixtureOnly || !r.ReadOnly || !admissionDisabled(r) {
			t.Fatal("CLI gained execution authority", r)
		}
		if !success && (!r.TrustProgressOnly || r.ReleaseUnitVerified || r.ProvenanceVerified || r.ArtifactsVerified || r.MetadataVerified || r.DescriptorVerified) {
			t.Fatal("failed admission exposed candidate authority", r)
		}
		return r
	}
	base, e := os.ReadFile(core)
	if e != nil {
		t.Fatal(e)
	}
	receipt["binarySHA256"] = digest(base)
	receipt["seedSHA256"], receipt["policySHA256"], receipt["evidenceSHA256"] = digest(seed), digest(tj(f.policy)), digest(tj(f.evidence))
	receipt["unitSHA256"], receipt["fixtureClock"] = digest(tj(f.unit)), f.now
	r := call(args(true), true, true)
	pass("completePairHelperInstallerAndProvenanceAdmitted", r.ReleaseUnitVerified && r.ProvenanceVerified && r.ArtifactsVerified && r.ArtifactCount == 4 && r.Sequence == 10)
	pass("readOnlyAndEveryExecutionCapabilityDisabled", admissionDisabled(r) && r.ReadOnly)
	write("state.json", r.NextState)
	f.now++
	r = call(args(false), true, true)
	pass("restartReverifiesIdenticalUnitWithoutWritingState", r.ReleaseUnitVerified && r.Sequence == 10)
	for _, flag := range []string{"--commit", "--apply", "--automatic", "--production", "--insecure", "--request-stdin"} {
		call(append(args(true), flag), false, false)
		pass("refusesAuthorityFlag:"+flag, true)
	}
	for _, flag := range []string{"--development", "--policy-file", "--policy-sha256", "--evidence-file", "--artifact-dir", "--fixture-time"} {
		a := args(true)
		for i, s := range a {
			if s == flag {
				n := 2
				if flag == "--development" {
					n = 1
				}
				a = append(a[:i], a[i+n:]...)
				break
			}
		}
		call(a, false, false)
		pass("requiresExplicitInput:"+flag, true)
	}
	a := args(true)
	for i, s := range a {
		if s == "--policy-sha256" {
			a[i+1] = digest([]byte("other policy"))
		}
	}
	call(a, false, false)
	pass("independentPolicyPinRefusesSubstitution", true)
	a = args(true)
	for i, s := range a {
		if s == "--tls-ca-file" {
			a = append(a[:i], a[i+2:]...)
			break
		}
	}
	call(a, false, false)
	pass("untrustedFixtureTLSRefused", true)
	write("evidence.json", append(tj(f.evidence), ' '))
	call(args(true), false, true)
	pass("exactTUFBoundEvidenceBytesRequired", true)
	write("evidence.json", tj(f.evidence))
	helper := filepath.Join(artifactDir, "owner-helper.fixture")
	hb, _ := os.ReadFile(helper)
	if e := os.Remove(helper); e != nil {
		t.Fatal(e)
	}
	call(args(true), false, true)
	pass("omittedOwnerHelperRefused", true)
	write("artifacts/owner-helper.fixture", hb)
	corePath := filepath.Join(artifactDir, "core.fixture")
	cb, _ := os.ReadFile(corePath)
	changed := append([]byte(nil), cb...)
	changed[len(changed)-1] ^= 1
	write("artifacts/core.fixture", changed)
	call(args(true), false, true)
	pass("changedArtifactBytesRefused", true)
	write("artifacts/core.fixture", cb)
	if e := os.Chmod(corePath, 0700); e != nil {
		t.Fatal(e)
	}
	call(args(true), false, true)
	pass("executableArtifactRefused", true)
	if e := os.Chmod(corePath, 0600); e != nil {
		t.Fatal(e)
	}
	// Authenticate a new descriptor but replace the workflow assertion under a
	// valid disposable builder signature. The independent policy must refuse.
	f.provenance.Identity.WorkflowRevision = "4444444444444444444444444444444444444444"
	f.seal()
	write("evidence.json", tj(f.evidence))
	call(args(true), false, true)
	pass("signedWorkflowSubstitutionRefused", true)
	f.provenance.Identity = f.policy.Identity
	f.seal()
	write("evidence.json", tj(f.evidence))
	// Root succession followed by artifact failure returns progress plus failure;
	// manually saving the returned fixture snapshot models the external controller.
	old := f.rotate("root")
	f.root.Signed.Version = 2
	f.put("2.root.json", f.rootSign(old[:2]...))
	f.release(2, 11)
	f.unit.Sequence = 11
	f.provenance.UnitSHA256 = digest(tj(f.unit))
	f.seal()
	write("evidence.json", tj(f.evidence))
	if e := os.Remove(helper); e != nil {
		t.Fatal(e)
	}
	r = call(args(false), false, true)
	pass("failedAdmissionRetainsRootAndObservedReleaseProgress", r.RootVersion == 2 && r.Sequence == 11 && r.TrustProgressOnly)
	var next TrustFixtureSnapshot
	if e := json.Unmarshal(r.NextState, &next); e != nil {
		t.Fatal(e)
	}
	pass("installedReservedFloorsAndOpaqueAnchorPreserved", next.InstalledFloor == f.seed.InstalledFloor && next.ReservedFloor == f.seed.ReservedFloor && next.PreserveSHA256 == f.seed.PreserveSHA256)
	write("state.json", r.NextState)
	write("artifacts/owner-helper.fixture", hb)
	f.mu.Lock()
	delete(f.files, "2.root.json")
	f.mu.Unlock()
	r = call(args(false), true, true)
	pass("retryUsesRetainedRootAndCompletesSameUnit", r.RootVersion == 2 && r.ReleaseUnitVerified && r.Sequence == 11)
	for _, name := range []string{"originals", "counters", "unknown-holds"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		pass("preserves:"+name, e == nil && string(b) == "unopened fixture "+name)
	}
	entries, e := os.ReadDir(artifactDir)
	inert := e == nil && len(entries) == 4
	for _, entry := range entries {
		st, e := entry.Info()
		inert = inert && e == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 == 0
	}
	pass("inertArtifactDirectoryStillFourNonexecutableFiles", inert)
}
