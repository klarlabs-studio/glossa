package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAppJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	tok, err := appJWT(key, 99001, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	var header map[string]string
	decode(t, parts[0], &header)
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("header %v", header)
	}
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	decode(t, parts[1], &claims)
	if claims.Iss != "99001" {
		t.Errorf("iss = %q", claims.Iss)
	}
	if want := now.Add(-60 * time.Second).Unix(); claims.Iat != want {
		t.Errorf("iat = %d, want backdated 60s (%d)", claims.Iat, want)
	}
	if claims.Exp <= now.Unix() || claims.Exp-claims.Iat > 600 {
		t.Errorf("exp = %d: must be in the future and at most 10 min after iat %d", claims.Exp, claims.Iat)
	}
}

func decode(t *testing.T, seg string, v any) {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}
