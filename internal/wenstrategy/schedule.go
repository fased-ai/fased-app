package wenstrategy

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"time"
)

type Slot struct {
	RoundID string `json:"roundId"`
	OpensAt string `json:"opensAt"`
}
type ScheduleState struct {
	AcceptedRounds      int     `json:"acceptedRounds"`
	AcceptedRoundsToday int     `json:"acceptedRoundsToday"`
	LastAcceptedRound   *string `json:"lastAcceptedRound"`
}

func (s Schedule) Validate() error {
	start, e := timestamp(s.StartsAt)
	if e != nil {
		return e
	}
	end, e := timestamp(s.ExpiresAt)
	if e != nil {
		return e
	}
	if !end.After(start) || end.Sub(start) > 24*time.Hour {
		return fmt.Errorf("invalid session interval")
	}
	if _, e = amount(s.AnchorRound); e != nil {
		return e
	}
	if s.EveryNthRound < 1 || s.EveryNthRound > 144 || s.MaxRoundsTotal < 1 || s.MaxRoundsTotal > 144 || s.MaxRoundsPerUTCDay < 1 || s.MaxRoundsPerUTCDay > 144 || s.CooldownRounds < 0 || s.CooldownRounds > 143 {
		return fmt.Errorf("invalid schedule bounds")
	}
	if len(s.UTCWindows) > 1440 {
		return fmt.Errorf("too many windows")
	}
	for _, w := range s.UTCWindows {
		if w.StartMinute < 0 || w.StartMinute >= w.EndMinute || w.EndMinute > 1440 {
			return fmt.Errorf("invalid UTC window")
		}
	}
	if len(s.SelectedRoundIDs)+len(s.SkipRoundIDs) > 144 {
		return fmt.Errorf("too many explicit slots")
	}
	for _, list := range [][]string{s.SelectedRoundIDs, s.SkipRoundIDs} {
		if len(list) > 144 {
			return fmt.Errorf("too many slots")
		}
		seen := map[string]bool{}
		for _, id := range list {
			if _, e = amount(id); e != nil {
				return e
			}
			if seen[id] {
				return fmt.Errorf("duplicate slot")
			}
			seen[id] = true
		}
	}
	if r := s.RandomSelection; r != nil {
		if !seed.MatchString(r.SeedHex) || r.Count < 1 || r.Count > 144 {
			return fmt.Errorf("invalid random selection")
		}
	}
	return nil
}
func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}
func baseEligible(s Schedule, slot Slot) (bool, error) {
	id, e := amount(slot.RoundID)
	if e != nil {
		return false, e
	}
	open, e := timestamp(slot.OpensAt)
	if e != nil {
		return false, e
	}
	start, _ := timestamp(s.StartsAt)
	end, _ := timestamp(s.ExpiresAt)
	anchor, _ := amount(s.AnchorRound)
	if open.Before(start) || !open.Before(end) || id < anchor || (id-anchor)%uint64(s.EveryNthRound) != 0 || contains(s.SkipRoundIDs, slot.RoundID) || len(s.SelectedRoundIDs) > 0 && !contains(s.SelectedRoundIDs, slot.RoundID) {
		return false, nil
	}
	if len(s.UTCWindows) > 0 {
		minute := open.Hour()*60 + open.Minute()
		match := false
		for _, w := range s.UTCWindows {
			if minute >= w.StartMinute && minute < w.EndMinute {
				match = true
			}
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

// CandidateSlots filters an explicit canonical slot inventory; it does not invent
// timing from IDs. Callers must supply the complete session inventory for pinning.
func CandidateSlots(s Schedule, slots []Slot) ([]Slot, error) {
	if e := s.Validate(); e != nil {
		return nil, e
	}
	if len(slots) > 144 {
		return nil, fmt.Errorf("too many candidate slots")
	}
	out := []Slot{}
	seen := map[string]bool{}
	for _, slot := range slots {
		if seen[slot.RoundID] {
			return nil, fmt.Errorf("duplicate supplied slot")
		}
		seen[slot.RoundID] = true
		ok, e := baseEligible(s, slot)
		if e != nil {
			return nil, e
		}
		if ok {
			out = append(out, slot)
		}
	}
	sort.Slice(out, func(i, j int) bool { a, _ := amount(out[i].RoundID); b, _ := amount(out[j].RoundID); return a < b })
	return out, nil
}

// PinRandom performs the specified seed:id hash ordering before market filters.
// Failed future filters never permit a replacement or reroll.
func PinRandom(s Schedule, slots []Slot) ([]string, error) {
	if e := completeInventory(s, slots); e != nil {
		return nil, e
	}

	cs, e := CandidateSlots(s, slots)
	if e != nil {
		return nil, e
	}
	if s.RandomSelection == nil {
		return nil, fmt.Errorf("random selection absent")
	}
	if len(cs) < s.RandomSelection.Count {
		return nil, fmt.Errorf("review reduced random count")
	}
	sort.Slice(cs, func(i, j int) bool {
		a := sha256.Sum256([]byte(s.RandomSelection.SeedHex + ":" + cs[i].RoundID))
		b := sha256.Sum256([]byte(s.RandomSelection.SeedHex + ":" + cs[j].RoundID))
		if a != b {
			return string(a[:]) < string(b[:])
		}
		x, _ := amount(cs[i].RoundID)
		y, _ := amount(cs[j].RoundID)
		return x < y
	})
	out := make([]string, s.RandomSelection.Count)
	for i := range out {
		out[i] = cs[i].RoundID
	}
	return out, nil
}

// Eligible is a pure admission check, not a clock, scheduler or budget reservation.
// pinnedIDs must come from PinRandom over the complete admitted slot inventory.
func Eligible(s Schedule, slot Slot, state ScheduleState, pinnedIDs []string) (bool, error) {
	if e := s.Validate(); e != nil {
		return false, e
	}
	ok, e := baseEligible(s, slot)
	if e != nil || !ok {
		return ok, e
	}
	if state.AcceptedRounds < 0 || state.AcceptedRoundsToday < 0 || state.AcceptedRoundsToday > state.AcceptedRounds || (state.AcceptedRounds > 0) != (state.LastAcceptedRound != nil) {
		return false, fmt.Errorf("invalid schedule state")
	}
	if state.AcceptedRounds >= s.MaxRoundsTotal || state.AcceptedRoundsToday >= s.MaxRoundsPerUTCDay {
		return false, nil
	}
	if s.RandomSelection != nil {
		if len(pinnedIDs) != s.RandomSelection.Count {
			return false, fmt.Errorf("missing pinned random selection")
		}
		seen := map[string]bool{}
		for _, id := range pinnedIDs {
			if _, e := amount(id); e != nil {
				return false, e
			}
			if seen[id] {
				return false, fmt.Errorf("duplicate pinned selection")
			}
			seen[id] = true
		}
		if !seen[slot.RoundID] {
			return false, nil
		}
	}
	if state.LastAcceptedRound != nil {
		id, _ := amount(slot.RoundID)
		last, e := amount(*state.LastAcceptedRound)
		if e != nil {
			return false, e
		}
		if id <= last || id-last <= uint64(s.CooldownRounds) {
			return false, nil
		}
	}
	return true, nil
}

// completeInventory checks coverage and shape, not authenticity. Slot identities
// and nominal times still require canonical market admission by the caller.
func completeInventory(s Schedule, slots []Slot) error {
	if e := s.Validate(); e != nil {
		return e
	}
	if len(slots) == 0 || len(slots) > 144 {
		return fmt.Errorf("incomplete random inventory")
	}
	ordered := append([]Slot(nil), slots...)
	for _, slot := range ordered {
		if _, e := amount(slot.RoundID); e != nil {
			return e
		}
		if _, e := timestamp(slot.OpensAt); e != nil {
			return e
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, _ := amount(ordered[i].RoundID)
		b, _ := amount(ordered[j].RoundID)
		return a < b
	})
	start, _ := timestamp(s.StartsAt)
	end, _ := timestamp(s.ExpiresAt)
	var lastID uint64
	var lastTime time.Time
	for i, slot := range ordered {
		id, _ := amount(slot.RoundID)
		open, _ := timestamp(slot.OpensAt)
		if open.Before(start) || !open.Before(end) {
			return fmt.Errorf("random inventory outside session")
		}
		if i == 0 {
			if open.Sub(start) >= 10*time.Minute {
				return fmt.Errorf("random inventory missing opening")
			}
		} else {
			if id <= lastID || id-lastID != 1 || open.Sub(lastTime) != 10*time.Minute {
				return fmt.Errorf("nonconsecutive random inventory")
			}
		}
		lastID = id
		lastTime = open
	}
	if lastTime.Add(10 * time.Minute).Before(end) {
		return fmt.Errorf("random inventory missing ending")
	}
	return nil
}
