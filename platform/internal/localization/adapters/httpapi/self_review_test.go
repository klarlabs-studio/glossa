package httpapi

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

func TestTranslationCarriesTheAuthorReviewHint(t *testing.T) {
	de := bcp47.MustParse("de")
	content, err := mfcontent.Parse(mfcontent.MF1, "Hallo", de)
	if err != nil {
		t.Fatal(err)
	}
	view := func(hint *bool) app.TranslationView {
		return app.TranslationView{Translation: domain.Translation{Locale: de, Content: content}, ReviewByAuthorAllowed: hint}
	}
	yes := true
	if got := toTranslation(view(&yes)); got.ReviewByAuthorAllowed == nil || !*got.ReviewByAuthorAllowed {
		t.Errorf("hint lost: %v", got.ReviewByAuthorAllowed)
	}
	if got := toTranslation(view(nil)); got.ReviewByAuthorAllowed != nil {
		t.Errorf("hint invented: %v", *got.ReviewByAuthorAllowed)
	}
}
