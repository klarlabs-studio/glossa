package outbox_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

var noop = outbox.HandlerFunc(func(context.Context, outbox.Delivery) error { return nil })

func TestSubscribe(t *testing.T) {
	r := outbox.NewRegistry()
	if err := r.Subscribe("catalog.source_revised", "localization.mark_outdated", noop); err != nil {
		t.Fatalf("valid subscription: %v", err)
	}
	if err := r.Subscribe("catalog.source_revised", "quality.recheck", noop); err != nil {
		t.Fatalf("second subscriber: %v", err)
	}

	bad := []struct{ typ, sub string }{
		{"catalog.source_revised", "localization.mark_outdated"}, // duplicate
		{"Catalog.SourceRevised", "x"},
		{"catalog.source_revised", ""},
		{"", "x"},
		{"catalog..x", "x"},
	}
	for _, b := range bad {
		if err := r.Subscribe(b.typ, b.sub, noop); !errors.Is(err, outbox.ErrInvalidSubscription) {
			t.Errorf("Subscribe(%q, %q) err = %v, want ErrInvalidSubscription", b.typ, b.sub, err)
		}
	}
	if err := r.Subscribe("a.b", "c", nil); !errors.Is(err, outbox.ErrInvalidSubscription) {
		t.Errorf("nil handler err = %v", err)
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("bad payload")
	err := outbox.Permanent(base)
	if !outbox.IsPermanent(err) || !errors.Is(err, base) {
		t.Errorf("Permanent(%v) lost its identity: %v", base, err)
	}
	if outbox.IsPermanent(base) {
		t.Error("plain error reported as permanent")
	}
	if outbox.Permanent(nil) != nil {
		t.Error("Permanent(nil) != nil")
	}
}

func TestDecodeFailureIsPermanent(t *testing.T) {
	d := outbox.Delivery{Type: "a.b", Payload: []byte(`{"n":"not a number"}`)}
	var v struct{ N int }
	if err := d.Decode(&v); !outbox.IsPermanent(err) {
		t.Errorf("Decode err = %v, want permanent", err)
	}
}
