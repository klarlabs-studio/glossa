// Package metrics records the Workflow context in Prometheus (RFC 0006
// §10.1): transitions by outcome, instances by status, live
// assignments by whether they are overdue, and approval decisions by
// subject and verdict.
//
// Every label value is allowlisted here. A value outside its list is not
// recorded at all: the app passes constants, so one would be a
// programming error, and it must not become a series nobody meant to
// create. No definition name, vendor, assignee or tenant is ever a
// label.
package metrics

import (
	"errors"
	"slices"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The allowlisted label values.
var (
	outcomes = []string{app.TransitionApplied, app.TransitionIgnored, app.TransitionRefused}
	statuses = []string{domain.StatusActive, domain.StatusFinished}
	subjects = []string{string(domain.SubjectTranslation), string(domain.SubjectReleaseRequest)}
	verdicts = []string{string(domain.VerdictGranted), string(domain.VerdictDenied)}
)

// Prometheus implements app.RunnerMetrics and app.DecisionMetrics.
type Prometheus struct {
	transitions *prometheus.CounterVec
	instances   *prometheus.GaugeVec
	assignments *prometheus.GaugeVec
	decisions   *prometheus.CounterVec
}

var (
	_ app.RunnerMetrics   = (*Prometheus)(nil)
	_ app.DecisionMetrics = (*Prometheus)(nil)
)

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	p := &Prometheus{
		transitions: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_workflow_transitions_total",
			Help: "Workflow instance transitions recorded, by outcome: applied (the instance moved), " +
				"ignored (the event no longer applied: the world moved on) or refused (an action was refused for permission " +
				"and the instance stayed where it was).",
		}, []string{"outcome"})),
		instances: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_workflow_instances",
			Help: "Workflow instances across the deployment, by status (active, finished), as the timer sweep last counted them. " +
				"Only the replica that leads the sweep updates it: read it beside glossa_scheduler_last_run_timestamp_seconds{job=\"workflow.timers\"}.",
		}, []string{"status"})),
		assignments: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_assignments_open",
			Help: "Live (open or accepted) assignments across the deployment, by whether they are past their due date, " +
				"as the timer sweep last counted them.",
		}, []string{"overdue"})),
		decisions: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_approvals_decisions_total",
			Help: "Approval decisions recorded, by subject (translation, release_request) and decision (granted, denied). " +
				"A repeated grant by the same person records nothing and is not counted.",
		}, []string{"subject", "decision"})),
	}
	// Publish every series at zero, so a dashboard shows "none" rather
	// than a gap until the first one happens.
	for _, o := range outcomes {
		p.transitions.WithLabelValues(o)
	}
	for _, s := range subjects {
		for _, v := range verdicts {
			p.decisions.WithLabelValues(s, v)
		}
	}
	return p
}

// Transition implements app.RunnerMetrics.
func (p *Prometheus) Transition(outcome string) {
	if slices.Contains(outcomes, outcome) {
		p.transitions.WithLabelValues(outcome).Inc()
	}
}

// Instances implements app.RunnerMetrics.
func (p *Prometheus) Instances(status string, n int) {
	if slices.Contains(statuses, status) {
		p.instances.WithLabelValues(status).Set(float64(n))
	}
}

// OpenAssignments implements app.RunnerMetrics.
func (p *Prometheus) OpenAssignments(overdue bool, n int) {
	p.assignments.WithLabelValues(strconv.FormatBool(overdue)).Set(float64(n))
}

// Decision implements app.DecisionMetrics.
func (p *Prometheus) Decision(subject, verdict string) {
	if slices.Contains(subjects, subject) && slices.Contains(verdicts, verdict) {
		p.decisions.WithLabelValues(subject, verdict).Inc()
	}
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
