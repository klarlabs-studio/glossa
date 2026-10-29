package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The branch overlay (RFC 0004 §4.1). A branch push proposes; only the
// default branch's push (UpsertMessages) makes anything live.

// MaxBranchItems bounds a branch push: a branch's whole catalog.
const MaxBranchItems = 10000

// ErrTooManyBranchItems: a branch push holds 1 to MaxBranchItems items.
var ErrTooManyBranchItems = errors.New("catalog: a branch push holds 1 to 10000 items")

// ErrInvalidBranchState: the branch list's state filter is open, merged
// or closed.
var ErrInvalidBranchState = errors.New("catalog: a branch is open, merged or closed")

// BranchPush is a push from a feature branch (glossa push --branch).
type BranchPush struct {
	Branch string
	domain.PushInfo
	// Items are the branch's messages. BaseRevision is ignored: a branch
	// never overwrites anyone's edit.
	Items []UpsertItem
	// Complete says Items are the branch's whole catalog: the keys it
	// proposed before and no longer has are withdrawn, and the live
	// keys it lacks are reported as removed. A partial push only adds.
	Complete bool
}

// BranchItemStatus is what a branch push did with one item.
type BranchItemStatus string

// Branch item statuses.
const (
	// BranchNewKey: the key is new; the branch proposes its message
	// (created, or shared with branches proposing the same source).
	BranchNewKey BranchItemStatus = "new_key"
	// BranchSourceProposal: the branch proposes a change to a live
	// message's source.
	BranchSourceProposal BranchItemStatus = "source_proposal"
	// BranchUnchanged: the live source already says this.
	BranchUnchanged BranchItemStatus = "unchanged"
	// BranchKeyConflict: another open branch proposes the same new key
	// with different source.
	BranchKeyConflict BranchItemStatus = "key_conflict"
	BranchItemFailed  BranchItemStatus = "failed"
)

// BranchItemResult reports one item, in request order.
type BranchItemResult struct {
	Key     string
	Status  BranchItemStatus
	Message *domain.Message
	Error   *ItemError
}

// KeyConflict is a new key the branch proposes with different source
// than other open branches do. Every one of them reports it.
type KeyConflict struct {
	Key      domain.MessageKey
	Branches []domain.BranchName
}

// BranchReport is a branch's status: what it proposes, what it would
// remove, what conflicts, and what merging it would make outdated.
type BranchReport struct {
	Branch domain.Branch
	// Items is the push's per-item result (a push's report only).
	Items           []BranchItemResult
	NewKeys         []domain.MessageKey
	SourceProposals []domain.MessageKey
	Removed         []domain.MessageKey
	// Invalid is what the branch's last push could not accept. A status
	// report has it too, not only the push's own answer: the Glossa PR
	// check runs minutes later and must still be able to say what was
	// wrong (RFC 0004 §6.4).
	Invalid   []domain.InvalidItem
	Conflicts []KeyConflict
	// Outdated counts, per locale, the translations the branch's source
	// proposals will make outdated when it merges; nil without the
	// translation impact port.
	Outdated map[string]int

	conflictingBranches []domain.BranchID
	changedMessages     []domain.MessageID
}

// PushBranch applies a branch's push in one transaction: new keys become
// proposed messages the branch owns, changed source for live keys
// becomes source proposals, and nothing live changes. The branch is
// created on its first push; a push on a closed branch reopens it.
func (s *Service) PushBranch(ctx context.Context, project domain.ProjectID, in BranchPush) (BranchReport, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return BranchReport{}, err
	}
	name, err := domain.ParseBranchName(in.Branch)
	if err != nil {
		return BranchReport{}, err
	}
	if len(in.Items) == 0 || len(in.Items) > MaxBranchItems {
		return BranchReport{}, ErrTooManyBranchItems
	}
	var rep BranchReport
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		p, err := st.Project(ctx, project)
		if err != nil {
			return err
		}
		b, created, err := s.lockOrCreateBranch(ctx, st, project, name, by)
		if err != nil {
			return err
		}
		expected, now := b.Version, s.now()
		reopened, err := b.Push(in.PushInfo, now)
		if err != nil {
			return err
		}
		w := &branchWrite{s: s, st: st, b: &b, by: by, now: now}
		if created {
			if err := st.Publish(ctx, branchEvent(domain.EventBranchOpened, b, by)); err != nil {
				return err
			}
		}
		if reopened {
			if err := w.reproposeOwned(ctx); err != nil {
				return err
			}
		}
		if rep.Items, err = w.apply(ctx, p, in.Items); err != nil {
			return err
		}
		// What the push could not accept is remembered on the branch, so
		// the PR check can report it long after the push's answer is gone.
		b.Invalid = invalidItems(rep.Items)
		if in.Complete {
			if err := w.withdrawMissing(ctx, in.Items); err != nil {
				return err
			}
		}
		if err := st.UpdateBranch(ctx, b, expected); err != nil {
			return err
		}
		if err := w.project(ctx); err != nil {
			return err
		}
		items := rep.Items
		if rep, err = s.report(ctx, st, b); err != nil {
			return err
		}
		rep.Items = items
		if reopened {
			if err := st.Publish(ctx, branchEvent(domain.EventBranchReopened, b, by)); err != nil {
				return err
			}
		}
		return st.Publish(ctx, branchPushedEvent(rep, by))
	})
	if err != nil {
		return BranchReport{}, err
	}
	return rep, s.countOutdated(ctx, project, &rep)
}

