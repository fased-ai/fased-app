package update

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTrustRehearsalCompiledCLI(t *testing.T) {
	core := os.Getenv("FASED_TRUST_CORE")
	if core == "" {
		t.Skip("explicit compiled fixture binary required")
	}
	if os.Geteuid() == 0 {
		t.Fatal("never run project code as root")
	}
	core, e := filepath.Abs(core)
	if e != nil {
		t.Fatal(e)
	}
	root := os.Getenv("FASED_TRUST_EVIDENCE")
	if root == "" {
		t.Fatal("persistent evidence root required")
	}
	root, e = filepath.Abs(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(root, "cli-fixtures"), 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(filepath.Join(root, "cli-fixtures"), "trust-")
	if e != nil {
		t.Fatal(e)
	}
	f := newTrustFixture(t)
	checks := map[string]bool{}
	receipt := map[string]any{"result": "FAIL", "class": "ordinary-user compiled read-only CLI; disposable keys and controlled HTTPS only", "fixtureOnly": true, "checks": checks}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
		if !t.Failed() {
			receipt["result"] = "PASS"
		}
		receipt["generatedInputsRemoved"] = true
		receipt["privateKeys"] = "memory only"
		b, _ := json.MarshalIndent(receipt, "", "  ")
		if e := os.WriteFile(filepath.Join(root, "trust-cli-result.json"), append(b, '\n'), 0600); e != nil {
			t.Error(e)
		}
	})
	pass := func(name string, b bool) {
		t.Helper()
		if !b {
			t.Fatal(name)
		}
		checks[name] = true
	}
	seedPath, statePath, caPath := filepath.Join(dir, "seed.json"), filepath.Join(dir, "state.json"), filepath.Join(dir, "ca.pem")
	write := func(p string, b []byte) {
		t.Helper()
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	seed := tj(f.seed)
	receipt["seedSHA256"] = digest(seed)
	receipt["initialRootSHA256"] = digest(f.seed.Root)
	receipt["initialFixtureClock"] = f.now
	write(seedPath, seed)
	write(caPath, f.ca)
	for _, n := range []string{"originals", "counters", "unknown-holds"} {
		write(filepath.Join(dir, n), []byte("unopened "+n))
	}
	call := func(args []string, success bool) TrustRehearsalReport {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, core, args...)
		out, e := cmd.Output()
		if success && e != nil {
			if x, ok := e.(*exec.ExitError); ok {
				t.Fatalf("CLI refused valid fixture: %s", x.Stderr)
			}
			t.Fatal(e)
		}
		if !success {
			if e == nil || len(out) != 0 {
				t.Fatal("unsafe CLI accepted inputs")
			}
			return TrustRehearsalReport{}
		}
		var r TrustRehearsalReport
		if e = json.Unmarshal(out, &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	args := func(initial bool) []string {
		a := []string{"updates", "trust-inspect", "--development", "--seed-file", seedPath, "--seed-sha256", digest(seed), "--fixture-time", strconv.FormatInt(f.now, 10), "--tls-ca-file", caPath}
		if initial {
			return append(a, "--initial")
		}
		b, e := os.ReadFile(statePath)
		if e != nil {
			t.Fatal(e)
		}
		return append(a, "--state-file", statePath, "--state-sha256", digest(b))
	}
	r := call(args(true), true)
	pass("independentlyPinnedSeedAccepted", r.MetadataVerified && r.DescriptorVerified && r.FixtureOnly && r.Sequence == 10)
	pass("readOnlyAndNoFinancialOrInstallAuthority", r.ReadOnly && !r.InstallAllowed && !r.ResumeAllowed && !r.UpdaterEnabled && !r.WalletSigner && !r.FinancialExecution)
	write(statePath, r.NextState)
	beforeState := digest(r.NextState)
	f.now++
	repeated := call(args(false), true)
	pass("restartReobservesSameRelease", repeated.Sequence == 10 && repeated.RootVersion == 1)
	untouched, readErr := os.ReadFile(statePath)
	pass("initialSnapshotReadOnly", readErr == nil && digest(untouched) == beforeState)
	a := args(false)
	a = append(a, "--commit")
	call(a, false)
	pass("noCommitCLIExists", true)
	a = args(false)
	for i, s := range a {
		if s == "--state-sha256" {
			a[i+1] = digest([]byte("substitution"))
		}
	}
	call(a, false)
	pass("staleSnapshotAnchorRefused", true)
	a = args(false)
	for i, s := range a {
		if s == "--fixture-time" {
			a[i+1] = strconv.FormatInt(f.now-2, 10)
		}
	}
	call(a, false)
	pass("backwardFixtureClockRefused", true)
	a = args(false)
	for i, s := range a {
		if s == "--development" {
			a = append(a[:i], a[i+1:]...)
			break
		}
	}
	call(a, false)
	pass("explicitDevelopmentRequired", true)
	a = args(false)
	for i, s := range a {
		if s == "--tls-ca-file" {
			a = append(a[:i], a[i+2:]...)
			break
		}
	}
	call(a, false)
	pass("untrustedTLSRefused", true)
	old := f.rotate("root")
	f.root.Signed.Version = 2
	f.put("2.root.json", f.rootSign(old[:2]...))
	f.release(2, 11)
	r = call(args(false), true)
	pass("dualRootQuorumAccepted", r.RootVersion == 2 && r.Sequence == 11)
	write(statePath, r.NextState)
	rootTwoSnapshotSHA := digest(r.NextState)
	f.rotate("timestamp")
	f.rotate("snapshot")
	f.root.Signed.Version = 3
	f.put("3.root.json", f.rootSign())
	f.release(1, 11)
	r = call(args(false), true)
	var snapshot TrustFixtureSnapshot
	if e = StrictJSON(r.NextState, &snapshot); e != nil {
		t.Fatal(e)
	}
	pass("onlineCacheResetPreservesReleaseAndFinancialFixtures", r.OnlineCacheReset && snapshot.ReleaseFloor == 11 && snapshot.ReservedFloor == 4 && snapshot.InstalledFloor == 3 && snapshot.PreserveSHA256 == f.seed.PreserveSHA256)
	data, e := os.ReadFile(seedPath)
	if e != nil {
		t.Fatal(e)
	}
	pass("seedUnchanged", digest(data) == digest(seed))
	data, e = os.ReadFile(statePath)
	if e != nil {
		t.Fatal(e)
	}
	pass("CLILeavesSnapshotUnchanged", digest(data) == rootTwoSnapshotSHA)
	// The runner alone advances its fixture snapshot. A later CLI failure must
	// report newer trusted progress without writing input or approving a release.
	write(statePath, r.NextState)
	beforeProgress := digest(r.NextState)
	oldTargetKeys := f.rotate("targets")
	f.root.Signed.Version = 4
	f.put("4.root.json", f.rootSign())
	f.keys["targets"] = oldTargetKeys
	f.release(2, 12)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, core, args(false)...)
	blocked, blockedErr := cmd.Output()
	var progress TrustRehearsalReport
	if e = json.Unmarshal(blocked, &progress); e != nil {
		t.Fatal(e)
	}
	pass("failedCandidateReturnsVerifiedTrustProgress", blockedErr != nil && progress.TrustProgressOnly && progress.RootVersion == 4 && !progress.MetadataVerified && !progress.DescriptorVerified)
	data, e = os.ReadFile(statePath)
	pass("blockedProgressRemainsReadOnly", e == nil && digest(data) == beforeProgress)
	pass("blockedProgressCannotInstallOrResume", !progress.InstallAllowed && !progress.ResumeAllowed && !progress.UpdaterEnabled && !progress.WalletSigner && !progress.FinancialExecution)
	for _, n := range []string{"originals", "counters", "unknown-holds"} {
		data, e := os.ReadFile(filepath.Join(dir, n))
		pass("preserved-"+n, e == nil && string(data) == "unopened "+n)
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	pass("noInstanceOrSignerStateCreated", len(entries) == 6)
	bin, e := os.ReadFile(core)
	if e != nil {
		t.Fatal(e)
	}
	receipt["binarySHA256"] = digest(bin)
}
