package update

import (
	"context"
	"testing"
)

func TestTrustRehearsalPersistsVerifiedProgressOnLaterFailure(t *testing.T) {
	for _, kind := range []string{"revoked-root-targets", "timestamp-before-bad-snapshot"} {
		t.Run(kind, func(t *testing.T) {
			f := newTrustFixture(t)
			first := f.run(nil)
			store, _ := fixtureStore(t)
			if _, e := store.Commit(first); e != nil {
				t.Fatal(e)
			}
			oldTargets := f.keys["targets"]
			if kind == "revoked-root-targets" {
				f.rotate("targets")
				f.root.Signed.Version = 2
				f.put("2.root.json", f.rootSign())
				current := f.keys["targets"]
				f.keys["targets"] = oldTargets
				f.release(2, 11)
				f.keys["targets"] = current
			} else {
				f.release(3, 11)
				f.put("3.snapshot.json", []byte("incomplete"))
			}
			progress, e := RehearseTrust(context.Background(), f.options(&first))
			if e == nil || len(progress.next) == 0 || progress.Report().MetadataVerified {
				t.Fatal("verified trust progress lost on later failure", e)
			}
			if _, e = store.Commit(progress); e != nil {
				t.Fatal("verified progress cannot persist", e)
			}
			if _, e = store.Load(first.seedSHA, first.report.NextSHA256); e == nil {
				t.Fatal("old external anchor accepted")
			}
			// Withhold root succession and offer an older otherwise valid chain.
			f.mu.Lock()
			delete(f.files, "2.root.json")
			f.mu.Unlock()
			current := f.keys["targets"]
			f.keys["targets"] = oldTargets
			f.release(1, 11)
			f.keys["targets"] = current
			if _, e = RehearseTrust(context.Background(), f.options(&progress)); e == nil {
				t.Fatal("retry accepted revoked/older trust after a later failure")
			}
			f.release(4, 12)
			next := f.run(&progress)
			if _, e = store.Commit(next); e != nil {
				t.Fatal("fresh forward repair failed", e)
			}
		})
	}
}
