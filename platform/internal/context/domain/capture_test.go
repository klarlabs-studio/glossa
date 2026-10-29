package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

var (
	build       = uuid.MustParse("0192a1b2-0000-7000-8000-0000000000cc")
	imageDigest = domain.Digest(strings.Repeat("ab", 32))
)

func captureInput() domain.CaptureInput {
	return domain.CaptureInput{
		Route: "/checkout/payment", Viewport: domain.Viewport{Width: 1280, Height: 800}, Locale: bcp47.MustParse("de"),
		Image: domain.Image{Digest: imageDigest, Width: 1280, Height: 2400},
		Regions: []domain.Region{
			{Key: "checkout.pay", Kind: domain.RegionElement, Box: domain.Box{X: 10, Y: 20, Width: 120, Height: 40}, Visible: true},
			{Key: "checkout.help", Kind: domain.RegionAttribute, Box: domain.Box{X: 0, Y: 0, Width: 32, Height: 32}, Visible: true},
			{Key: "checkout.legal", Kind: domain.RegionText, Box: domain.Box{X: -400, Y: 0, Width: 0, Height: 0}, Visible: false},
		},
	}
}

func TestNewCapture(t *testing.T) {
	c, err := domain.NewCapture(project, build, captureInput(), "token:ci", now)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID.Version() != 7 || c.ProjectID != project || c.BuildID != build || c.Route != "/checkout/payment" ||
		c.Locale.String() != "de" || c.Image.Digest != imageDigest || len(c.Regions) != 3 || !c.CreatedAt.Equal(now) {
		t.Errorf("capture = %+v", c)
	}
}

func TestNewCaptureRejectsInvalidInput(t *testing.T) {
	cases := map[string]struct {
		change func(*domain.CaptureInput)
		want   error
	}{
		"no route":           {func(in *domain.CaptureInput) { in.Route = "" }, domain.ErrInvalidCapture},
		"long route":         {func(in *domain.CaptureInput) { in.Route = "/" + strings.Repeat("r", domain.MaxRouteLen) }, domain.ErrInvalidCapture},
		"no viewport":        {func(in *domain.CaptureInput) { in.Viewport = domain.Viewport{} }, domain.ErrInvalidCapture},
		"huge viewport":      {func(in *domain.CaptureInput) { in.Viewport.Width = 20000 }, domain.ErrInvalidCapture},
		"no locale":          {func(in *domain.CaptureInput) { in.Locale = bcp47.Tag{} }, domain.ErrInvalidCapture},
		"bad digest":         {func(in *domain.CaptureInput) { in.Image.Digest = "abc" }, domain.ErrInvalidDigest},
		"empty image":        {func(in *domain.CaptureInput) { in.Image.Width = 0 }, domain.ErrInvalidCapture},
		"over 40 megapixel":  {func(in *domain.CaptureInput) { in.Image.Width, in.Image.Height = 8000, 5001 }, domain.ErrInvalidCapture},
		"region without key": {func(in *domain.CaptureInput) { in.Regions[0].Key = "" }, domain.ErrInvalidRegion},
		"region bad kind":    {func(in *domain.CaptureInput) { in.Regions[0].Kind = "pixel" }, domain.ErrInvalidRegion},
		"negative size":      {func(in *domain.CaptureInput) { in.Regions[0].Box.Width = -1 }, domain.ErrInvalidRegion},
		"too many regions": {func(in *domain.CaptureInput) {
			in.Regions = make([]domain.Region, domain.MaxRegionsPerCapture+1)
			for i := range in.Regions {
				in.Regions[i] = domain.Region{Key: "k", Kind: domain.RegionText, Visible: true}
			}
		}, domain.ErrTooManyRegions},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := captureInput()
			tc.change(&in)
			if _, err := domain.NewCapture(project, build, in, "token:ci", now); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCaptureAddsToABuildWithinItsLimit(t *testing.T) {
	if err := domain.CheckCaptureLimit(domain.MaxCapturesPerBuild - 1); err != nil {
		t.Errorf("499 existing: %v", err)
	}
	if err := domain.CheckCaptureLimit(domain.MaxCapturesPerBuild); !errors.Is(err, domain.ErrTooManyCaptures) {
		t.Errorf("500 existing: err = %v, want ErrTooManyCaptures", err)
	}
}

func TestCaptureSameShot(t *testing.T) {
	a, err := domain.NewCapture(project, build, captureInput(), "token:ci", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.NewCapture(project, build, captureInput(), "token:ci", now)
	if err != nil {
		t.Fatal(err)
	}
	if !a.SameShot(b) {
		t.Error("the same route, viewport, locale and image are the same shot")
	}
	other := captureInput()
	other.Image.Digest = domain.Digest(strings.Repeat("cd", 32))
	c, err := domain.NewCapture(project, build, other, "token:ci", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.SameShot(c) {
		t.Error("a different image is a different shot")
	}
}
