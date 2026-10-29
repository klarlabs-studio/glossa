package sealing_test

import (
	"bytes"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/sealing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := sealing.New(key(1))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal([]byte("sk-secret"), "tenant-1", "provider-9")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("sk-secret")) {
		t.Fatal("the plaintext is visible in the sealed value")
	}
	again, _ := s.Seal([]byte("sk-secret"), "tenant-1", "provider-9")
	if bytes.Equal(sealed, again) {
		t.Error("sealing is randomized: equal secrets must not give equal ciphertexts")
	}
	got, err := s.Open(sealed, "tenant-1", "provider-9")
	if err != nil || string(got) != "sk-secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
}

func TestOpenRefusesTamperingWrongContextAndWrongKey(t *testing.T) {
	s, _ := sealing.New(key(1))
	sealed, _ := s.Seal([]byte("sk-secret"), "tenant-1", "provider-9")

	if _, err := s.Open(sealed, "tenant-2", "provider-9"); !errors.Is(err, sealing.ErrOpen) {
		t.Errorf("another tenant's context opened it: %v", err)
	}
	// The context parts are length-prefixed: shifting a boundary fails.
	if _, err := s.Open(sealed, "tenant-1provider-9"); !errors.Is(err, sealing.ErrOpen) {
		t.Errorf("a concatenated context opened it: %v", err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	if _, err := s.Open(tampered, "tenant-1", "provider-9"); !errors.Is(err, sealing.ErrOpen) {
		t.Errorf("tampered: %v", err)
	}
	other, _ := sealing.New(key(2))
	if _, err := other.Open(sealed, "tenant-1", "provider-9"); !errors.Is(err, sealing.ErrOpen) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := s.Open([]byte{1, 2}, "x"); !errors.Is(err, sealing.ErrOpen) {
		t.Errorf("short: %v", err)
	}
}

func TestNewRejectsShortKeys(t *testing.T) {
	if _, err := sealing.New(make([]byte, 16)); err == nil {
		t.Error("a 128-bit key must be refused: secrets are sealed with AES-256")
	}
}
