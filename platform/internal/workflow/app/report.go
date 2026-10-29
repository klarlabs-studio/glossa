package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The quality side of vendors and workflows (RFC 0006 §3.4, §10.2): the
// numbers of completed assignments, aggregated by assignee and locale.
//
// They are computed on read from what the owning contexts already hold
// — the assignments here, the source in Catalog, the revision log in
// Localization, the findings in Quality — and nothing new is stored, so
// no text is copied anywhere. Every per-unit read runs as the caller,
// through that context's own use case, so a report says nothing its
// reader could not have read unit by unit.

// UnitQuality is what the other contexts say about one unit of a
// completed assignment.
type UnitQuality struct {
	// Found is false when the message or its translation can no longer
	// be read: deleted since, or never written. The unit is then counted
	// as delivered but contributes no other number.
	Found bool
	// SourceWords is the words in the source's visible text.
	SourceWords int
	// FromTM says the text that stood at completion came from
	// translation memory: an exact match, the only kind Glossa reuses
	// without a person or a model writing it.
	FromTM bool
	// ReviewState is the translation's review state now.
	ReviewState string
	// Changed says the current text differs from the text that stood
	// when the assignment was completed: someone edited the delivered
	// text since.
	Changed bool
	// EditDistance and EditRatio compare the text at completion with
	// the current text, by Intelligence's computation (the one
	// GET …/ai-metrics reports): Levenshtein over the visible text, and
	// that over the longer text's length.
	EditDistance int
	EditRatio    float64
	// Findings are the unit's open (unwaived) findings now, by layer
	// and severity.
	Findings []domain.FindingCount
}

// QualityFacts reads a unit's numbers from the contexts that own them,
// as the principal on ctx. completed is when the assignment was
// completed: the text that stood then is the delivered text.
type QualityFacts interface {
	UnitQuality(ctx context.Context, project, message uuid.UUID, locale string, completed time.Time) (UnitQuality, error)
}

// WithQualityFacts lets the service report quality numbers; without it
// QualityReport answers ErrReportUnavailable.
func WithQualityFacts(f QualityFacts) WorkOption { return func(s *WorkService) { s.facts = f } }

// ErrReportUnavailable is a server built without the contexts a report
// reads.
var ErrReportUnavailable = errors.New("workflow: quality reports are not available on this server")

// Bounds on one report. A report reads every unit of every completed
// assignment it covers through three contexts, so it is bounded and
// says when it stopped (QualityReport.Truncated) rather than timing out.
const (
	// MaxReportAssignments bounds the assignments one report scans:
	// the completed ones it reports and the later ones rework is read
	// from.
	MaxReportAssignments = 2000
	// MaxReportUnits bounds the units whose facts one report reads.
	MaxReportUnits = 5000
)

// ReportQuery narrows a report. Zero fields don't filter.
type ReportQuery struct {
	Project uuid.UUID
	// Assignee is a stored spelling ("vendor:<uuid>", "member:<uuid>",
	// "group:<uuid>", "role:<name>").
	Assignee string
	// Since keeps assignments completed at or after it.
	Since time.Time
}

