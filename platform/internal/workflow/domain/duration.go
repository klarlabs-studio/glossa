package domain

import (
	"encoding/json"
	"regexp"
	"strconv"
	"time"
)

// Duration is a due period in a definition, written as an ISO 8601
// duration in weeks, days, hours and minutes ("P3D", "PT4H", "P1DT12H").
// Years and months are refused: their length depends on the calendar,
// and a due date must mean the same thing whenever it is computed.
//
// A Duration is never a timer in the chart. The runner stores due_at =
// now + Duration on the assignment or approval request, and Workflow's
// sweep raises timer.due from it (RFC 0006 §2.3).
type Duration struct {
	iso string
	d   time.Duration
}

// maxDue bounds a due period: a year is already longer than any piece
// of localization work should wait.
const maxDue = 366 * 24 * time.Hour

var isoDuration = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?)?$`)

// ParseDuration reads an ISO 8601 duration of weeks, days, hours and
// minutes.
func ParseDuration(s string) (Duration, error) {
	m := isoDuration.FindStringSubmatch(s)
	if m == nil || s == "P" || s == "PT" || s[len(s)-1] == 'T' {
		return Duration{}, paramErr("due must be an ISO 8601 duration such as P3D or PT4H, not %q", s)
	}
	units := []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute}
	var d time.Duration
	for i, u := range units {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil || n > int(maxDue/u) {
			return Duration{}, paramErr("due %q is longer than a year", s)
		}
		d += time.Duration(n) * u
	}
	if d <= 0 || d > maxDue {
		return Duration{}, paramErr("due must be more than nothing and at most a year, not %q", s)
	}
	return Duration{iso: s, d: d}, nil
}

// IsZero reports whether no duration was given.
func (d Duration) IsZero() bool { return d.d == 0 }

// Std is the duration as a time.Duration.
func (d Duration) Std() time.Duration { return d.d }

// String is the duration as written.
func (d Duration) String() string { return d.iso }

// MarshalJSON writes the duration as written.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.iso) }

// UnmarshalJSON reads an ISO 8601 duration.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return paramErr("due must be a string such as \"P3D\"")
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
