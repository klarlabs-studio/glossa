package remote

import (
	"testing"
	"time"
)

func TestBatchSizeAndTimeoutComeFromTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{{"", MaxBatch}, {"100", 100}, {"1", 1}, {"0", MaxBatch}, {"501", MaxBatch}, {"x", MaxBatch}} {
		t.Setenv("GLOSSA_BATCH_SIZE", tc.env)
		if got := batchSize(); got != tc.want {
			t.Errorf("GLOSSA_BATCH_SIZE=%q: batchSize() = %d, want %d", tc.env, got, tc.want)
		}
	}
	for _, tc := range []struct {
		env  string
		want time.Duration
	}{{"", 30 * time.Second}, {"2m", 2 * time.Minute}, {"500ms", 30 * time.Second}, {"x", 30 * time.Second}} {
		t.Setenv("GLOSSA_HTTP_TIMEOUT", tc.env)
		if got := httpTimeout(); got != tc.want {
			t.Errorf("GLOSSA_HTTP_TIMEOUT=%q: httpTimeout() = %v, want %v", tc.env, got, tc.want)
		}
	}
}
