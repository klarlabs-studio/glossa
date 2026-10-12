package sources_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/sources"
	mcpapp "go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// The seam between MCP's `translation_propose` and Localization's
// service, and the one invariant of RFC 0005 §7.4: an agent's
// translation always enters review and is never an approved revision.
//
// These tests run the *real* Localization service over a fake store, so
// what they assert is the state the domain's WritePolicy actually
// reached — not the state the adapter said it wanted. The project they
// run against has `review_required` **off**, which is the case that
// would otherwise approve new text outright: a person with
// `translations.write` writing into such a project gets `approved`, and
// that is correct for a person. It must not be what a token gets.

// locStore is Localization's port with the translation-write path
// filled in; anything else panics on a nil interface, which is the bug.
type locStore struct {
	localizationapp.Store
	locales map[bcp47.Tag]bool
	stored  map[bcp47.Tag]localizationdomain.Translation
	// revisions is every revision appended, in order.
	revisions []localizationdomain.Revision
	events    []outbox.Event
}

func newLocStore(locales ...string) *locStore {
	s := &locStore{locales: map[bcp47.Tag]bool{}, stored: map[bcp47.Tag]localizationdomain.Translation{}}
	for _, l := range locales {
		s.locales[bcp47.MustParse(l)] = true
	}
	return s
}

func (s *locStore) InsertLocale(_ context.Context, l localizationdomain.Locale, _ string) (bool, error) {
	if s.locales[l.Code] {
		return false, nil
	}
	s.locales[l.Code] = true
	return true, nil
}

func (s *locStore) Locale(_ context.Context, project uuid.UUID, code bcp47.Tag) (localizationdomain.Locale, error) {
	if !s.locales[code] {
		return localizationdomain.Locale{}, localizationapp.ErrNotFound
	}
	return localizationdomain.NewLocale(project, code, false, time.Now().UTC()), nil
}

func (s *locStore) LockMessageState(context.Context, uuid.UUID) (localizationapp.MessageState, bool, error) {
	return localizationapp.MessageState{}, false, nil
}

func (s *locStore) SaveMessageState(context.Context, localizationapp.MessageState) error { return nil }

func (s *locStore) LockTranslation(
	_ context.Context, _ uuid.UUID, locale bcp47.Tag,
) (localizationdomain.Translation, bool, error) {
	t, found := s.stored[locale]
	return t, found, nil
}

func (s *locStore) InsertTranslation(_ context.Context, t localizationdomain.Translation) error {
	s.stored[t.Locale] = t
	return nil
}

func (s *locStore) UpdateTranslation(_ context.Context, t localizationdomain.Translation, _ int) error {
	s.stored[t.Locale] = t
	return nil
}

func (s *locStore) Translation(
	_ context.Context, _ uuid.UUID, locale bcp47.Tag,
) (localizationapp.TranslationRow, error) {
	t, found := s.stored[locale]
	if !found {
		return localizationapp.TranslationRow{}, localizationapp.ErrNotFound
	}
	return localizationapp.TranslationRow{Translation: t, CurrentSourceRevision: sourceRev}, nil
}

func (s *locStore) AppendRevision(_ context.Context, r localizationdomain.Revision) error {
	s.revisions = append(s.revisions, r)
	return nil
}

func (s *locStore) Publish(_ context.Context, e outbox.Event) error {
	if err := e.Validate(); err != nil { // what the outbox would refuse
		return err
	}
	s.events = append(s.events, e)
	return nil
}

type locTransactor struct{ store *locStore }

func (t locTransactor) InTenant(ctx context.Context, fn func(context.Context, localizationapp.Store) error) error {
	return fn(ctx, t.store)
}

func (t locTransactor) InCurrent(ctx context.Context, fn func(context.Context, localizationapp.Store) error) error {
	return fn(ctx, t.store)
}

// sourceRev is the message's current source revision throughout.
const sourceRev = 3

// locCatalog is Catalog's port: one project with one message.
type locCatalog struct {
	project        uuid.UUID
	reviewRequired bool
}

