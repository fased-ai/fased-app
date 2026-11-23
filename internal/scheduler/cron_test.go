package scheduler

import (
	"testing"
	"time"
)

func TestParseCronAndSemantics(t *testing.T) {
	t.Parallel()
	rule, err := ParseCron("*/15 9-11 1 1 7")
	if err != nil {
		t.Fatalf("ParseCron failed: %v", err)
	}
	// Sunday bit 7 normalizes to 0 and DOM/DOW OR semantics apply when both restricted.
	if !rule[4].Has(0) || rule[4].Wild || rule[2].Wild {
		t.Fatalf("unexpected cron field flags: %+v", rule)
	}
	// Matches Jan 1 even if not Sunday (Wednesday in 2025)
	if !rule.Matches(time.Date(2025, 1, 1, 9, 15, 0, 0, time.UTC)) {
		t.Fatal("expected DOM/DOW OR match on Jan 1")
	}
	// Matches Sunday in Jan even if day != 1
	if !rule.Matches(time.Date(2025, 1, 5, 10, 30, 0, 0, time.UTC)) {
		t.Fatal("expected DOM/DOW OR match on Sunday")
	}
	// Does not match outside hour range
	if rule.Matches(time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("matched outside hour bounds")
	}

	for _, bad := range []string{"", "* * * *", "* * * * * *", "@daily", "60 * * * *", "0 24 * * *", "0 0 * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Fatalf("admitted invalid cron %q", bad)
		}
	}
}

func TestCronDSTTransitionsAndNormalizedCalendar(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	// Fall-back DST: 2024-11-03 01:30 EDT (first) vs 01:30 EST (second, 1h later)
	first := time.Date(2024, 11, 3, 1, 30, 0, 0, ny)
	// Go time.Date on ambiguous local time picks earlier or let's construct via UTC addition:
	utcBase := time.Date(2024, 11, 3, 5, 30, 0, 0, time.UTC) // 01:30 EDT (-04:00)
	edtFirst := utcBase.In(ny)
	estSecond := utcBase.Add(time.Hour).In(ny)
	_ = first
	if !FirstCronWallMinute(edtFirst) {
		t.Fatal("expected first fall-back wall minute to pass")
	}
	if FirstCronWallMinute(estSecond) {
		t.Fatal("expected repeated fall-back wall minute to be skipped")
	}

	cal := NormalizedCalendar(&TaskCalendar{
		Version:    "cron-v1",
		Expression: "30 2 * * *",
		TimeZone:   "America/New_York",
		Until:      time.Date(2025, 3, 12, 12, 0, 0, 0, ny).Unix(),
	}, time.Date(2025, 3, 9, 0, 0, 0, 0, ny).Unix())
	// On 2025-03-09 in NY, 02:30 does not exist (spring forward 01:59 -> 03:00), so next is March 10!
	if !ValidCron(cal) {
		t.Fatalf("NormalizedCalendar produced invalid cron: %+v", cal)
	}
	gotLocal := time.Unix(cal.At, 0).In(ny)
	if gotLocal.Day() != 10 || gotLocal.Hour() != 2 || gotLocal.Minute() != 30 {
		t.Fatalf("expected 2025-03-10 02:30 after spring-forward skip, got %v", gotLocal)
	}
}
