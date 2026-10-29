package domain_test

import (
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func TestSpreadOfNothingIsNotZero(t *testing.T) {
	t.Parallel()
	s := domain.NewSpread(nil)
	if s.Measured() {
		t.Fatalf("an empty sample has percentiles: %+v", s)
	}
	if s.N != 0 || s.P50 != 0 || s.P90 != 0 {
		t.Fatalf("want a zero spread, got %+v", s)
	}
}

func TestSpreadInterpolatesLikePercentileCont(t *testing.T) {
	t.Parallel()
	// percentile_cont over 1..10 seconds: p50 = 5.5 s, p90 = 9.1 s.
	ds := make([]time.Duration, 0, 10)
	for i := 10; i >= 1; i-- { // unsorted on purpose
		ds = append(ds, time.Duration(i)*time.Second)
	}
	s := domain.NewSpread(ds)
	if !s.Measured() || s.N != 10 {
		t.Fatalf("want 10 samples, got %+v", s)
	}
	if got, want := s.P50, 5500*time.Millisecond; got != want {
		t.Errorf("p50 = %v, want %v", got, want)
	}
	if got, want := s.P90, 9100*time.Millisecond; got != want {
		t.Errorf("p90 = %v, want %v", got, want)
	}
}

func TestSpreadOfOneSampleIsThatSample(t *testing.T) {
	t.Parallel()
	s := domain.NewSpread([]time.Duration{7 * time.Minute})
	if !s.Measured() || s.N != 1 || s.P50 != 7*time.Minute || s.P90 != 7*time.Minute {
		t.Fatalf("got %+v", s)
	}
}

func TestSpreadDoesNotReorderTheCallersSlice(t *testing.T) {
	t.Parallel()
	ds := []time.Duration{3, 1, 2}
	domain.NewSpread(ds)
	if ds[0] != 3 || ds[1] != 1 || ds[2] != 2 {
		t.Fatalf("the caller's sample was sorted under it: %v", ds)
	}
}

func TestSpreadFromSecondsRejectsTheNotMeasuredSentinel(t *testing.T) {
	t.Parallel()
	if s := domain.SpreadFromSeconds(0, -1, -1); s.Measured() {
		t.Errorf("an empty SQL percentile reads as measured: %+v", s)
	}
	s := domain.SpreadFromSeconds(4, 1.5, 3)
	if !s.Measured() || s.N != 4 || s.P50 != 1500*time.Millisecond || s.P90 != 3*time.Second {
		t.Errorf("got %+v", s)
	}
}

func TestShareIsNotMeasuredWithoutMessages(t *testing.T) {
	t.Parallel()
	// No active message means no share: 0/0 is not 0 %, and it is not
	// 100 % either. Both readings would be a claim nobody measured.
	if s := (domain.Share{Of: 0, With: 0}); s.Measured() {
		t.Errorf("a project with no active messages reports a share")
	}
	s := domain.Share{Of: 8, With: 2}
	if !s.Measured() || s.Ratio() != 0.25 {
		t.Errorf("ratio = %v, want 0.25", s.Ratio())
	}
}
