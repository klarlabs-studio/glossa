package secrets_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/apps/api/internal/infra/secrets"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSealOpenRoundtrip(t *testing.T) {
	s, err := secrets.New(testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := []byte("sk-test-anthropic-1234567890")
	ct, nonce, err := s.Seal(plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(ct) == 0 || len(nonce) != 12 {
		t.Fatalf("bad sizes: ct=%d nonce=%d", len(ct), len(nonce))
	}
	got, err := s.Open(ct, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plain)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	s, err := secrets.New(testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, n1, _ := s.Seal([]byte("x"))
	_, n2, _ := s.Seal([]byte("x"))
	if string(n1) == string(n2) {
		t.Fatalf("nonce reused across Seals")
	}
}

func TestOpenRejectsTamper(t *testing.T) {
	s, _ := secrets.New(testKey)
	ct, nonce, _ := s.Seal([]byte("secret"))
	ct[0] ^= 0xFF
	if _, err := s.Open(ct, nonce); err == nil {
		t.Fatal("expected auth tag failure on tampered ciphertext")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := secrets.New("tooshort"); err == nil {
		t.Fatal("expected ErrKeyLength")
	}
	if _, err := secrets.New(strings.Repeat("z", 64)); err == nil {
		t.Fatal("expected hex decode error")
	}
}
