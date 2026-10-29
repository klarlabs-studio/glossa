package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func capturesExample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(testdata(t, "schemas", "examples", "captures.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCapturesReadsTheExample(t *testing.T) {
	raw := capturesExample(t)
	up, err := domain.ParseCaptures(raw)
	if err != nil {
		t.Fatal(err)
	}
	if up.Application != "web" || up.Commit.String() != "9f2c1e7a4b3d5c6e8f0a1b2c3d4e5f6a7b8c9d0e" ||
		up.Branch.String() != "feat/checkout-copy" || up.Tool != (domain.Tool{Name: "glossa", Version: "0.9.0"}) {
		t.Errorf("header = %+v", up.Upload)
	}
	canonical, err := jcs.Canonicalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if up.Digest != domain.DigestOf(canonical) {
		t.Errorf("digest = %s, want the SHA-256 of the canonical manifest", up.Digest)
	}
	if len(up.Captures) != 2 {
		t.Fatalf("captures = %d", len(up.Captures))
	}
	c := up.Captures[0]
	if c.Route != "/checkout/[step]" || c.Viewport != (domain.Viewport{Width: 390, Height: 844}) || c.Locale.String() != "de" ||
		c.Image != (domain.Image{Digest: "3b1f0a4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708", Width: 780, Height: 3120}) {
		t.Errorf("capture = %+v", c)
	}
	want := []domain.Region{
		// y 720.5, height 48: the box covers the pixels 720 to 769.
		{Key: "cart.checkout", Kind: domain.RegionElement, Box: domain.Box{X: 16, Y: 720, Width: 358, Height: 49}, Visible: true},
		// A t() string's region names its key through the render log.
		{Key: "checkout.pay", Kind: domain.RegionText, Box: domain.Box{X: 24, Y: 1180, Width: 143, Height: 20}, Visible: true},
		{Key: "checkout.card.placeholder", Kind: domain.RegionAttribute, Box: domain.Box{X: 16, Y: 960, Width: 358, Height: 44}, Visible: true},
		{Key: "checkout.legal", Kind: domain.RegionText, Box: domain.Box{}, Visible: false},
	}
	if fmt.Sprint(c.Regions) != fmt.Sprint(want) {
		t.Errorf("regions = %+v\nwant %+v", c.Regions, want)
	}
	parts := up.Parts()
	if len(parts) != 2 || parts[0].Digest != c.Image.Digest || parts[1].Digest != up.Captures[1].Image.Digest {
		t.Errorf("parts = %+v", parts)
	}
	if keys := up.Keys(); fmt.Sprint(keys) != "[cart.checkout checkout.pay checkout.card.placeholder checkout.legal home.hero.title]" {
		t.Errorf("keys = %v", keys)
	}
}

func TestParseCapturesDigestIgnoresFormattingAndUndefinedMembers(t *testing.T) {
	raw := capturesExample(t)
	first, err := domain.ParseCaptures(raw)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["generated_by"] = "a newer glossa capture"
	compact, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.ParseCaptures(compact)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Errorf("digest changed with formatting or an undefined member: %s vs %s", first.Digest, second.Digest)
	}
}

func TestParseCapturesSharesAnImageBetweenCaptures(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(capturesExample(t), &doc); err != nil {
		t.Fatal(err)
	}
	captures := doc["captures"].([]any)
	second := captures[1].(map[string]any)
	second["image"] = captures[0].(map[string]any)["image"]
	raw, _ := json.Marshal(doc)
	up, err := domain.ParseCaptures(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Parts()) != 1 {
		t.Errorf("parts = %+v, want the shared image once", up.Parts())
	}
}

func TestParseCapturesRefusals(t *testing.T) {
	cases := []struct {
		why  string
		edit func(doc map[string]any)
		want error
	}{
		{"the same shot twice", func(doc map[string]any) {
			c := doc["captures"].([]any)
			doc["captures"] = append(c, c[0])
		}, domain.ErrInvalidCaptures},
		{"one image with two sizes", func(doc map[string]any) {
			c := doc["captures"].([]any)
			img := c[1].(map[string]any)["image"].(map[string]any)
			img["sha256"] = c[0].(map[string]any)["image"].(map[string]any)["sha256"]
		}, domain.ErrInvalidCaptures},
		{"a region index outside the render log", func(doc map[string]any) {
			r := capture0(doc)["regions"].([]any)[1].(map[string]any)
			r["index"] = 9
		}, domain.ErrInvalidCaptures},
		{"a render index twice", func(doc map[string]any) {
			rs := capture0(doc)["renders"].([]any)
			rs[1].(map[string]any)["index"] = 0
		}, domain.ErrInvalidCaptures},
		{"an image over 40 megapixels", func(doc map[string]any) {
			img := capture0(doc)["image"].(map[string]any)
			img["width"], img["height"] = 8000, 5001
		}, domain.ErrInvalidCaptures},
		{"a viewport wider than 10000 pixels", func(doc map[string]any) {
			capture0(doc)["viewport"].(map[string]any)["width"] = 10001
		}, domain.ErrInvalidCaptures},
		{"501 captures", func(doc map[string]any) {
			c := doc["captures"].([]any)
			var many []any
			for i := range 501 {
				shot := map[string]any{}
				for k, v := range c[1].(map[string]any) {
					shot[k] = v
				}
				shot["route"] = fmt.Sprintf("/p/%d", i)
				many = append(many, shot)
			}
			doc["captures"] = many
		}, domain.ErrTooManyCaptures},
		{"10001 regions", func(doc map[string]any) {
			region := capture0(doc)["regions"].([]any)[0]
			regions := make([]any, 10_001)
			for i := range regions {
				regions[i] = region
			}
			capture0(doc)["regions"] = regions
		}, domain.ErrTooManyRegions},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(capturesExample(t), &doc); err != nil {
				t.Fatal(err)
			}
			tc.edit(doc)
			raw, _ := json.Marshal(doc)
			if _, err := domain.ParseCaptures(raw); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	big := []byte(`{"schema":"glossa.captures/v1","pad":"` + strings.Repeat("x", domain.MaxManifestBytes) + `"}`)
	if _, err := domain.ParseCaptures(big); !errors.Is(err, domain.ErrManifestTooLarge) {
		t.Errorf("a manifest over 20 MB: err = %v", err)
	}
	for _, bad := range []string{`{`, `[]`, `{"schema":"glossa.captures/v1"} {}`} {
		if _, err := domain.ParseCaptures([]byte(bad)); !errors.Is(err, domain.ErrInvalidCaptures) {
			t.Errorf("ParseCaptures(%s): err = %v", bad, err)
		}
	}
}

func capture0(doc map[string]any) map[string]any {
	return doc["captures"].([]any)[0].(map[string]any)
}

func TestImageKeyIsContentAddressedPerProject(t *testing.T) {
	d := domain.Digest(strings.Repeat("0f", 32))
	tenant := tenancy.NewID()
	got := domain.ImageKey(tenant, project, d)
	if want := "context/" + tenant.String() + "/" + project.String() + "/img/" + d.String() + ".png"; got != want {
		t.Errorf("ImageKey = %s, want %s", got, want)
	}
}
