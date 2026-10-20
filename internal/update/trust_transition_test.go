package update

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func TestTrustRehearsalRefreshAndFloors(t *testing.T) {
	f := newTrustFixture(t)
	first := f.run(nil)
	view := first.Report()
	if !view.FixtureOnly || !view.ReadOnly || !view.MetadataVerified || !view.DescriptorVerified || view.InstallAllowed || view.ResumeAllowed || view.UpdaterEnabled || view.WalletSigner || view.FinancialExecution || view.Sequence != 10 {
		t.Fatal("unexpected authority", view)
	}
	f.now++
	repeat := f.run(&first)
	f.release(2, 11)
	f.now++
	next := f.run(&repeat)
	var s TrustFixtureSnapshot
	if StrictJSON(next.next, &s) != nil || s.ReleaseFloor != 11 || s.InstalledFloor != 3 || s.ReservedFloor != 4 || s.PreserveSHA256 != f.seed.PreserveSHA256 {
		t.Fatal("fixture financial anchors changed")
	}
	copy := next.Report()
	copy.NextState[0] = 'x'
	if digest(next.next) != next.report.NextSHA256 {
		t.Fatal("view mutated opaque transition")
	}
}

func TestTrustRehearsalRejectsUntrustedTransitions(t *testing.T) {
	for _, name := range []string{"seed-substitution", "missing-state", "state-substitution", "backward-clock", "production-seed", "wrong-repository", "wrong-purpose", "one-root-key", "root-threshold-downgrade", "duplicate-root-signature", "one-target-key", "duplicate-target-signature", "wrong-role-signature", "timestamp-expired", "snapshot-expired", "targets-expired", "final-root-expired", "metadata-mix", "unsigned-descriptor-change", "metadata-rollback", "same-version-change", "lower-release", "same-sequence-change", "unknown-target", "oversize", "initial-with-state", "untrusted-tls", "metadata-redirect", "root-missing-old-quorum", "root-missing-new-quorum", "root-skipped-version", "revoked-target-key", "reset-cannot-lower-release"} {
		t.Run(name, func(t *testing.T) {
			f := newTrustFixture(t)
			first := f.run(nil)
			o := f.options(&first)
			switch name {
			case "seed-substitution":
				o.SeedSHA256 = strings.Repeat("0", 64)
			case "missing-state":
				o.Previous = nil
			case "state-substitution":
				o.Previous[0] = 'x'
			case "backward-clock":
				o.Now--
			case "production-seed":
				f.seed.Channel = "stable"
				o = f.options(&first)
			case "wrong-repository":
				f.seed.Repository = NewProbe()
				o = f.options(nil)
			case "wrong-purpose":
				f.seed.Purpose = "production"
				o = f.options(nil)
			case "one-root-key":
				f.root.Signatures = f.root.Signatures[:1]
				f.seed.Root = f.bytes(f.root)
				o = f.options(nil)
			case "root-threshold-downgrade":
				f.root.Signed.Roles["root"].Threshold = 1
				f.seed.Root = f.rootSign()
				o = f.options(nil)
			case "duplicate-root-signature":
				f.root.Signatures[1] = f.root.Signatures[0]
				f.seed.Root = f.bytes(f.root)
				o = f.options(nil)
			case "one-target-key":
				f.tg.Signatures = f.tg.Signatures[:1]
				f.publishTargets(f.bytes(f.tg))
			case "duplicate-target-signature":
				f.tg.Signatures[1] = f.tg.Signatures[0]
				f.publishTargets(f.bytes(f.tg))
			case "wrong-role-signature":
				f.tg.ClearSignatures()
				_, _ = f.tg.Sign(f.keys["root"][0])
				_, _ = f.tg.Sign(f.keys["root"][1])
				f.publishTargets(f.bytes(f.tg))
			case "timestamp-expired":
				f.ts.Signed.Expires = time.Unix(f.now, 0)
				f.ts.ClearSignatures()
				_, _ = f.ts.Sign(f.keys["timestamp"][0])
				f.put("timestamp.json", f.bytes(f.ts))
			case "snapshot-expired":
				f.sn.Signed.Expires = time.Unix(f.now, 0)
				f.publishTargets(f.bytes(f.tg))
			case "targets-expired":
				f.tg.Signed.Expires = time.Unix(f.now, 0)
				f.tg.ClearSignatures()
				_, _ = f.tg.Sign(f.keys["targets"][0])
				_, _ = f.tg.Sign(f.keys["targets"][1])
				f.publishTargets(f.bytes(f.tg))
			case "final-root-expired":
				f.root.Signed.Version = 2
				f.root.Signed.Expires = time.Unix(f.now, 0)
				f.put("2.root.json", f.rootSign())
			case "metadata-mix":
				f.put("1.snapshot.json", f.get("1.targets.json"))
			case "unsigned-descriptor-change":
				for name, b := range f.files {
					if strings.HasSuffix(name, ".descriptor.json") {
						f.put(name, append(b, ' '))
					}
				}
			case "metadata-rollback":
				f.release(2, 10)
				prev := f.run(nil)
				f.release(1, 11)
				o = f.options(&prev)
			case "same-version-change":
				f.put("timestamp.json", append(f.get("timestamp.json"), ' '))
			case "lower-release":
				f.release(2, 9)
			case "same-sequence-change":
				f.release(2, 10)
				var d TrustFixtureDescriptor
				if e := StrictJSON(firstDescriptor(first), &d); e != nil {
					t.Fatal(e)
				}
				d.EvidenceSHA256 = digest([]byte("changed evidence"))
				db := tj(d)
				tf, e := metadata.TargetFile().FromBytes(fixtureTarget, db, "sha256")
				if e != nil {
					t.Fatal(e)
				}
				f.tg.Signed.Targets[fixtureTarget] = tf
				f.tg.ClearSignatures()
				_, _ = f.tg.Sign(f.keys["targets"][0])
				_, _ = f.tg.Sign(f.keys["targets"][1])
				f.put("targets/fixture/"+digest(db)+".descriptor.json", db)
				f.publishTargets(f.bytes(f.tg))
			case "unknown-target":
				f.tg.Signed.Targets["other"] = f.tg.Signed.Targets[fixtureTarget]
				delete(f.tg.Signed.Targets, fixtureTarget)
				f.tg.ClearSignatures()
				_, _ = f.tg.Sign(f.keys["targets"][0])
				_, _ = f.tg.Sign(f.keys["targets"][1])
				f.publishTargets(f.bytes(f.tg))
			case "oversize":
				f.put("timestamp.json", []byte(strings.Repeat(" ", maxTrustMetadata+1)))
			case "initial-with-state":
				o.Initial = true
			case "untrusted-tls":
				o.TLSCA = nil
			case "metadata-redirect":
				f.mu.Lock()
				f.redirects["timestamp.json"] = "https://untrusted.invalid/"
				f.mu.Unlock()
			case "root-missing-old-quorum", "root-missing-new-quorum", "root-skipped-version":
				old := f.rotate("root")
				f.root.Signed.Version = 2
				if name == "root-skipped-version" {
					f.root.Signed.Version = 3
				}
				b := f.rootSign(old[:2]...)
				if name == "root-missing-old-quorum" {
					b = f.rootSign()
				}
				if name == "root-missing-new-quorum" {
					f.root.ClearSignatures()
					_, _ = f.root.Sign(old[0])
					_, _ = f.root.Sign(old[1])
					b = f.bytes(f.root)
				}
				f.put("2.root.json", b)
				f.release(2, 11)
			case "revoked-target-key":
				old := f.rotate("targets")
				f.root.Signed.Version = 2
				f.put("2.root.json", f.rootSign())
				f.keys["targets"] = old
				f.release(2, 11)
			case "reset-cannot-lower-release":
				f.rotate("timestamp")
				f.rotate("snapshot")
				f.root.Signed.Version = 2
				f.put("2.root.json", f.rootSign())
				f.release(1, 9)
			}
			if r, e := RehearseTrust(context.Background(), o); e == nil || r.Report().MetadataVerified {
				t.Fatal("unsafe transition accepted", name, e)
			}
		})
	}
}