func (c locCatalog) Project(_ context.Context, id uuid.UUID) (localizationapp.ProjectInfo, error) {
	if id != c.project {
		return localizationapp.ProjectInfo{}, localizationapp.ErrNotFound
	}
	return localizationapp.ProjectInfo{
		ID: c.project, SourceLocale: bcp47.MustParse("en"), DefaultSyntax: mfcontent.MF2,
		ReviewRequired: c.reviewRequired,
	}, nil
}

func (c locCatalog) Message(_ context.Context, project uuid.UUID, key string) (localizationapp.SourceMessage, error) {
	if project != c.project || key != "checkout.pay" {
		return localizationapp.SourceMessage{}, localizationapp.ErrNotFound
	}
	content, err := mfcontent.Parse(mfcontent.MF2, "Pay {$amount}", bcp47.MustParse("en"))
	if err != nil {
		return localizationapp.SourceMessage{}, err
	}
	return localizationapp.SourceMessage{
		ID: messageID, ProjectID: project, Key: key, Namespace: "checkout", State: "active",
		Revision: sourceRev, Version: 1, Content: content,
	}, nil
}

func (c locCatalog) MessagesByKeys(
	ctx context.Context, project uuid.UUID, keys []string,
) (map[string]localizationapp.SourceMessage, error) {
	out := map[string]localizationapp.SourceMessage{}
	for _, k := range keys {
		m, err := c.Message(ctx, project, k)
		if err == nil {
			out[k] = m
		}
	}
	return out, nil
}

func (c locCatalog) SourceAt(
	ctx context.Context, project, _ uuid.UUID, _ int,
) (mfcontent.Content, error) {
	m, err := c.Message(ctx, project, "checkout.pay")
	return m.Content, err
}

func (c locCatalog) SourcesAt(
	ctx context.Context, project uuid.UUID, refs []localizationapp.SourceRef,
) (map[localizationapp.SourceRef]mfcontent.Content, error) {
	out := map[localizationapp.SourceRef]mfcontent.Content{}
	for _, r := range refs {
		content, err := c.SourceAt(ctx, project, r.Message, r.Revision)
		if err != nil {
			return nil, err
		}
		out[r] = content
	}
	return out, nil
}

// messageID is the one message these tests translate.
var messageID = uuid.New()

// newTranslations builds the adapter over the real Localization service
// and a context acting as an API token with the given scopes — the same
// grant an MCP session would run under, which notably never carries
// `translations.review`.
func newTranslations(t *testing.T, reviewRequired bool, scopes ...string) (
	*sources.Translations, *locStore, uuid.UUID, context.Context,
) {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{"read", "write"}
	}
	project := uuid.New()
	store := newLocStore("de")
	svc := localizationapp.New(locTransactor{store: store},
		locCatalog{project: project, reviewRequired: reviewRequired})
	ctx := authztest.Token(context.Background(), tenancy.NewID(), scopes...)
	return sources.NewTranslations(svc), store, project, ctx
}

// proposal is a valid proposal for the seeded message.
func proposal() tools.TranslationProposal {
	return tools.TranslationProposal{Key: "checkout.pay", Locale: "de", Text: "Zahlen {$amount}"}
}

