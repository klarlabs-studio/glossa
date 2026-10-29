// Package metrics records the Quality context in Prometheus (RFC 0005
// §11): the findings each layer produced, by code and severity; check
// runs by what asked for them and what they concluded; how long each
// layer took; what became of waiver requests; what the visual probe
// pass handed in and what the ingest made of it; and which version of
// a project's check policy is grading.
//
// Nothing counted a finding before M4. Label values are allowlisted —
// the layer is one of the ten, the severity one of the three, the
// outcome one of a handful, the code dropped past a bound and the
// project dropped past another — because a code comes from a layer and
// a project comes from a tenant, and a dashboard that melts under one
// project's vocabulary is worse than one number fewer. An unbounded
// label is how a metrics endpoint becomes a memory leak.
package metrics

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// MaxCodes bounds the distinct codes one process labels findings with;
// everything past it is counted as "other", so one tenant's vocabulary
// can't unbound the series.
const MaxCodes = 200

// MaxProjects bounds the projects `glossa_quality_policy_version` names.
// A project label is bounded by the deployment and not by the domain —
// a tenant can make as many projects as it likes — so the gauge takes
// the first MaxProjects it sees and drops the rest rather than growing
// a series per project forever. A gauge is not a counter: "other" would
// mean the last project to be read, which is worse than nothing, so
// there is no bucket here.
const MaxProjects = 500

// Prometheus implements app.Metrics.
type Prometheus struct {
	findings *prometheus.CounterVec
	runs     *prometheus.CounterVec
	waivers  *prometheus.CounterVec
	duration *prometheus.HistogramVec
	probes   *prometheus.CounterVec
	policy   *prometheus.GaugeVec

	mu       sync.Mutex
	codes    map[string]struct{}
	projects map[string]struct{}
}

var _ app.Metrics = (*Prometheus)(nil)

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Prometheus{
		findings: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_quality_findings_total",
			Help: "Findings stored with a check run, by layer, code and the severity their layer emitted (a waiver is applied on read, so `waived` never appears here).",
		}, []string{"layer", "code", "severity"})),
		runs: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_quality_check_runs_total",
			Help: "Check runs stored, by trigger (cli, pull_request, write, capture, api) and conclusion (success, failure, neutral).",
		}, []string{"trigger", "conclusion"})),
		waivers: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_quality_waivers_total",
			Help: "What became of a waiver, by outcome: created, updated, revoked, refused, or expired by the daily sweep (a date passed; nobody decided anything).",
		}, []string{"outcome"})),
		duration: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "glossa_quality_check_duration_seconds",
			Help: "How long one layer of a check run took, so a slow layer is visible before it is unbearable. Deterministic layers are milliseconds; the buckets reach a minute because a project can always be larger than the last one.",
			// A check is meant to be cheap enough that people run it, so
			// the interesting resolution is at the bottom.
			Buckets: []float64{.001, .005, .01, .05, .1, .5, 1, 5, 15, 60},
		}, []string{"layer"})),
		probes: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_quality_visual_probes_total",
			Help: "Visual probe findings a capture upload handed in, by code and what the ingest made of them: first_sighting (a warning the two-sighting rule has not confirmed), confirmed (it has), dropped (a policy rule switched it off).",
		}, []string{"code", "outcome"})),
		policy: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_quality_policy_version",
			Help: "The version of a project's stored check policy, as the last read or save of it saw. Two versions can be live at once during a grace period; this is the current document's, not the one a given run graded against.",
		}, []string{"project"})),
		codes:    map[string]struct{}{},
		projects: map[string]struct{}{},
	}
}

// FindingRecorded implements app.Metrics.
func (p *Prometheus) FindingRecorded(layer domain.Layer, code string, severity domain.Severity) {
	p.findings.WithLabelValues(known(string(layer), layers()), p.knownCode(code),
		known(string(severity), []string{string(domain.Error), string(domain.Warning), string(domain.Waived)})).Inc()
}

// CheckRunRecorded implements app.Metrics.
func (p *Prometheus) CheckRunRecorded(trigger domain.Trigger, conclusion domain.Conclusion) {
	p.runs.WithLabelValues(known(string(trigger), triggers()), known(string(conclusion), conclusions())).Inc()
}

// LayerChecked implements app.Metrics.
func (p *Prometheus) LayerChecked(layer domain.Layer, d time.Duration) {
	p.duration.WithLabelValues(known(string(layer), layers())).Observe(d.Seconds())
}

// WaiverDecided implements app.Metrics.
func (p *Prometheus) WaiverDecided(outcome string) {
	p.waivers.WithLabelValues(known(outcome, []string{
		app.WaiverCreated, app.WaiverUpdated, app.WaiverRevoked, app.WaiverRefused, app.WaiverExpired,
	})).Inc()
}

// VisualProbeRecorded implements app.Metrics.
func (p *Prometheus) VisualProbeRecorded(code, outcome string) {
	p.probes.WithLabelValues(p.knownCode(code),
		known(outcome, []string{app.ProbeFirstSighting, app.ProbeConfirmed, app.ProbeDropped})).Inc()
}

// PolicyVersionRead implements app.Metrics. A project past MaxProjects
// is not published: an unbounded gauge label is how a metrics endpoint
// becomes a memory leak.
func (p *Prometheus) PolicyVersionRead(project uuid.UUID, version int) {
	id := project.String()
	if !p.knownProject(id) {
		return
	}
	p.policy.WithLabelValues(id).Set(float64(version))
}

// knownProject admits a project as a label until MaxProjects distinct
// ones have been seen.
func (p *Prometheus) knownProject(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.projects[id]; ok {
		return true
	}
	if len(p.projects) >= MaxProjects {
		return false
	}
	p.projects[id] = struct{}{}
	return true
}

// knownCode keeps a code as a label until MaxCodes distinct ones have
// been seen, and buckets the rest.
func (p *Prometheus) knownCode(code string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.codes[code]; ok {
		return code
	}
	if code == "" || len(p.codes) >= MaxCodes {
		return "other"
	}
	p.codes[code] = struct{}{}
	return code
}

func known(v string, allowed []string) string {
	if slices.Contains(allowed, v) {
		return v
	}
	return "other"
}

func layers() []string {
	out := make([]string, len(domain.Layers))
	for i, l := range domain.Layers {
		out[i] = string(l)
	}
	return out
}

func triggers() []string {
	out := make([]string, len(domain.Triggers))
	for i, t := range domain.Triggers {
		out[i] = string(t)
	}
	return out
}

func conclusions() []string {
	return []string{string(domain.ConclusionSuccess), string(domain.ConclusionFailure), string(domain.ConclusionNeutral)}
}

func register[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			if existing, ok := are.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic(err)
	}
	return c
}
