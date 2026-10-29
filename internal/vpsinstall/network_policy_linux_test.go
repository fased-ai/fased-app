//go:build linux

package vpsinstall

import (
	"context"
	"encoding/json"
	"testing"
)

// This is a structural kernel-listing fixture, not an nft interpreter or OS
// proof. The retained Ubuntu VM owns actual expression/program acceptance.
func networkPolicyFixture(m Manifest, a networkAttempt) []byte {
	entries := []any{
		map[string]any{"metainfo": map[string]any{"version": "1.0.9", "json_schema_version": 1}},
		map[string]any{"table": map[string]any{"family": "inet", "name": networkTable(m, a), "handle": 1, "comment": "Fased Remote " + m.ID + " attempt " + a.ID}},
		map[string]any{"chain": map[string]any{"family": "inet", "table": networkTable(m, a), "name": "input", "type": "filter", "hook": "input", "prio": 0, "policy": "drop", "handle": 2}},
	}
	for i := 0; i < 8; i++ {
		entries = append(entries, map[string]any{"rule": map[string]any{"family": "inet", "table": networkTable(m, a), "chain": "input", "handle": i + 3, "expr": []any{map[string]any{"accept": nil}}}})
	}
	return encoded(map[string]any{"nftables": entries})
}
func TestNetworkPolicyRequiresKernelInputDropChainAndEightRules(t *testing.T) {
	_, _, m := networkFixture(t)
	a := networkAttempt{ID: "11111111111111111111111111111111", Access: nProof()}
	for _, variant := range []string{"complete", "empty-table", "accept-policy", "no-rules", "wrong-table", "wrong-rule-table", "wrong-chain", "wrong-hook", "wrong-type", "wrong-priority", "missing-priority", "seven-rules", "nine-rules", "empty-rule"} {
		t.Run(variant, func(t *testing.T) {
			var doc map[string][]map[string]map[string]any
			if e := json.Unmarshal(networkPolicyFixture(m, a), &doc); e != nil {
				t.Fatal(e)
			}
			entries := doc["nftables"]
			switch variant {
			case "empty-table":
				entries = entries[:2]
			case "accept-policy":
				entries[2]["chain"]["policy"] = "accept"
			case "no-rules":
				entries = entries[:3]
			case "wrong-table":
				entries[1]["table"]["name"] = "foreign"
			case "wrong-rule-table":
				entries[3]["rule"]["table"] = "foreign"
			case "wrong-chain":
				entries[3]["rule"]["chain"] = "forward"
			case "wrong-hook":
				entries[2]["chain"]["hook"] = "forward"
			case "wrong-type":
				entries[2]["chain"]["type"] = "nat"
			case "wrong-priority":
				entries[2]["chain"]["prio"] = 10
			case "missing-priority":
				delete(entries[2]["chain"], "prio")
			case "seven-rules":
				entries = entries[:10]
			case "nine-rules":
				entries = append(entries, entries[3])
			case "empty-rule":
				entries[3]["rule"]["expr"] = []any{}
			}
			doc["nftables"] = entries
			e := verifyNetworkTableJSON(encoded(doc), m, a)
			if (e == nil) != (variant == "complete") {
				t.Fatal(variant, e)
			}
		})
	}
}
func nProof() networkAccess {
	return networkAccess{SessionID: "structural-fixture", PeerIP: "100.90.0.1", ServerIP: "100.90.0.2", SSHPort: 22}
}
func TestNetworkControllerSnapshotCannotBlessEmptyKernelTable(t *testing.T) {
	h, n, m, s := networkBegin(t)
	a, e := n.load(m)
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string][]map[string]json.RawMessage
	if e = json.Unmarshal(networkPolicyFixture(m, a), &doc); e != nil {
		t.Fatal(e)
	}
	doc["nftables"] = doc["nftables"][:2]
	h.kernelJSON = encoded(doc)
	if status, e := n.inspect(context.Background(), m); e == nil || status.Phase != "recovery-required" {
		t.Fatal("empty table passed matching snapshot", status, e)
	}
	h.proof.SessionID = "different-private-connection"
	if status, e := n.confirm(context.Background(), m, s.AttemptID); e == nil || status.Phase == "committed" {
		t.Fatal("empty table committed", status, e)
	}
}