// lockOrCreateBranch locks the branch, creating it if the project has
// none of that name; created says a row was inserted (the caller
// publishes catalog.branch.opened, which opens its environment).
func (s *Service) lockOrCreateBranch(ctx context.Context, st Store, project domain.ProjectID, name domain.BranchName, by domain.Author) (domain.Branch, bool, error) {
	b, err := st.LockBranch(ctx, project, name)
	if !errors.Is(err, ErrNotFound) {
		return b, false, err
	}
	nb, err := domain.NewBranch(project, name, s.now())
	if err != nil {
		return domain.Branch{}, false, err
	}
	// A concurrent first push may win the insert; lock its row instead.
	inserted, err := st.InsertBranch(ctx, nb, by)
	if err != nil {
		return domain.Branch{}, false, err
	}
	b, err = st.LockBranch(ctx, project, name)
	return b, inserted, err
}

// BranchUpsert is what CI (or the pull_request webhook) says about a
// branch without pushing messages: glossa push --branch --pr sends the
// same fields with its items.
type BranchUpsert struct {
	Branch string
	domain.PushInfo
	// PreviewURL, when set, records where CI deployed the branch's
	// preview ("" clears it).
	PreviewURL *string
}

// UpsertBranch creates a branch or records what CI says about an
// existing one (its head commit, its pull request, its preview URL).
// It proposes nothing: only a push does. A closed branch reopens, a
// merged one is refused (domain.ErrBranchMerged).
func (s *Service) UpsertBranch(ctx context.Context, project domain.ProjectID, in BranchUpsert) (domain.Branch, bool, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Branch{}, false, err
	}
	name, err := domain.ParseBranchName(in.Branch)
	if err != nil {
		return domain.Branch{}, false, err
	}
	var (
		b       domain.Branch
		created bool
	)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		b, created, err = s.lockOrCreateBranch(ctx, st, project, name, by)
		if err != nil {
			return err
		}
		expected, now := b.Version, s.now()
		reopened, err := b.Push(in.PushInfo, now)
		if err != nil {
			return err
		}
		if in.PreviewURL != nil {
			if _, err := b.ReportPreview(*in.PreviewURL, now); err != nil {
				return err
			}
		}
		if err := st.UpdateBranch(ctx, b, expected); err != nil {
			return err
		}
		if created {
			return st.Publish(ctx, branchEvent(domain.EventBranchOpened, b, by))
		}
		if reopened {
			return st.Publish(ctx, branchEvent(domain.EventBranchReopened, b, by))
		}
		return nil
	})
	if err != nil {
		return domain.Branch{}, false, err
	}
	return b, created, nil
}

// BranchFilter narrows a branch list.
type BranchFilter struct {
	// State lists only branches in it ("" is every state).
	State domain.BranchState
	// Name lists only the branch of that name (the CLI resolving a
	// branch it knows by name).
	Name string
}

// ListBranches lists a project's branches by name.
func (s *Service) ListBranches(ctx context.Context, project domain.ProjectID, f BranchFilter, page pagination.Page) ([]domain.Branch, *string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, nil, err
	}
	if f.State != "" && f.State != domain.BranchOpen && f.State != domain.BranchMerged && f.State != domain.BranchClosed {
		return nil, nil, ErrInvalidBranchState
	}
	var rows []domain.Branch
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		if f.Name != "" {
			return s.branchByName(ctx, st, project, f, &rows)
		}
		var err error
		rows, err = st.Branches(ctx, project, f.State, domain.BranchName(page.After), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(b domain.Branch) string { return string(b.Name) })
	return items, next, nil
}

