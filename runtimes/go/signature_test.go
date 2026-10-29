package glossa

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
)

// signManifest signs raw the way the Release context does: Ed25519 over
// the JCS form without `signatures`.
func signManifest(t *testing.T, raw []byte, keyID string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	canonical, err := canonicalJSONWithout(raw, "signatures")
	if err != nil {
		t.Fatal(err)
	}
	sig := base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, canonical))
	return manifestWithSignatures(t, raw, []any{map[string]any{"keyId": keyID, "alg": "Ed25519", "sig": sig}})
}

func testKey(seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed + byte(i)
	}
	priv := ed25519.NewKeyFromSeed(s)
	return priv.Public().(ed25519.PublicKey), priv
}

func TestParsePublicKey(t *testing.T) {
	pub, _ := testKey(0)
	enc := base64.RawURLEncoding.EncodeToString(pub)
	k, err := ParsePublicKey("k1", enc)
	if err != nil || k.KeyID != "k1" || !k.Key.Equal(pub) {
		t.Fatalf("ParsePublicKey = %+v, %v", k, err)
	}
	if _, err := ParsePublicKey("k1", base64.URLEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("padded base64url must be accepted: %v", err)
	}
	for _, bad := range []string{"", "!!!", base64.RawURLEncoding.EncodeToString(pub[:10])} {
		if _, err := ParsePublicKey("k1", bad); err == nil {
			t.Errorf("ParsePublicKey(%q) succeeded", bad)
		}
	}
	if _, err := ParsePublicKey("", enc); err == nil {
		t.Error("an empty key ID must be rejected")
	}
}

func TestVerifySignatures(t *testing.T) {
	pub, priv := testKey(0)
	_, otherPriv := testKey(100)
	keys := []PublicKey{{KeyID: "k1", Key: pub}}
	unsigned := manifestJSON(t, nil)

	verify := func(raw []byte, keys []PublicKey) error {
		m, err := parseManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		return verifySignatures(raw, m, keys)
	}

	if err := verify(signManifest(t, unsigned, "k1", priv), keys); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verify(unsigned, nil); err != nil {
		t.Fatalf("verification without keys must be skipped: %v", err)
	}
	for name, raw := range map[string][]byte{
		"unsigned":    unsigned,
		"wrong key":   signManifest(t, unsigned, "k1", otherPriv),
		"unknown kid": signManifest(t, unsigned, "k2", priv),
		"tampered":    tamper(t, signManifest(t, unsigned, "k1", priv)),
	} {
		if err := verify(raw, keys); !errors.Is(err, errSignature) {
			t.Errorf("%s: err = %v, want errSignature", name, err)
		}
	}
}

func TestVerifySignaturesAnyConfiguredKey(t *testing.T) {
	pub1, _ := testKey(0)
	pub2, priv2 := testKey(50)
	raw := signManifest(t, manifestJSON(t, nil), "k2", priv2)
	m, _ := parseManifest(raw)
	keys := []PublicKey{{KeyID: "k1", Key: pub1}, {KeyID: "k2", Key: pub2}}
	if err := verifySignatures(raw, m, keys); err != nil {
		t.Fatalf("signature by the second configured key rejected: %v", err)
	}
}

func tamper(t *testing.T, raw []byte) []byte {
	t.Helper()
	return mutateJSON(t, raw, func(m map[string]any) { m["sourceLocale"] = "ar" })
}