// QualityRow is one assignee's completed work in one locale.
type QualityRow struct {
	Assignee string `json:"assignee"`
	Locale   string `json:"locale"`
	// Assignments is the completed assignments with units in the
	// locale; OnTime, Late and NoDue split them by their due date.
	Assignments int `json:"assignments"`
	OnTime      int `json:"on_time"`
	Late        int `json:"late"`
	NoDue       int `json:"no_due"`
	// Units is the units delivered; Unavailable those whose facts could
	// not be read (a deleted message, a translation never written).
	Units       int `json:"units"`
	Unavailable int `json:"unavailable"`
	SourceWords int `json:"source_words"`
	// TMWords is the source words whose delivered text came from
	// translation memory, by match band. Glossa reuses exact matches
	// only, so "exact" is the one band there is.
	TMWords map[string]int `json:"tm_words"`
	// Review outcomes, from each unit's review state now.
	Approved   int `json:"approved"`
	Rejected   int `json:"rejected"`
	InReview   int `json:"needs_review"`
	Draft      int `json:"draft"`
	Unreviewed int `json:"unreviewed"`
	// Changed is the units whose text differs from what was delivered;
	// MeanEditDistance and MeanEditRatio average the edit over the
	// reviewed (approved or rejected) units, unchanged ones counting 0.
	Changed          int     `json:"changed_after_delivery"`
	MeanEditDistance float64 `json:"mean_edit_distance"`
	MeanEditRatio    float64 `json:"mean_edit_ratio"`
	// Findings is the open findings on the units now, by layer, and
	// FindingsPerUnit their total over Units.
	Findings        map[string]int `json:"findings"`
	FindingsPerUnit float64        `json:"findings_per_unit"`
	// Reworked is the units given out again in a later assignment,
	// made after this one was completed; ReworkRate is that over Units.
	Reworked   int     `json:"reworked"`
	ReworkRate float64 `json:"rework_rate"`
	// OnTimeRate is OnTime over the assignments that had a due date.
	OnTimeRate float64 `json:"on_time_rate"`

	editSum  int
	ratioSum float64
	reviewed int
}

// QualityReport is the rows, sorted by assignee and locale.
type QualityReport struct {
	Since       *time.Time   `json:"since,omitempty"`
	GeneratedAt time.Time    `json:"generated_at"`
	Rows        []QualityRow `json:"rows"`
	// Truncated says a bound stopped the report: its numbers cover only
	// the assignments and units read before it.
	Truncated bool `json:"truncated"`
}

// QualityReport aggregates the completed assignments the caller may see
// (RFC 0006 §3.4). It takes assignments.read in the caller's project
// scope — the same check every project-addressed list makes — and a
// member whose visibility is `assigned` is refused, as from every list:
// a vendor delivers work, and the numbers are about vendors. A project
// outside the caller's scope is not found.
func (s *WorkService) QualityReport(ctx context.Context, q ReportQuery) (QualityReport, error) {
	scope, err := authz.Projects(ctx, PermAssignmentsRead)
	if err != nil {
		return QualityReport{}, err
	}
	if q.Project != uuid.Nil {
		if err := authz.RequireIn(ctx, PermAssignmentsRead, q.Project); err != nil {
			return QualityReport{}, err
		}
	}
	if q.Assignee != "" {
		if _, err := domain.ParseAssignee(q.Assignee); err != nil {
			return QualityReport{}, fmt.Errorf("%w: %w", ErrInvalidQuery, err)
		}
	}
	if s.facts == nil {
		return QualityReport{}, ErrReportUnavailable
	}
	all, truncated, err := s.reportAssignments(ctx, q.Project, within(scope))
	if err != nil {
		return QualityReport{}, err
	}
	out := QualityReport{GeneratedAt: s.now().UTC(), Rows: []QualityRow{}, Truncated: truncated}
	if !q.Since.IsZero() {
		since := q.Since.UTC()
		out.Since = &since
	}
	rows := map[[2]string]*QualityRow{}
	units := 0
	for _, a := range all {
		if a.State != domain.AssignmentDone || a.ClosedAt == nil || a.ClosedAt.Before(q.Since) ||
			(q.Assignee != "" && a.Assignee.String() != q.Assignee) {
			continue
		}
		if units+len(a.Units) > MaxReportUnits {
			out.Truncated = true
			break
		}
		units += len(a.Units)
		if err := s.addAssignment(ctx, rows, a, all); err != nil {
			return QualityReport{}, err
		}
	}
	for _, r := range rows {
		out.Rows = append(out.Rows, r.finish())
	}
	slices.SortFunc(out.Rows, func(a, b QualityRow) int {
		if c := strings.Compare(a.Assignee, b.Assignee); c != 0 {
			return c
		}
		return strings.Compare(a.Locale, b.Locale)
	})
	return out, nil
}

