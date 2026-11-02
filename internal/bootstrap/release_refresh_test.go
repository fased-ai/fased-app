package bootstrap

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func refreshFixture(t *testing.T) (*firstFixture, ReleaseRefreshInput, Options) {
	t.Helper()
	f, o, input := finalizerFixture(t, SingleOwnerSigningProfile)
	input.Plan.Expires.Targets = f.now.Add(5 * 24 * time.Hour)
	v, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := v.Finalize(approveRelease(t, f, v))
	if err != nil {
		t.Fatal(err)
	}
	raw := jsonBytes(t, out.Publication)
	return f, ReleaseRefreshInput{Seed: o.Seed, SeedSHA256: o.SeedSHA256, Publication: raw, PublicationSHA256: digest(raw), now: func() time.Time { return f.now }}, o
}
func approveRefresh(t *testing.T, f *firstFixture, v *ReleaseRefresher) ReleaseApprovals {
	t.Helper()
	a := ReleaseApprovals{}
	for _, role := range []string{"snapshot", "timestamp"} {
		if role == "snapshot" && !v.Plan().RenewSnapshot {
			continue
		}
		r, err := v.Request(role, a)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := f.keys[role][0].PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := metadata.KeyFromPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		id, err := key.ID()
		if err != nil {
			t.Fatal(err)
		}
		sig, err := f.keys[role][0].SignMessage(bytes.NewReader(r.Payload))
		if err != nil {
			t.Fatal(err)
		}
		s := []metadata.Signature{{KeyID: id, Signature: sig}}
		if role == "snapshot" {
			a.Snapshot = s
		} else {
			a.Timestamp = s
		}
	}
	return a
}
func TestReleaseRefreshRoundTrip(t *testing.T) {
	f, in, _ := refreshFixture(t)
	// Expired online metadata is historical input; offline approval is still current.
	f.now = f.now.Add(2 * time.Hour)
	v, err := NewReleaseRefresher(in)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Plan().Due || !v.Plan().RenewSnapshot {
		t.Fatal("renewal missing")
	}
	plan := v.Plan()
	in.Plan = &plan
	resumed, err := NewReleaseRefresher(in)
	if err != nil || resumed.Plan() != plan {
		t.Fatal("plan drift", err)
	}
	out, err := resumed.Finalize(approveRefresh(t, f, resumed))
	if err != nil {
		t.Fatal(err)
	}
	var old ReleasePublication
	json.Unmarshal(in.Publication, &old)
	if !out.TargetsUnchanged || out.NoOp || out.Published || out.Activation || out.Publication.Descriptor != old.Descriptor || out.Plan.Versions.Targets != 1 || out.Plan.Versions.Snapshot != 2 || out.Plan.Versions.Timestamp != 2 {
		t.Fatal("authority widened")
	}
	for n, b := range old.Files {
		if n != "timestamp.json" && n != "1.snapshot.json" && !bytes.Equal(out.Publication.Files[n], b) {
			t.Fatal("immutable file changed", n)
		}
	}
	raw := jsonBytes(t, out.Publication)
	in.Publication = raw
	in.PublicationSHA256 = digest(raw)
	in.Plan = nil
	next, err := NewReleaseRefresher(in)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := next.Finalize(ReleaseApprovals{})
	if err != nil || !noop.NoOp || next.Plan().Due || !bytes.Equal(jsonBytes(t, noop.Publication), raw) {
		t.Fatal("not-due refresh was not exact no-op", err)
	}
	// A timestamp-only renewal never needs Snapshot/Targets keys.
	f.now = f.now.Add(13 * time.Hour)
	next, err = NewReleaseRefresher(in)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Plan().Due || next.Plan().RenewSnapshot {
		t.Fatal("expected timestamp-only renewal")
	}
	if _, err = next.Request("snapshot", ReleaseApprovals{}); err == nil {
		t.Fatal("unneeded Snapshot request")
	}
	if _, err = next.Finalize(approveRefresh(t, f, next)); err != nil {
		t.Fatal(err)
	}
}
func TestReleaseRefreshRefusals(t *testing.T) {
	f, in, _ := refreshFixture(t)
	mutate := func(change func(*ReleasePublication)) ReleaseRefreshInput {
		i := in
		var p ReleasePublication
		json.Unmarshal(in.Publication, &p)
		change(&p)
		i.Publication = jsonBytes(t, p)
		i.PublicationSHA256 = digest(i.Publication)
		return i
	}
	cases := map[string]ReleaseRefreshInput{
		"pin":          func() ReleaseRefreshInput { i := in; i.PublicationSHA256 = strings.Repeat("0", 64); return i }(),
		"descriptor":   mutate(func(p *ReleasePublication) { p.Descriptor.Sequence++ }),
		"release-id":   mutate(func(p *ReleasePublication) { p.Descriptor.GitHub.ReleaseID = "999" }),
		"unknown-file": mutate(func(p *ReleasePublication) { p.Files["extra"] = []byte("x") }),
		"targets": mutate(func(p *ReleasePublication) {
			p.Files["1.targets.json"] = bytes.Replace(p.Files["1.targets.json"], []byte(`"version":1`), []byte(`"version":2`), 1)
		}),
		"signature": mutate(func(p *ReleasePublication) {
			var m map[string]any
			json.Unmarshal(p.Files["timestamp.json"], &m)
			m["signatures"] = []any{}
			p.Files["timestamp.json"] = jsonBytes(t, m)
		}),
	}
	for name, i := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewReleaseRefresher(i); err == nil {
				t.Fatal("changed authority admitted")
			}
		})
	}
	v, err := NewReleaseRefresher(in)
	if err != nil {
		t.Fatal(err)
	}
	a := approveRefresh(t, f, v)
	for _, role := range []string{"targets", "root", "unknown"} {
		if _, err = v.Request(role, a); err == nil {
			t.Fatal("offline request", role)
		}
	}
	bad := a
	bad.Targets = a.Snapshot
	if _, err = v.Finalize(bad); err == nil {
		t.Fatal("offline approval accepted")
	}
	bad = a
	bad.Timestamp = a.Snapshot
	if _, err = v.Finalize(bad); err == nil {
		t.Fatal("wrong role accepted")
	}
	bad = a
	bad.Timestamp = append(append([]metadata.Signature{}, a.Timestamp...), a.Timestamp...)
	if _, err = v.Finalize(bad); err == nil {
		t.Fatal("duplicate accepted")
	}
	plan := v.Plan()
	plan.Versions.Timestamp++
	in.Plan = &plan
	if _, err = NewReleaseRefresher(in); err == nil {
		t.Fatal("changed plan accepted")
	}
	in.Plan = nil
	f.now = f.now.Add(121 * time.Hour)
	if _, err = NewReleaseRefresher(in); err == nil {
		t.Fatal("expired offline authorization renewed")
	}
	if _, err = v.Finalize(a); err == nil {
		t.Fatal("frozen plan outlived authorization")
	}
}

