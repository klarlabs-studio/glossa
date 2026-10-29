package messageformat

import (
	"testing"
	"time"
)

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestWithTimeZone(t *testing.T) {
	berlin := mustLocation(t, "Europe/Berlin")
	tokyo := mustLocation(t, "Asia/Tokyo")
	summer := time.Date(2026, 9, 19, 14, 5, 0, 0, time.UTC)
	winter := time.Date(2026, 1, 19, 14, 5, 0, 0, time.UTC)
	tests := []struct {
		name string
		src  string
		v    any
		tz   *time.Location
		want string
	}{
		{"no zone: the value's own location", "{$t :time}", summer.In(tokyo), nil, "23:05"},
		{"zone applied", "{$t :time}", summer, berlin, "16:05"},
		{"daylight saving follows the zone", "{$t :time}", winter, berlin, "15:05"},
		{"a value in another location is converted", "{$t :time}", summer.In(tokyo), time.UTC, "14:05"},
		{"date crosses midnight", "{$t :date length=short}", time.Date(2026, 9, 19, 23, 30, 0, 0, time.UTC), tokyo, "20.9.2026"},
		{"datetime", "{$t :datetime dateLength=long}", summer, tokyo, "19. September 2026 um 23:05"},
		{"placeholder zone wins", "{$t :time timeZone=UTC}", summer, berlin, "14:05"},
		{"string operand", "{$t :time}", "2026-09-19T14:05:00Z", berlin, "16:05"},
		{"fixed zone", "{$t :time}", summer, time.FixedZone("IST", 5*3600+1800), "19:35"},
		{"through a local", ".local $d = {$t :datetime}\n{{{$d :time}}}", summer, tokyo, "23:05"},
		{"zone name", "{$t :time timeZoneStyle=long}", winter, berlin, "15:05 Mitteleuropäische Normalzeit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseMF2(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			opts := []FormatOption{WithBidiIsolation(false)}
			if tt.tz != nil {
				opts = append(opts, WithTimeZone(tt.tz))
			}
			got, err := Format(msg, "de", map[string]any{"t": tt.v}, opts...)
			if err != nil {
				t.Fatalf("Format: %v (output %q)", err, got)
			}
			if got != tt.want {
				t.Errorf("Format = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithTimeZoneKeepsErrors(t *testing.T) {
	msg, err := ParseMF2("{$t :time}")
	if err != nil {
		t.Fatal(err)
	}
	want, wantErr := Format(msg, "de", map[string]any{"t": "gestern"})
	got, gotErr := Format(msg, "de", map[string]any{"t": "gestern"}, WithTimeZone(time.UTC))
	if got != want || len(errCodes(gotErr)) != 1 || errCodes(gotErr)[0] != errCodes(wantErr)[0] {
		t.Errorf("with zone %q %v, without %q %v: want the same single error", got, gotErr, want, wantErr)
	}
}
