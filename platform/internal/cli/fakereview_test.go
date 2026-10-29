package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// The fake server's translation reads and reviews (reviewTranslation in
// platform/api/openapi.yaml): GET answers the unit with an ETag (its
// revision), POST …/reviews needs a matching If-Match (412 otherwise),
// moves the unit and appends a revision. Moving to the state a unit is
// in is invalid_transition (409).

type fakeReviews struct {
	// problems makes a review of "key@locale" answer this status and
	// code, as the server would.
	problems map[string]fakeProblem
	// concurrent is how many reads are followed by another writer
	// changing the unit, so the review that follows is stale.
	concurrent int
	// ifMatch records each review's If-Match; reviews records the
	// "key@locale state" of each accepted one.
	ifMatch []string
	reviews []string
}

type fakeProblem struct {
	status int
	code   string
}

func (f *fakeServer) routeReviews(mux *http.ServeMux, p string) {
	f.rv = &fakeReviews{problems: map[string]fakeProblem{}}
	mux.HandleFunc("GET "+p+"/messages/{key}/translations/{locale}", f.getTranslation)
	mux.HandleFunc("POST "+p+"/messages/{key}/translations/{locale}/reviews", f.reviewTranslation)
}

func (f *fakeServer) getTranslation(w http.ResponseWriter, r *http.Request) {
	key, locale := r.PathValue("key"), r.PathValue("locale")
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.translations[locale][key]
	if f.messages[key] == nil || t == nil {
		problemResp(w, 404, "not_found", "no translation")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, t.revision))
	writeJSONResp(w, 200, f.translationJSON(key, locale, t))
	if f.rv.concurrent > 0 {
		f.rv.concurrent--
		t.revision++
	}
}

func (f *fakeServer) reviewTranslation(w http.ResponseWriter, r *http.Request) {
	key, locale := r.PathValue("key"), r.PathValue("locale")
	var body struct{ State string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rv.ifMatch = append(f.rv.ifMatch, r.Header.Get("If-Match"))
	t := f.translations[locale][key]
	if f.messages[key] == nil || t == nil {
		problemResp(w, 404, "not_found", "no translation")
		return
	}
	if pr, ok := f.rv.problems[key+"@"+locale]; ok {
		problemResp(w, pr.status, pr.code, "the fake server says "+pr.code)
		return
	}
	switch body.State {
	case "approved", "rejected", "draft", "needs_review":
	default:
		problemResp(w, 400, "invalid_state", "unknown state "+body.State)
		return
	}
	if r.Header.Get("If-Match") != strconv.Quote(strconv.Itoa(t.revision)) {
		problemResp(w, 412, "precondition_failed", "the translation changed")
		return
	}
	if t.state == body.State {
		problemResp(w, 409, "invalid_transition", "already "+body.State)
		return
	}
	t.state, t.revision = body.State, t.revision+1
	f.rv.reviews = append(f.rv.reviews, key+"@"+locale+" "+body.State)
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, t.revision))
	writeJSONResp(w, 200, f.translationJSON(key, locale, t))
}
