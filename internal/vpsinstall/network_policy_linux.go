//go:build linux

package vpsinstall

import (
	"encoding/json"
	"errors"
)

// Text snapshots prove that owned objects have not changed; they do not prove
// that nft installed the intended policy. Inspect the kernel's actual table,
// chain hook/default verdict and finite rule inventory before claiming success.
func verifyNetworkTableJSON(data []byte, m Manifest, a networkAttempt) error {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if len(data) == 0 || len(data) > 1<<20 || json.Unmarshal(data, &doc) != nil {
		return errors.New("invalid bounded kernel nftables policy")
	}
	tables, chains, rules, metadata := 0, 0, 0, 0
	for _, entry := range doc.Nftables {
		if len(entry) != 1 {
			return errors.New("ambiguous kernel nftables object")
		}
		for kind, body := range entry {
			switch kind {
			case "metainfo":
				metadata++
				if metadata > 1 {
					return errors.New("duplicate nftables metadata")
				}
			case "table":
				var table struct{ Family, Name, Comment string }
				if json.Unmarshal(body, &table) != nil || table.Family != "inet" || table.Name != networkTable(m, a) || table.Comment != "Fased Remote "+m.ID+" attempt "+a.ID {
					return errors.New("kernel firewall table identity differs from the owned attempt")
				}
				tables++
			case "chain":
				var chain struct {
					Family, Table, Name, Type, Hook, Policy string
					Prio                                    *int
				}
				if json.Unmarshal(body, &chain) != nil || chain.Family != "inet" || chain.Table != networkTable(m, a) || chain.Name != "input" || chain.Type != "filter" || chain.Hook != "input" || chain.Prio == nil || *chain.Prio != 0 || chain.Policy != "drop" {
					return errors.New("kernel firewall lacks the owned input/filter/drop chain")
				}
				chains++
			case "rule":
				var rule struct {
					Family, Table, Chain string
					Expr                 []json.RawMessage
				}
				if json.Unmarshal(body, &rule) != nil || rule.Family != "inet" || rule.Table != networkTable(m, a) || rule.Chain != "input" || len(rule.Expr) == 0 {
					return errors.New("kernel firewall rule has the wrong table/chain or no expression")
				}
				rules++
			default:
				return errors.New("unsupported object in owned kernel firewall table")
			}
		}
	}
	if tables != 1 || chains != 1 || rules != 8 {
		return errors.New("kernel firewall must contain exactly one owned table, one input/drop chain and eight rules")
	}
	return nil
}
