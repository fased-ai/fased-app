package wenstrategy

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
)

var presets = map[string][5]int{
	"wide": {2000, 2000, 2000, 2000, 2000}, "center": {500, 1500, 6000, 1500, 500},
	"trend-up": {500, 1000, 1500, 4500, 2500}, "trend-down": {2500, 4500, 1500, 1000, 500}, "tails": {4000, 1000, 0, 1000, 4000},
}

// Preset returns a copy, so callers cannot mutate the contract catalogue.
func Preset(name string) ([5]int, bool) { p, ok := presets[name]; return p, ok }
func weightsValid(ws []int) error {
	if len(ws) != 5 {
		return fmt.Errorf("five weights required")
	}
	sum := 0
	for _, w := range ws {
		if w < 0 || w > 10000 {
			return fmt.Errorf("invalid weight")
		}
		sum += w
	}
	if sum != 10000 {
		return fmt.Errorf("weights must sum to 10000")
	}
	return nil
}
func presetValid(name string, ws []int) error {
	if e := weightsValid(ws); e != nil {
		return e
	}
	if name == "custom" {
		return nil
	}
	p, ok := presets[name]
	if !ok {
		return fmt.Errorf("unknown preset")
	}
	for i, w := range ws {
		if w != p[i] {
			return fmt.Errorf("modified preset must be custom")
		}
	}
	return nil
}

// CompileAllocation distributes every lamport by largest remainder, ties A–E.
// It rejects dust instead of increasing spending or silently dropping a range.
func CompileAllocation(value string, ws []int) ([5]string, error) {
	var out [5]string
	if e := weightsValid(ws); e != nil {
		return out, e
	}
	n, e := amount(value)
	if e != nil || n == 0 {
		return out, fmt.Errorf("invalid positive allocation amount")
	}
	var whole [5]uint64
	type remainder struct {
		i int
		r int64
	}
	rs := make([]remainder, 5)
	var sum uint64
	for i, w := range ws {
		p := new(big.Int).Mul(new(big.Int).SetUint64(n), big.NewInt(int64(w)))
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(p, big.NewInt(10000), r)
		whole[i] = q.Uint64()
		sum += whole[i]
		rs[i] = remainder{i, r.Int64()}
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].r != rs[j].r {
			return rs[i].r > rs[j].r
		}
		return rs[i].i < rs[j].i
	})
	for i := uint64(0); i < n-sum; i++ {
		whole[rs[i].i]++
	}
	for i, v := range whole {
		if ws[i] > 0 && v < MinimumEntryLamports {
			return out, fmt.Errorf("allocation below minimum in range %d", i)
		}
		out[i] = strconv.FormatUint(v, 10)
	}
	return out, nil
}