func firstDescriptor(t TrustTransition) []byte {
	var s TrustFixtureSnapshot
	_ = StrictJSON(t.next, &s)
	return s.Descriptor
}

func TestTrustRehearsalRootAndOnlineRotation(t *testing.T) {
	for _, name := range []string{"root-dual-quorum", "expired-intermediate", "online-cache-reset", "targets-key-replacement"} {
		t.Run(name, func(t *testing.T) {
			f := newTrustFixture(t)
			f.release(8, 10)
			previous := f.run(nil)
			if name == "root-dual-quorum" {
				old := f.rotate("root")
				f.root.Signed.Version = 2
				f.put("2.root.json", f.rootSign(old[:2]...))
			}
			if name == "expired-intermediate" {
				f.now += 10000
				f.root.Signed.Version = 2
				f.root.Signed.Expires = time.Unix(f.now-1, 0)
				f.put("2.root.json", f.rootSign())
				f.root.Signed.Version = 3
				f.root.Signed.Expires = time.Unix(f.now+86400, 0)
				f.put("3.root.json", f.rootSign())
			}
			if name == "online-cache-reset" {
				f.rotate("timestamp")
				f.rotate("snapshot")
				f.root.Signed.Version = 2
				f.put("2.root.json", f.rootSign())
			}
			if name == "targets-key-replacement" {
				f.rotate("targets")
				f.root.Signed.Version = 2
				f.put("2.root.json", f.rootSign())
			}
			v := int64(9)
			if name == "online-cache-reset" {
				v = 1
			}
			f.release(v, 10)
			next := f.run(&previous)
			if next.report.OnlineCacheReset != (name == "online-cache-reset") || next.report.Sequence != 10 {
				t.Fatal("rotation lost release floor")
			}
		})
	}
}