// branchByName answers the name filter: the one branch of that name, or
// nothing (an unknown or invalid name is an empty page, never an error:
// it is a filter, not a lookup).
func (s *Service) branchByName(ctx context.Context, st Store, project domain.ProjectID, f BranchFilter, out *[]domain.Branch) error {
	name, err := domain.ParseBranchName(f.Name)
	if err != nil {
		return nil
	}
	b, err := st.Branch(ctx, project, name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil || (f.State != "" && b.State != f.State) {
		return err
	}
	*out = append(*out, b)
	return nil
}

// GetBranch reads one branch by ID.
func (s *Service) GetBranch(ctx context.Context, project domain.ProjectID, id domain.BranchID) (domain.Branch, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return domain.Branch{}, err
	}
	var b domain.Branch
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		b, err = st.BranchByID(ctx, project, id)
		return err
	})
	return b, err
}

// ListProposals lists a branch's proposals by key: what it proposes for
// each key, and the message that key names.
func (s *Service) ListProposals(ctx context.Context, project domain.ProjectID, branch string, page pagination.Page) ([]domain.Proposal, *string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, nil, err
	}
	name, err := parseBranchOrNotFound(branch)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Proposal
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		b, err := st.Branch(ctx, project, name)
		if err != nil {
			return err
		}
		rows, err = st.BranchProposalsPage(ctx, b.ID, domain.MessageKey(page.After), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(p domain.Proposal) string { return string(p.Key) })
	return items, next, nil
}

// OpenBranchesProposing lists the names of the project's open branches
// that propose message id — the branches whose preview shows a change
// to it, and so the ones to publish again when its translations change
// (RFC 0004 §4.2). Release reads it through its Source port.
func (s *Service) OpenBranchesProposing(ctx context.Context, project domain.ProjectID, id domain.MessageID) ([]domain.BranchName, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, err
	}
	var out []domain.BranchName
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		all, err := st.ProposalsForMessages(ctx, []domain.MessageID{id})
		if err != nil || len(all) == 0 {
			return err
		}
		ids := make([]domain.BranchID, 0, len(all))
		for _, pr := range all {
			ids = append(ids, pr.BranchID)
		}
		branches, err := st.BranchesByIDs(ctx, ids)
		if err != nil {
			return err
		}
		for _, b := range branches {
			if b.State == domain.BranchOpen && b.ProjectID == project {
				out = append(out, b.Name)
			}
		}
		slices.Sort(out)
		return nil
	})
	return out, err
}

// branchWrite is one branch change in progress: it collects the messages
// it changed for Localization's projection.
type branchWrite struct {
	s       *Service
	st      Store
	b       *domain.Branch
	by      domain.Author
	now     time.Time
	changed []domain.Message
}

func (w *branchWrite) apply(ctx context.Context, p domain.Project, items []UpsertItem) ([]BranchItemResult, error) {
	keys, prepared := w.s.prepareUpserts(p, items)
	existing, err := w.st.LockMessagesByKeys(ctx, p.ID, keys)
	if err != nil {
		return nil, err
	}
	var ids []domain.MessageID
	for _, m := range existing {
		ids = append(ids, m.ID)
	}
	all, err := w.st.ProposalsForMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	others, err := w.openProposalsOfOthers(ctx, all)
	if err != nil {
		return nil, err
	}
	own, err := w.st.BranchProposals(ctx, w.b.ID)
	if err != nil {
		return nil, err
	}
	ownByKey := map[domain.MessageKey]domain.Proposal{}
	for _, pr := range own {
		ownByKey[pr.Key] = pr
	}
	results := make([]BranchItemResult, len(items))
	for i, it := range prepared {
		if it.err != nil {
			results[i] = BranchItemResult{Key: items[i].Key, Status: BranchItemFailed, Error: it.err}
			continue
		}
		m, found := existing[it.key]
		var res BranchItemResult
		switch {
		case !found:
			res, err = w.createProposed(ctx, p, it)
		case m.State == domain.MessageActive:
			res, err = w.proposeChange(ctx, m, it, ownByKey)
		default:
			res, err = w.proposeKey(ctx, m, it, others[m.ID], ownByKey)
		}
		if err != nil {
			return nil, err
		}
		res.Key = items[i].Key
		results[i] = res
	}
	return results, nil
}