// TestAProposalIsNeverApproved is the invariant. The project does not
// require review, so Localization's own default for new text is
// `approved`; a proposal must still land in `needs_review`.
func TestAProposalIsNeverApproved(t *testing.T) {
	a, store, project, ctx := newTranslations(t, false)

	got, err := a.ProposeTranslation(ctx, project, proposal())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(localizationdomain.StateNeedsReview) {
		t.Fatalf("state = %q, want needs_review", got.State)
	}
	stored := store.stored[bcp47.MustParse("de")]
	if stored.State != localizationdomain.StateNeedsReview {
		t.Fatalf("the stored translation is %q, want needs_review", stored.State)
	}
	if len(store.revisions) != 1 || store.revisions[0].State != localizationdomain.StateNeedsReview {
		t.Fatalf("the revision log says %+v, want one revision in review", store.revisions)
	}
	// The same project, written the way a person writes: approved. That
	// is what makes the assertion above about the adapter and not about
	// a project that happens to require review.
	direct, _, err := localizationapp.New(locTransactor{store: newLocStore("de")},
		locCatalog{project: project}).
		PutTranslation(ctx, project, "checkout.pay", "de",
			localizationapp.TranslationInput{Text: "Zahlen {$amount}"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if direct.State != localizationdomain.StateApproved {
		t.Fatalf("the control write is %q; the test no longer proves anything", direct.State)
	}
}

// TestAProposalIsNeverApprovedWhereReviewIsRequired: the other project
// setting reaches the same place, so neither setting can change it.
func TestAProposalIsNeverApprovedWhereReviewIsRequired(t *testing.T) {
	a, store, project, ctx := newTranslations(t, true)
	if _, err := a.ProposeTranslation(ctx, project, proposal()); err != nil {
		t.Fatal(err)
	}
	if got := store.stored[bcp47.MustParse("de")].State; got != localizationdomain.StateNeedsReview {
		t.Fatalf("state = %q, want needs_review", got)
	}
}

// TestRevisingAnApprovedTranslationSendsItBackToReview: an agent may
// not leave approved text approved by writing over it. New text is
// re-decided, and for a proposal the decision is always review.
func TestRevisingAnApprovedTranslationSendsItBackToReview(t *testing.T) {
	a, store, project, ctx := newTranslations(t, false)
	// Someone approved a translation first, the way a reviewer would.
	svc := localizationapp.New(locTransactor{store: store}, locCatalog{project: project})
	approved, _, err := svc.PutTranslation(ctx, project, "checkout.pay", "de",
		localizationapp.TranslationInput{Text: "Bezahlen {$amount}"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != localizationdomain.StateApproved {
		t.Fatalf("the seeded translation is %q, want approved", approved.State)
	}

	got, err := a.ProposeTranslation(ctx, project, proposal())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(localizationdomain.StateNeedsReview) {
		t.Fatalf("state = %q, want needs_review: an approval is not inherited", got.State)
	}
	if store.stored[bcp47.MustParse("de")].State != localizationdomain.StateNeedsReview {
		t.Fatal("the stored translation stayed approved")
	}
}

// TestRepeatingAProposalDoesNotUnapproveIt: an agent that proposes
// exactly what is already there changes nothing. The invariant is
// "never approved by the agent", not "always back to review": pushing
// an approved translation out of its approval because a tool repeated
// itself would make an idempotent call destructive.
func TestRepeatingAProposalDoesNotUnapproveIt(t *testing.T) {
	a, store, project, ctx := newTranslations(t, false)
	svc := localizationapp.New(locTransactor{store: store}, locCatalog{project: project})
	approved, _, err := svc.PutTranslation(ctx, project, "checkout.pay", "de",
		localizationapp.TranslationInput{Text: "Zahlen {$amount}"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != localizationdomain.StateApproved {
		t.Fatalf("the seeded translation is %q, want approved", approved.State)
	}

	got, err := a.ProposeTranslation(ctx, project, proposal())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != string(localizationapp.WriteUnchanged) {
		t.Fatalf("status = %q, want unchanged", got.Status)
	}
	if store.stored[bcp47.MustParse("de")].State != localizationdomain.StateApproved {
		t.Fatal("repeating the approved text sent it back to review")
	}
	if len(store.revisions) != 1 {
		t.Fatalf("revisions = %d, want only the approval's", len(store.revisions))
	}
}

// TestAProposalIsRecordedAsAgentWritten: the provenance says an agent
// wrote the text — not a person, and not a person asking for AI (RFC
// 0005 §7.3). `agent` is the origin itself, so the revision log can be
// filtered on it; the detail still says which agent surface and tool,
// and still names no provider.
func TestAProposalIsRecordedAsAgentWritten(t *testing.T) {
	a, store, project, ctx := newTranslations(t, false)
	got, err := a.ProposeTranslation(ctx, project, proposal())
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != string(localizationdomain.OriginAgent) {
		t.Errorf("origin = %q, want agent", got.Origin)
	}
	if got.Origin == string(localizationdomain.OriginAI) {
		t.Error("an agent's write is recorded as `ai`, which is what this change undoes")
	}
	prov := store.revisions[0].Provenance
	if prov.Origin != localizationdomain.OriginAgent {
		t.Errorf("the revision's origin = %q, want agent", prov.Origin)
	}
	if string(prov.Detail) != `{"via":"mcp","tool":"translation_propose"}` {
		t.Errorf("origin detail = %s", prov.Detail)
	}
	// The token is the author, "token:<uuid>", as the ledger spells it.
	if got.By == "" || got.By[:6] != "token:" {
		t.Errorf("by = %q, want the API token", got.By)
	}
}

// TestAProposalOntoAStaleRevisionIsRefused: an agent that read the
// translation, then proposed against a revision that has since moved,
// is told to read it again rather than overwriting the newer text.
func TestAProposalOntoAStaleRevisionIsRefused(t *testing.T) {
	a, _, project, ctx := newTranslations(t, false)
	if _, err := a.ProposeTranslation(ctx, project, proposal()); err != nil {
		t.Fatal(err)
	}
	stale := 99
	p := proposal()
	p.BaseRevision = &stale
	_, err := a.ProposeTranslation(ctx, project, p)
	var invalid *mcpapp.InvalidArgumentError
	if !errors.As(err, &invalid) || invalid.Argument != "base_revision" {
		t.Fatalf("err = %v, want an invalid base_revision", err)
	}
}

// TestAProposalWithoutABaseRevisionProposesOntoWhatStands: an agent
// that did not read first still writes, and does not have to learn
// If-Match semantics to do it.
func TestAProposalWithoutABaseRevisionProposesOntoWhatStands(t *testing.T) {
	a, store, project, ctx := newTranslations(t, false)
	if _, err := a.ProposeTranslation(ctx, project, proposal()); err != nil {
		t.Fatal(err)
	}
	second := proposal()
	second.Text = "Jetzt zahlen {$amount}"
	got, err := a.ProposeTranslation(ctx, project, second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != string(localizationapp.WriteRevised) || got.Revision != 2 {
		t.Fatalf("status = %q at revision %d, want a revision of the first", got.Status, got.Revision)
	}
	if store.stored[bcp47.MustParse("de")].Content.Text != "Jetzt zahlen {$amount}" {
		t.Fatal("the second proposal did not land")
	}
}

// TestProposingIntoTheSourceLocaleIsAnArgumentError: source text is
// written with message_upsert, and saying so is more useful than a
// storage error.
func TestProposingIntoTheSourceLocaleIsAnArgumentError(t *testing.T) {
	a, _, project, ctx := newTranslations(t, false)
	p := proposal()
	p.Locale = "en"
	_, err := a.ProposeTranslation(ctx, project, p)
	var invalid *mcpapp.InvalidArgumentError
	if !errors.As(err, &invalid) || invalid.Argument != "locale" {
		t.Fatalf("err = %v, want an invalid locale", err)
	}
}

// TestProposingIntoAnUnknownProjectIsNotFound: another tenant's project
// is not there, and MCP never says forbidden-with-detail.
func TestProposingIntoAnUnknownProjectIsNotFound(t *testing.T) {
	a, _, _, ctx := newTranslations(t, false)
	if _, err := a.ProposeTranslation(ctx, uuid.New(), proposal()); !errors.Is(err, tools.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestALocaleThePprojectDoesNotHaveIsNotFound: locale_add comes first.
func TestALocaleTheProjectDoesNotHaveIsNotFound(t *testing.T) {
	a, _, project, ctx := newTranslations(t, false)
	p := proposal()
	p.Locale = "fr"
	if _, err := a.ProposeTranslation(ctx, project, p); !errors.Is(err, tools.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestAddLocaleIsIdempotent: adding a locale the project has changes
// nothing and is not an error, so an agent need not check first.
func TestAddLocaleIsIdempotent(t *testing.T) {
	a, _, project, ctx := newTranslations(t, false)
	first, err := a.AddLocale(ctx, project, "pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Locale != "pt-BR" {
		t.Fatalf("added = %+v, want a created pt-BR", first)
	}
	again, err := a.AddLocale(ctx, project, "pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	if again.Created {
		t.Fatal("adding it twice reported a second creation")
	}
}