// Actual compiled Go verifier ↔ Node online signer. Only disposable online keys
// cross this fixture boundary; no Root/Targets private key is written.
func TestReleaseRefreshCompiledOnlineSigner(t *testing.T) {
	f, in, o := refreshFixture(t)
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(f.parent, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seed := write("seed.json", in.Seed)
	pub := write("publication.json", in.Publication)
	keys := map[string]string{}
	for role, key := range f.onlineFixtureKeys {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		keys[role] = write(role+".pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	}
	binary := filepath.Join(f.parent, "verifier")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/fased-release-candidate")
	cmd.Dir = "../.."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(b), err)
	}
	if err := os.Chmod(binary, 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/refresh-ubuntu-metadata.mjs")
	if err != nil {
		t.Fatal(err)
	}
	spec := write("bridge.json", jsonBytes(t, map[string]any{"seedFile": seed, "publicationFile": pub, "seedSHA256": in.SeedSHA256, "publicationSHA256": in.PublicationSHA256, "verifierFile": binary, "verifierSHA256": digest(bin), "parent": f.parent, "keys": keys}))
	output := filepath.Join(f.parent, "renewed.json")
	js := `import {readFileSync,writeFileSync,readdirSync} from "node:fs";
import {pathToFileURL} from "node:url";
const {refreshVerifier,onlineMetadataSigner}=await import(pathToFileURL(process.argv[1]));
const {publicationInput}=await import(new URL("publish-ubuntu-release.mjs",pathToFileURL(process.argv[1])));
const {createHash}=await import("node:crypto");
const s=JSON.parse(readFileSync(process.argv[2]));
const seed=readFileSync(s.seedFile),publication=readFileSync(s.publicationFile);
const verify=refreshVerifier(s.verifierFile,s.verifierSHA256,s.parent),sign=onlineMetadataSigner(seed,s.seedSHA256,s.keys);
const input={seed,publication,seedSHA256:s.seedSHA256,publicationSHA256:s.publicationSHA256};
const plan=await verify("plan",input);if(!plan.due)throw Error("expected due plan");
const approvals={};for(const role of [...(plan.renewSnapshot?["snapshot"]:[]),"timestamp"]){approvals[role]=await sign(await verify(role,{...input,plan,approvals}));}
const out=await verify("finalize",{...input,plan,approvals});
const raw=Buffer.from(JSON.stringify(out.publication));publicationInput(raw,{sha256:createHash("sha256").update(raw).digest("hex"),previousCommit:"1".repeat(40)});
const renewed={...input,publication:raw,publicationSHA256:createHash("sha256").update(raw).digest("hex")};
const quiet=await verify("plan",renewed);if(quiet.due)throw Error("unexpected immediate renewal");
const noop=await verify("finalize",{...renewed,plan:quiet,approvals:{}});if(!noop.noOp || !noop.targetsUnchanged)throw Error("no-op failed");
if(readdirSync(s.parent).some(n=>n.startsWith(".verify-")))throw Error("verifier scratch leaked");
writeFileSync(process.argv[3],JSON.stringify(out),{mode:0o600,flag:"wx"});`
	cmd = exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", js, script, spec, output)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(b), err)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var out ReleaseRefreshResult
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.TargetsUnchanged || out.NoOp || out.Activation || out.Published {
		t.Fatal("wrong authority")
	}
	f.files = map[string][]byte{}
	for name, b := range out.Publication.Files {
		f.files["/"+name] = b
	}
	// Fresh inspection uses the same current time as the compiled verifier,
	// never an earlier fixture clock which would exceed its strict 48h window.
	f.now = time.Now().UTC().Truncate(time.Second)
	// Production bootstrap/update admission remains unchanged.
	o.Directory = filepath.Join(f.parent, "refreshed-inspection")
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	release, err := p.ApplicationRelease()
	if err != nil || release.Sequence != f.descriptor.Sequence {
		t.Fatal("fresh release inspection", err)
	}
	for _, flag := range []string{"--private-key", "--clock", "--tls-ca-file"} {
		cmd = exec.CommandContext(t.Context(), binary, "refresh-plan", "--refresh-file", spec, flag, "forbidden")
		if b, err := cmd.Output(); err == nil || len(b) != 0 {
			t.Fatal("unsupported refresh override", flag)
		}
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("fixture application ran")
	}
}

func TestReleaseRefreshClippedExpiry(t *testing.T) {
	f, _, input := finalizerFixture(t, SingleOwnerSigningProfile)
	end := f.now.Add(36 * time.Hour)
	input.Plan.Expires = ReleaseExpiries{Targets: end, Snapshot: end, Timestamp: end}
	v, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := v.Finalize(approveRelease(t, f, v))
	if err != nil {
		t.Fatal(err)
	}
	raw := jsonBytes(t, out.Publication)
	r, err := NewReleaseRefresher(ReleaseRefreshInput{Seed: input.Seed, SeedSHA256: input.SeedSHA256, Publication: raw, PublicationSHA256: digest(raw), now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan().Due || !r.Plan().OfflineRenewalNeeded {
		t.Fatal("clipped expiry caused repeated online renewal")
	}
	if _, err = r.Finalize(ReleaseApprovals{}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(-time.Second)
	if _, err = r.Finalize(ReleaseApprovals{}); err == nil {
		t.Fatal("clock rollback accepted")
	}
}

func TestReleaseRefreshLargePublication(t *testing.T) {
	f, _, input := finalizerFixture(t, SingleOwnerSigningProfile)
	input.Provenance = append(input.Provenance, bytes.Repeat([]byte(" "), 60*1024-len(input.Provenance))...)
	input.Plan.Descriptor.Release.Provenance.Size = int64(len(input.Provenance))
	input.Plan.Descriptor.Release.Provenance.SHA256 = digest(input.Provenance)
	v, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := v.Finalize(approveRelease(t, f, v))
	if err != nil {
		t.Fatal(err)
	}
	raw := jsonBytes(t, out.Publication)
	if len(raw) <= 64*1024 || len(raw) > 1<<20 {
		t.Fatal("fixture does not exercise public manifest bound")
	}
	r, err := NewReleaseRefresher(ReleaseRefreshInput{Seed: input.Seed, SeedSHA256: input.SeedSHA256, Publication: raw, PublicationSHA256: digest(raw), now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Finalize(approveRefresh(t, f, r)); err != nil {
		t.Fatal(err)
	}
}