// openProposalsOfOthers groups other open branches' new-key proposals
// by message.
func (w *branchWrite) openProposalsOfOthers(ctx context.Context, all []domain.Proposal) (map[domain.MessageID][]domain.Proposal, error) {
	var branchIDs []domain.BranchID
	for _, pr := range all {
		if pr.BranchID != w.b.ID {
			branchIDs = append(branchIDs, pr.BranchID)
		}
	}
	branches, err := w.st.BranchesByIDs(ctx, branchIDs)
	if err != nil {
		return nil, err
	}
	out := map[domain.MessageID][]domain.Proposal{}
	for _, pr := range all {
		if pr.BranchID != w.b.ID && pr.Kind == domain.ProposalNewKey && branches[pr.BranchID].State == domain.BranchOpen {
			out[pr.MessageID] = append(out[pr.MessageID], pr)
		}
	}
	return out, nil
}

// createProposed adds a message the branch proposes.
func (w *branchWrite) createProposed(ctx context.Context, p domain.Project, it preparedItem) (BranchItemResult, error) {
	d, ierr := it.details(domain.Details{Namespace: domain.DefaultNamespace})
	if ierr != nil {
		return BranchItemResult{Status: BranchItemFailed, Error: ierr}, nil
	}
	m, first, err := domain.NewProposedMessage(p.ID, it.key, d, it.content, w.by, w.now)
	if err != nil {
		return BranchItemResult{}, err
	}
	if _, err := w.st.InsertMessage(ctx, m, first, w.by); err != nil {
		return BranchItemResult{}, err
	}
	if err := w.st.Publish(ctx, messageEvent(domain.EventMessageCreated, m, w.by)); err != nil {
		return BranchItemResult{}, err
	}
	w.changed = append(w.changed, m)
	pr := domain.ProposeNewKey(w.b.ID, m, it.content, w.by, w.now)
	return BranchItemResult{Status: BranchNewKey, Message: &m}, w.st.SaveProposal(ctx, pr)
}

// proposeChange records changed source for a live message as a source
// proposal, never a revision: the live source stays what it is.
func (w *branchWrite) proposeChange(ctx context.Context, m domain.Message, it preparedItem, own map[domain.MessageKey]domain.Proposal) (BranchItemResult, error) {
	res := BranchItemResult{Message: &m}
	prev, had := own[it.key]
	if m.Source.SameModel(it.content) {
		res.Status = BranchUnchanged
		if !had {
			return res, nil
		}
		return res, w.st.DeleteProposal(ctx, w.b.ID, it.key)
	}
	res.Status = BranchSourceProposal
	pr := domain.ProposeSourceChange(w.b.ID, m, it.content, w.by, w.now)
	if had && prev.Kind == domain.ProposalSourceChange && prev.MessageID == m.ID {
		pr = prev
		if !pr.Update(it.content, m.Revision, w.now) {
			return res, nil
		}
		pr.Author = w.by
	}
	return res, w.st.SaveProposal(ctx, pr)
}

// proposeKey handles a key whose message is proposed (or obsolete): the
// branch shares it unless another open branch proposes it with
// different source (a conflict for both). Otherwise the message takes
// the branch's source, and an obsolete one is proposed again with its
// translations and history.
func (w *branchWrite) proposeKey(ctx context.Context, m domain.Message, it preparedItem, others []domain.Proposal, own map[domain.MessageKey]domain.Proposal) (BranchItemResult, error) {
	res := BranchItemResult{Message: &m, Status: BranchNewKey}
	pr := domain.ProposeNewKey(w.b.ID, m, it.content, w.by, w.now)
	if prev, had := own[it.key]; had {
		pr = prev
		pr.MessageID, pr.Kind, pr.Author = m.ID, domain.ProposalNewKey, w.by
		pr.Update(it.content, 0, w.now)
	}
	if slices.ContainsFunc(others, func(o domain.Proposal) bool { return !o.Source.SameModel(it.content) }) {
		res.Status = BranchKeyConflict
		return res, w.st.SaveProposal(ctx, pr)
	}
	if err := w.takeKey(ctx, &m, it); err != nil {
		return res, err
	}
	return res, w.st.SaveProposal(ctx, pr)
}

// takeKey makes a proposed or obsolete message say what the branch
// proposes.
func (w *branchWrite) takeKey(ctx context.Context, m *domain.Message, it preparedItem) error {
	expected, oldRev := m.Version, m.Revision
	reproposed := m.Repropose(w.now)
	detailsChanged := false
	if d, ierr := it.details(m.Details); ierr == nil {
		detailsChanged, _ = m.ChangeDetails(d, w.now)
	}
	rev, revised, err := m.ReviseSource(it.content, w.by, w.now)
	if err != nil || (!reproposed && !detailsChanged && !revised) {
		return err
	}
	if err := w.st.UpdateMessage(ctx, *m, expected); err != nil {
		return err
	}
	w.changed = append(w.changed, *m)
	if reproposed {
		if err := w.st.Publish(ctx, messageEvent(domain.EventMessageProposed, *m, w.by)); err != nil {
			return err
		}
	}
	if detailsChanged {
		if err := w.st.Publish(ctx, messageEvent(domain.EventMessageUpdated, *m, w.by)); err != nil {
			return err
		}
	}
	if !revised {
		return nil
	}
	if err := w.st.AppendSourceRevision(ctx, rev); err != nil {
		return err
	}
	return w.st.Publish(ctx, sourceRevisedEvent(*m, oldRev, w.by))
}

