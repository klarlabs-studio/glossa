package passkey_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/passkey"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

type repo struct{ creds []authgo.PasskeyCredential }

func (r *repo) Add(context.Context, authgo.PasskeyCredential) error { return nil }
func (r *repo) ListByUser(context.Context, authgo.UserID) ([]authgo.PasskeyCredential, error) {
	return r.creds, nil
}
func (r *repo) UpdateSignCount(context.Context, []byte, uint32) error { return nil }
func (r *repo) Delete(context.Context, []byte) error                  { return nil }

func config() passkey.Config {
	return passkey.Config{
		RPID: "glossa.test", RPName: "Glossa", Origins: []string{"https://app.glossa.test"},
		StateKey: []byte(strings.Repeat("k", 32)),
	}
}

func TestNewRefusesAShortStateKey(t *testing.T) {
	cfg := config()
	cfg.StateKey = []byte("short")
	if _, err := passkey.New(cfg, &repo{}); err == nil {
		t.Error("a state key under 32 bytes was accepted")
	}
}

func TestBeginLoginWithoutPasskeysIsErrNoPasskeys(t *testing.T) {
	a, err := passkey.New(config(), &repo{})
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := authgo.NewUserID("0190c0de-0000-7000-8000-000000000001")
	if _, _, err := a.BeginLogin(context.Background(), uid); !errors.Is(err, app.ErrNoPasskeys) {
		t.Errorf("err = %v, want app.ErrNoPasskeys", err)
	}
}

func TestBeginRegistrationReturnsCreationOptions(t *testing.T) {
	a, err := passkey.New(config(), &repo{})
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := authgo.NewUserID("0190c0de-0000-7000-8000-000000000001")
	options, state, err := a.BeginRegistration(context.Background(), uid, "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		PublicKey struct {
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	if o.PublicKey.RP.ID != "glossa.test" || o.PublicKey.Challenge == "" || len(state) == 0 {
		t.Errorf("options = %s", options)
	}
}