// reportAssignments reads every assignment of the report's scope, in id
// (creation) order, up to MaxReportAssignments.
func (s *WorkService) reportAssignments(ctx context.Context, project uuid.UUID, scope *[]uuid.UUID) ([]domain.Assignment, bool, error) {
	var out []domain.Assignment
	f := AssignmentFilter{Project: project, Within: scope, Limit: 200}
	for {
		var page []domain.Assignment
		err := s.tx.InTenant(ctx, func(ctx context.Context, st WorkStore) (err error) {
			page, err = st.ListAssignments(ctx, f)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		out = append(out, page...)
		if len(out) >= MaxReportAssignments {
			return out[:MaxReportAssignments], true, nil
		}
		if len(page) < f.Limit {
			return out, false, nil
		}
		f.After = page[len(page)-1].ID
	}
}

// addAssignment adds one completed assignment's units to their rows.
func (s *WorkService) addAssignment(ctx context.Context, rows map[[2]string]*QualityRow, a domain.Assignment, all []domain.Assignment) error {
	closed := *a.ClosedAt
	for _, locale := range domain.Locales(a.Units) {
		r := row(rows, a.Assignee.String(), locale)
		r.Assignments++
		switch {
		case a.DueAt == nil:
			r.NoDue++
		case closed.After(*a.DueAt):
			r.Late++
		default:
			r.OnTime++
		}
	}
	for _, u := range a.Units {
		r := row(rows, a.Assignee.String(), u.Locale)
		r.Units++
		if reassigned(u, closed, all) {
			r.Reworked++
		}
		f, err := s.facts.UnitQuality(ctx, a.ProjectID, u.Message, u.Locale, closed)
		if err != nil {
			return err
		}
		r.add(f)
	}
	return nil
}

// reassigned reports whether u was given out again after closed.
func reassigned(u domain.Unit, closed time.Time, all []domain.Assignment) bool {
	return slices.ContainsFunc(all, func(b domain.Assignment) bool {
		return b.CreatedAt.After(closed) && slices.Contains(b.Units, u)
	})
}

func row(rows map[[2]string]*QualityRow, assignee, locale string) *QualityRow {
	k := [2]string{assignee, locale}
	r, ok := rows[k]
	if !ok {
		r = &QualityRow{Assignee: assignee, Locale: locale, TMWords: map[string]int{"exact": 0}, Findings: map[string]int{}}
		rows[k] = r
	}
	return r
}

// add counts one unit's facts.
func (r *QualityRow) add(f UnitQuality) {
	if !f.Found {
		r.Unavailable++
		r.Unreviewed++
		return
	}
	r.SourceWords += f.SourceWords
	if f.FromTM {
		r.TMWords["exact"] += f.SourceWords
	}
	switch f.ReviewState {
	case "approved":
		r.Approved++
	case "rejected":
		r.Rejected++
	case "needs_review":
		r.InReview++
	case "draft":
		r.Draft++
	default:
		r.Unreviewed++
	}
	if f.ReviewState == "approved" || f.ReviewState == "rejected" {
		r.reviewed++
		r.editSum += f.EditDistance
		r.ratioSum += f.EditRatio
	}
	if f.Changed {
		r.Changed++
	}
	for _, c := range f.Findings {
		r.Findings[c.Layer] += c.Count
	}
}

// finish computes the row's rates, rounded to three places.
func (r *QualityRow) finish() QualityRow {
	out := *r
	if r.reviewed > 0 {
		out.MeanEditDistance = round3(float64(r.editSum) / float64(r.reviewed))
		out.MeanEditRatio = round3(r.ratioSum / float64(r.reviewed))
	}
	if r.Units > 0 {
		total := 0
		for _, n := range r.Findings {
			total += n
		}
		out.FindingsPerUnit = round3(float64(total) / float64(r.Units))
		out.ReworkRate = round3(float64(r.Reworked) / float64(r.Units))
	}
	if due := r.OnTime + r.Late; due > 0 {
		out.OnTimeRate = round3(float64(r.OnTime) / float64(due))
	}
	return out
}

func round3(f float64) float64 { return float64(int64(f*1000+0.5)) / 1000 }