// withdrawMissing drops the branch's proposals for keys its complete
// push no longer has, obsoletes the proposed messages nobody proposes
// any more, and records the live keys it lacks.
func (w *branchWrite) withdrawMissing(ctx context.Context, items []UpsertItem) error {
	pushed := map[domain.MessageKey]bool{}
	var keys []domain.MessageKey
	for _, it := range items {
		pushed[domain.MessageKey(it.Key)] = true
		keys = append(keys, domain.MessageKey(it.Key))
	}
	own, err := w.st.BranchProposals(ctx, w.b.ID)
	if err != nil {
		return err
	}
	var dropped []domain.MessageID
	for _, pr := range own {
		if pushed[pr.Key] {
			continue
		}
		if err := w.st.DeleteProposal(ctx, w.b.ID, pr.Key); err != nil {
			return err
		}
		if pr.Kind == domain.ProposalNewKey {
			dropped = append(dropped, pr.MessageID)
		}
	}
	if err := w.obsoleteUnproposed(ctx, dropped); err != nil {
		return err
	}
	removed, err := w.st.ActiveKeysExcept(ctx, w.b.ProjectID, keys)
	w.b.RemovedKeys = removed
	return err
}

// obsoleteUnproposed obsoletes the proposed messages among ids that no
// branch proposes any more.
func (w *branchWrite) obsoleteUnproposed(ctx context.Context, ids []domain.MessageID) error {
	if len(ids) == 0 {
		return nil
	}
	ms, err := w.st.LockMessagesByIDs(ctx, w.b.ProjectID, ids)
	if err != nil {
		return err
	}
	left, err := w.st.ProposalsForMessages(ctx, ids)
	if err != nil {
		return err
	}
	for _, m := range sortedByKey(ms) {
		proposed := slices.ContainsFunc(left, func(p domain.Proposal) bool {
			return p.MessageID == m.ID && p.Kind == domain.ProposalNewKey
		})
		if proposed || m.State != domain.MessageProposed {
			continue
		}
		if err := w.obsolete(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (w *branchWrite) obsolete(ctx context.Context, m domain.Message) error {
	expected := m.Version
	if !m.Obsolete(w.now) {
		return nil
	}
	if err := w.st.UpdateMessage(ctx, m, expected); err != nil {
		return err
	}
	w.changed = append(w.changed, m)
	return w.st.Publish(ctx, messageEvent(domain.EventMessageObsoleted, m, w.by))
}

// reproposeOwned brings the obsolete messages the branch proposes back
// to proposed: a reopened branch picks up where it left off.
func (w *branchWrite) reproposeOwned(ctx context.Context) error {
	own, err := w.st.BranchProposals(ctx, w.b.ID)
	if err != nil {
		return err
	}
	var ids []domain.MessageID
	for _, pr := range own {
		if pr.Kind == domain.ProposalNewKey {
			ids = append(ids, pr.MessageID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ms, err := w.st.LockMessagesByIDs(ctx, w.b.ProjectID, ids)
	if err != nil {
		return err
	}
	for _, m := range sortedByKey(ms) {
		expected := m.Version
		if !m.Repropose(w.now) {
			continue
		}
		if err := w.st.UpdateMessage(ctx, m, expected); err != nil {
			return err
		}
		w.changed = append(w.changed, m)
		if err := w.st.Publish(ctx, messageEvent(domain.EventMessageProposed, m, w.by)); err != nil {
			return err
		}
	}
	return nil
}

// project brings Localization's projection up to date in the same
// transaction (see MessageProjection).
func (w *branchWrite) project(ctx context.Context) error {
	if w.s.projection == nil || len(w.changed) == 0 {
		return nil
	}
	return w.s.projection.MessagesChanged(ctx, latestByID(w.changed))
}

// latestByID keeps each message's last state.
func latestByID(ms []domain.Message) []domain.Message {
	last := map[domain.MessageID]int{}
	for i, m := range ms {
		last[m.ID] = i
	}
	var out []domain.Message
	for i, m := range ms {
		if last[m.ID] == i {
			out = append(out, m)
		}
	}
	return out
}

func sortedByKey(ms map[domain.MessageID]domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(ms))
	for _, m := range ms {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b domain.Message) int { return compareKeys(a.Key, b.Key) })
	return out
}

func compareKeys(a, b domain.MessageKey) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// invalidItems keeps the push's rejected items, bounded: they are a
// report for the pull request, not a log.
func invalidItems(items []BranchItemResult) []domain.InvalidItem {
	var out []domain.InvalidItem
	for _, it := range items {
		if it.Status != BranchItemFailed || it.Error == nil {
			continue
		}
		if len(out) == domain.MaxInvalidItems {
			break
		}
		out = append(out, domain.InvalidItem{Key: it.Key, Code: it.Error.Code, Detail: it.Error.Detail})
	}
	return out
}

// report reads a branch's status from its proposals.
func (s *Service) report(ctx context.Context, st Store, b domain.Branch) (BranchReport, error) {
	rep := BranchReport{Branch: b, Removed: b.RemovedKeys, Invalid: b.Invalid}
	own, err := st.BranchProposals(ctx, b.ID)
	if err != nil {
		return rep, err
	}
	var newKeys []domain.MessageID
	for _, pr := range own {
		if pr.Kind == domain.ProposalSourceChange {
			rep.SourceProposals = append(rep.SourceProposals, pr.Key)
			rep.changedMessages = append(rep.changedMessages, pr.MessageID)
			continue
		}
		rep.NewKeys = append(rep.NewKeys, pr.Key)
		newKeys = append(newKeys, pr.MessageID)
	}
	if b.State != domain.BranchOpen || len(newKeys) == 0 {
		return rep, nil
	}
	all, err := st.ProposalsForMessages(ctx, newKeys)
	if err != nil {
		return rep, err
	}
	w := &branchWrite{s: s, st: st, b: &b}
	others, err := w.openProposalsOfOthers(ctx, all)
	if err != nil {
		return rep, err
	}
	var ids []domain.BranchID
	for _, ps := range others {
		for _, o := range ps {
			ids = append(ids, o.BranchID)
		}
	}
	names, err := st.BranchesByIDs(ctx, ids)
	if err != nil {
		return rep, err
	}
	seen := map[domain.BranchID]bool{}
	for _, pr := range own {
		if pr.Kind != domain.ProposalNewKey {
			continue
		}
		c := KeyConflict{Key: pr.Key}
		for _, o := range others[pr.MessageID] {
			if o.Source.SameModel(pr.Source) {
				continue
			}
			c.Branches = append(c.Branches, names[o.BranchID].Name)
			if !seen[o.BranchID] {
				seen[o.BranchID] = true
				rep.conflictingBranches = append(rep.conflictingBranches, o.BranchID)
			}
		}
		if len(c.Branches) > 0 {
			slices.Sort(c.Branches)
			rep.Conflicts = append(rep.Conflicts, c)
		}
	}
	return rep, nil
}

// countOutdated asks Localization how many translations the branch's
// source proposals will leave behind.
func (s *Service) countOutdated(ctx context.Context, project domain.ProjectID, rep *BranchReport) error {
	if s.impact == nil {
		return nil
	}
	rep.Outdated = map[string]int{}
	if len(rep.changedMessages) == 0 {
		return nil
	}
	counts, err := s.impact.CurrentTranslations(ctx, project, rep.changedMessages)
	if err != nil {
		return err
	}
	rep.Outdated = counts
	return nil
}

// BranchStatus reports a branch's status as its last push left it.
func (s *Service) BranchStatus(ctx context.Context, project domain.ProjectID, branch string) (BranchReport, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return BranchReport{}, err
	}
	name, err := parseBranchOrNotFound(branch)
	if err != nil {
		return BranchReport{}, err
	}
	var rep BranchReport
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		b, err := st.Branch(ctx, project, name)
		if err != nil {
			return err
		}
		rep, err = s.report(ctx, st, b)
		return err
	})
	if err != nil {
		return BranchReport{}, err
	}
	return rep, s.countOutdated(ctx, project, &rep)
}

func parseBranchOrNotFound(name string) (domain.BranchName, error) {
	n, err := domain.ParseBranchName(name)
	if err != nil {
		return "", ErrNotFound
	}
	return n, nil
}

// CloseBranch closes an unmerged branch (the pull_request.closed
// webhook). Its proposed messages stay proposed for ProposalRetention;
// SweepProposals obsoletes them after that.
func (s *Service) CloseBranch(ctx context.Context, project domain.ProjectID, branch string) (domain.Branch, error) {
	return s.changeBranch(ctx, project, branch, domain.EventBranchClosed, func(b *domain.Branch, _ *branchWrite) (bool, error) {
		return b.Close(s.now())
	})
}

// MergeBranch marks a branch merged. It activates nothing: the default
// branch's push does that, so correctness doesn't depend on this call
// (RFC 0004 §4.1). Proposed messages the default branch never brought
// in are swept like a closed branch's.
func (s *Service) MergeBranch(ctx context.Context, project domain.ProjectID, branch string) (domain.Branch, error) {
	return s.changeBranch(ctx, project, branch, domain.EventBranchMerged, func(b *domain.Branch, _ *branchWrite) (bool, error) {
		return b.Merge(s.now())
	})
}

// ReopenBranch opens a closed branch again; its proposed messages that
// were swept become proposed again, with their translations and history.
func (s *Service) ReopenBranch(ctx context.Context, project domain.ProjectID, branch string) (domain.Branch, error) {
	return s.changeBranch(ctx, project, branch, domain.EventBranchReopened, func(b *domain.Branch, w *branchWrite) (bool, error) {
		changed, err := b.Reopen(s.now())
		if err != nil || !changed {
			return changed, err
		}
		return true, w.reproposeOwned(ctx)
	})
}

// ReportBranchPreview records the preview URL CI deployed the branch to.
func (s *Service) ReportBranchPreview(ctx context.Context, project domain.ProjectID, branch, url string) (domain.Branch, error) {
	return s.changeBranch(ctx, project, branch, "", func(b *domain.Branch, _ *branchWrite) (bool, error) {
		return b.ReportPreview(url, s.now())
	})
}

// changeBranch runs change on a locked branch and publishes event (if
// any) when it changed something.
func (s *Service) changeBranch(ctx context.Context, project domain.ProjectID, branch, event string,
	change func(*domain.Branch, *branchWrite) (bool, error),
) (domain.Branch, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Branch{}, err
	}
	name, err := parseBranchOrNotFound(branch)
	if err != nil {
		return domain.Branch{}, err
	}
	var b domain.Branch
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if b, err = st.LockBranch(ctx, project, name); err != nil {
			return err
		}
		expected := b.Version
		w := &branchWrite{s: s, st: st, b: &b, by: by, now: s.now()}
		changed, err := change(&b, w)
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateBranch(ctx, b, expected); err != nil {
			return err
		}
		if err := w.project(ctx); err != nil {
			return err
		}
		if event == "" {
			return nil
		}
		return st.Publish(ctx, branchEvent(event, b, by))
	})
	return b, err
}

