package update

import (
	"strings"
	"testing"
)

func TestManagedSwapSnapshotRetainsUnknownAndRejectsAmbiguousShape(t *testing.T) {
	id := strings.Repeat("1", 32)
	hash := strings.Repeat("a", 64)
	c := CoreSnapshot{Version: 1, ProbeID: id, BootID: id, InstanceID: "fixture-core", State: State{Name: "core-db", Version: 32, Anchor: hash}}
	s := SignerSnapshot{Version: 2, ProbeID: id, BootID: id, AuthoritySHA256: hash, RecoveryHold: true, Pending: 1, Unknown: 1, States: []State{{Name: "vault", Version: 3, Features: []string{"managed-key-v2", "authority-epoch-v1", "managed-fresh-history-v1"}, Anchor: hash}, {Name: "operations", Version: 1, Features: []string{"managed-key-history-v1", "managed-maintenance-v2", "install-completion-v1", "managed-swap-fixture-v1"}, Anchor: hash}}}
	inv, e := JoinSnapshots(c, s, Target{OS: "linux", Arch: "amd64"})
	if e != nil || inv.Pending != 1 || inv.Unknown != 1 || !inv.RecoveryHold {
		t.Fatal("inventory discarded managed originals", e)
	}
	for _, mutate := range []func(*SignerSnapshot){func(s *SignerSnapshot) { s.Unknown = 2 }, func(s *SignerSnapshot) { s.States[0].Features = nil }, func(s *SignerSnapshot) { s.States[1].Features = nil }, func(s *SignerSnapshot) { s.RecoveryHold = false }} {
		changed := s
		changed.States = append([]State(nil), s.States...)
		mutate(&changed)
		if _, e = JoinSnapshots(c, changed, Target{OS: "linux", Arch: "amd64"}); e == nil {
			t.Fatal("ambiguous managed shape accepted")
		}
	}
}
