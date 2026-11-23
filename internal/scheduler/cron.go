package scheduler

import (
	"errors"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// TaskCalendar is a versioned reviewed schedule. Once-at keeps its absolute
// instant; cron evaluates named-zone wall time within a fixed bounded horizon.
type TaskCalendar struct {
	Expression string `json:"expression,omitempty"`
	Until      int64  `json:"until,omitempty"`
	Version    string `json:"version"`
	At         int64  `json:"at"`
	TimeZone   string `json:"timeZone"`
}

// CronField stores the allowed values bitmask for a single 5-field cron component.
type CronField struct {
	Bits uint64
	Wild bool
}

// Has reports whether bit v is enabled in this cron field.
func (f CronField) Has(v int) bool { return f.Bits&(uint64(1)<<v) != 0 }

// CronRule stores five parsed numeric cron fields. Day-of-month and day-of-week
// use conventional OR semantics when both are restricted.
type CronRule [5]CronField

// ParseCron parses a strict 5-field numeric cron expression.
func ParseCron(expression string) (CronRule, error) {
	var rule CronRule
	fields := strings.Fields(expression)
	if len(expression) > 120 || len(fields) != 5 {
		return rule, errors.New("use five cron fields: minute hour day month weekday")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		for _, char := range field {
			if (char < '0' || char > '9') && char != '*' && char != '/' && char != ',' && char != '-' {
				return rule, errors.New("cron fields must be numeric")
			}
		}
		lo, hi := bounds[i][0], bounds[i][1]
		rule[i].Wild = strings.HasPrefix(field, "*")
		for _, item := range strings.Split(field, ",") {
			parts := strings.Split(item, "/")
			if len(parts) > 2 {
				return rule, errors.New("invalid cron step")
			}
			step := 1
			var e error
			if len(parts) == 2 {
				step, e = strconv.Atoi(parts[1])
				if e != nil || step < 1 || step > hi-lo+1 {
					return rule, errors.New("invalid cron step")
				}
			}
			start, end := lo, hi
			if parts[0] != "*" {
				span := strings.Split(parts[0], "-")
				if len(span) > 2 {
					return rule, errors.New("invalid cron range")
				}
				start, e = strconv.Atoi(span[0])
				if e != nil {
					return rule, errors.New("cron fields must be numeric")
				}
				end = start
				if len(span) == 2 {
					end, e = strconv.Atoi(span[1])
					if e != nil {
						return rule, errors.New("invalid cron range")
					}
				} else if len(parts) == 2 {
					end = hi
				}
			}
			if start < lo || end > hi || start > end {
				return rule, errors.New("cron value outside its field range")
			}
			for v := start; v <= end; v += step {
				bit := v
				if i == 4 && v == 7 {
					bit = 0
				}
				rule[i].Bits |= uint64(1) << bit
			}
		}
	}
	return rule, nil
}

// Matches reports whether t satisfies this cron rule in t's attached location.
func (r CronRule) Matches(t time.Time) bool {
	if !r[0].Has(t.Minute()) || !r[1].Has(t.Hour()) || !r[3].Has(int(t.Month())) {
		return false
	}
	day, week := r[2].Has(t.Day()), r[4].Has(int(t.Weekday()))
	if !r[2].Wild && !r[4].Wild {
		return day || week
	}
	return day && week
}

// FirstCronWallMinute ensures backward DST transitions trigger a wall-clock minute only once.
func FirstCronWallMinute(t time.Time) bool {
	start, _ := t.ZoneBounds()
	if start.IsZero() {
		return true
	}
	_, offset := t.Zone()
	_, before := start.Add(-time.Second).Zone()
	if before <= offset {
		return true
	}
	previous := t.Add(-time.Duration(before-offset) * time.Second)
	return previous.Year() != t.Year() || previous.YearDay() != t.YearDay() || previous.Hour() != t.Hour() || previous.Minute() != t.Minute()
}

// NextCron returns the next matching Unix second strictly after after within c's bounded horizon.
func NextCron(c *TaskCalendar, after int64) int64 {
	if c == nil || c.Until <= after {
		return 0
	}
	rule, e := ParseCron(c.Expression)
	if e != nil {
		return 0
	}
	zone, e := time.LoadLocation(c.TimeZone)
	if e != nil {
		return 0
	}
	// At most one year is examined per admitted schedule. Iterating real instants
	// skips missing wall times and avoids ambiguous local-time construction.
	end := c.Until
	if limit := after + 366*86400; end > limit {
		end = limit
	}
	for at := (after/60 + 1) * 60; at <= end; at += 60 {
		local := time.Unix(at, 0).In(zone)
		if rule.Matches(local) && FirstCronWallMinute(local) {
			return at
		}
	}
	return 0
}

// NormalizedCalendar populates the initial cron occurrence timestamp when unset.
func NormalizedCalendar(c *TaskCalendar, now int64) *TaskCalendar {
	if c == nil {
		return nil
	}
	copy := *c
	if copy.Version == "cron-v1" && copy.At == 0 {
		copy.At = NextCron(&copy, now+59)
	}
	return &copy
}

// ValidCron reports whether a cron-v1 TaskCalendar has a valid expression, timezone, and At instant.
func ValidCron(c *TaskCalendar) bool {
	if c.At <= 0 || c.At%60 != 0 || c.Until < c.At || c.Until-c.At > 366*86400 {
		return false
	}
	rule, e := ParseCron(c.Expression)
	if e != nil {
		return false
	}
	zone, e := time.LoadLocation(c.TimeZone)
	if e != nil {
		return false
	}
	t := time.Unix(c.At, 0).In(zone)
	return rule.Matches(t) && FirstCronWallMinute(t)
}