// SweepProposals obsoletes the tenant's proposed messages whose branches
// all closed (or merged without the default branch bringing them in)
// at least ProposalRetention ago; they keep their translations and
// history, and a reopened branch or a later push of the key proposes
// them again. It returns how many it obsoleted. The purge job runs it
// per tenant with a background principal holding catalog.write.
func (s *Service) SweepProposals(ctx context.Context) (int, error) {
	// It reaches every project of the tenant, so nobody limited to some
	// projects runs it (RFC 0006 §4.1).
	if err := authz.RequireUnscoped(ctx, authz.CatalogWrite); err != nil {
		return 0, err
	}
	by, err := author(ctx, authz.CatalogWrite)
	if err != nil {
		return 0, err
	}
	n := 0
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		candidates, err := st.LockOrphanedProposedMessages(ctx)
		if err != nil || len(candidates) == 0 {
			return err
		}
		ids := make([]domain.MessageID, len(candidates))
		for i, m := range candidates {
			ids[i] = m.ID
		}
		all, err := st.ProposalsForMessages(ctx, ids)
		if err != nil {
			return err
		}
		var branchIDs []domain.BranchID
		for _, pr := range all {
			branchIDs = append(branchIDs, pr.BranchID)
		}
		branches, err := st.BranchesByIDs(ctx, branchIDs)
		if err != nil {
			return err
		}
		now := s.now()
		byProject := map[domain.ProjectID]*branchWrite{}
		for _, m := range candidates {
			if !expired(m, all, branches, now) {
				continue
			}
			w := byProject[m.ProjectID]
			if w == nil {
				w = &branchWrite{s: s, st: st, b: &domain.Branch{ProjectID: m.ProjectID}, by: by, now: now}
				byProject[m.ProjectID] = w
			}
			if err := w.obsolete(ctx, m); err != nil {
				return err
			}
			n++
		}
		for _, w := range byProject {
			if err := w.project(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// sweepPrincipal is the background principal of the proposal sweep.
// Its name is stable: it appears as the actor in logs and audits.
const sweepPrincipal = "catalog.sweep"

// sweepBatch bounds the tenants one run visits, so a database full of
// them doesn't make a single run unbounded; the next run takes the
// rest.
const sweepBatch = 50

// SweepAllProposals runs the proposal sweep over the tenants that have
// work — proposed messages whose every proposing branch closed at least
// domain.ProposalRetention ago — each in its own tenant scope as the
// background principal catalog.sweep, and reports how many messages it
// obsoleted. It runs outside any tenant (the daily catalog.proposals
// job, leased by the scheduler so one replica leads it) and needs a
// Scanner. A failing tenant doesn't stop the others; their errors are
// joined.
func (s *Service) SweepAllProposals(ctx context.Context) (int, error) {
	if s.scanner == nil {
		return 0, errors.New("catalog: SweepAllProposals needs a Scanner")
	}
	cutoff := s.now().Add(-domain.ProposalRetention)
	tenants, err := s.scanner.TenantsWithExpiredProposals(ctx, cutoff, sweepBatch)
	if err != nil {
		return 0, err
	}
	var (
		total int
		errs  []error
	)
	for _, t := range tenants {
		bg, err := authz.Background(tenancy.ContextWithTenant(ctx, t), sweepPrincipal, authz.CatalogWrite)
		if err != nil {
			return total, err
		}
		n, err := s.SweepProposals(bg)
		if err != nil {
			errs = append(errs, fmt.Errorf("catalog: sweep proposals of tenant %s: %w", t, err))
			continue
		}
		total += n
	}
	return total, errors.Join(errs...)
}

// ClosedBranches reports when each of the project's closed and merged
// branches closed. Context's retention deletes a closed branch's builds
// once the grace period has passed (RFC 0004 §2.3). Needs catalog.read.
func (s *Service) ClosedBranches(ctx context.Context, project domain.ProjectID) (map[domain.BranchName]time.Time, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, err
	}
	var out map[domain.BranchName]time.Time
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = st.ClosedBranches(ctx, project)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// expired reports whether every branch proposing m let its proposals
// expire. A proposed message no branch proposes (its branch withdrew
// it while another, closed branch still had it) follows the rest.
func expired(m domain.Message, all []domain.Proposal, branches map[domain.BranchID]domain.Branch, now time.Time) bool {
	owners := 0
	for _, pr := range all {
		if pr.MessageID != m.ID || pr.Kind != domain.ProposalNewKey {
			continue
		}
		owners++
		if !branches[pr.BranchID].ProposalsExpired(now) {
			return false
		}
	}
	return owners > 0
}

// Overlay is what a branch adds to the main catalog for its preview
// release (RFC 0004 §4.2): its proposals, and the messages they name.
// Every other environment's build excludes both — proposed messages
// aren't active, and a source proposal isn't the message's source.
type Overlay struct {
	Branch    domain.Branch
	Proposals []domain.Proposal
	Messages  map[domain.MessageID]domain.Message
}

// BranchOverlay reads a branch's overlay in one transaction: the port
// Release's branch environments build from.
func (s *Service) BranchOverlay(ctx context.Context, project domain.ProjectID, branch string) (Overlay, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return Overlay{}, err
	}
	name, err := parseBranchOrNotFound(branch)
	if err != nil {
		return Overlay{}, err
	}
	var o Overlay
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if o.Branch, err = st.Branch(ctx, project, name); err != nil {
			return err
		}
		if o.Proposals, err = st.BranchProposals(ctx, o.Branch.ID); err != nil {
			return err
		}
		ids := make([]domain.MessageID, len(o.Proposals))
		for i, pr := range o.Proposals {
			ids[i] = pr.MessageID
		}
		o.Messages, err = st.MessagesByIDs(ctx, project, ids)
		return err
	})
	return o, err
}

func branchEvent(typ string, b domain.Branch, by domain.Author) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateBranch, AggregateID: b.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.BranchEventOf(b, by),
	}
}

func branchPushedEvent(rep BranchReport, by domain.Author) outbox.Event {
	conflicting := make([]string, len(rep.conflictingBranches))
	for i, id := range rep.conflictingBranches {
		conflicting[i] = id.String()
	}
	return outbox.Event{
		Type: domain.EventBranchPushed, AggregateType: domain.AggregateBranch, AggregateID: rep.Branch.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.BranchPushed{
			BranchEvent: domain.BranchEventOf(rep.Branch, by), NewKeys: len(rep.NewKeys),
			SourceProposals: len(rep.SourceProposals), Conflicts: len(rep.Conflicts), ConflictingBranches: conflicting,
		},
	}
}
