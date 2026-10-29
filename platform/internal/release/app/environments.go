package app

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// systemActor records changes Release makes on its own (the default
// environments).
const systemActor = "system:release"

// ensureDefaults creates the default environments a project doesn't
// have yet. Projects get them on first use of Release, whatever created
// the project and whenever: an insert that finds the row does nothing.
func (s *Service) ensureDefaults(ctx context.Context, st Store, project uuid.UUID) error {
	for _, name := range domain.DefaultEnvironments {
		e, err := domain.NewEnvironment(project, name, domain.DefaultPolicy(name), s.now())
		if err != nil {
			return err
		}
		if _, err := st.InsertEnvironment(ctx, e, systemActor); err != nil {
			return err
		}
	}
	return nil
}

// checkProject authorizes perm and checks that the project exists.
func (s *Service) checkProject(ctx context.Context, project uuid.UUID, perm authz.Permission) (string, error) {
	by, err := actorIn(ctx, perm, project)
	if err != nil {
		return "", err
	}
	return by, s.source.CheckProject(ctx, project)
}

// ListEnvironments lists a project's environments by name.
func (s *Service) ListEnvironments(ctx context.Context, project uuid.UUID, page pagination.Page) ([]domain.Environment, *string, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, nil, err
	}
	var rows []domain.Environment
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		var err error
		rows, err = st.Environments(ctx, project, page.After, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(e domain.Environment) string { return e.Name })
	return items, next, nil
}

// GetEnvironment returns one environment.
func (s *Service) GetEnvironment(ctx context.Context, project uuid.UUID, name string) (domain.Environment, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return domain.Environment{}, err
	}
	if !validName(name) {
		return domain.Environment{}, ErrNotFound
	}
	var e domain.Environment
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		var err error
		e, err = st.Environment(ctx, project, name, false)
		return err
	})
	return e, err
}

func validName(name string) bool {
	_, err := domain.ParseEnvironmentName(name)
	return err == nil
}

// CreateEnvironment adds a custom environment (a branch preview, a QA
// stage). policy nil means the default policy for its name.
func (s *Service) CreateEnvironment(ctx context.Context, project uuid.UUID, name string, policy *domain.Policy) (domain.Environment, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Environment{}, err
	}
	p := domain.DefaultPolicy(name)
	if policy != nil {
		if p, err = domain.NewPolicy(policy.States, policy.IncludeOutdated); err != nil {
			return domain.Environment{}, err
		}
	}
	e, err := domain.NewEnvironment(project, name, p, s.now())
	if err != nil {
		return domain.Environment{}, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		inserted, err := st.InsertEnvironment(ctx, e, by)
		if err != nil {
			return err
		}
		if !inserted {
			return ErrEnvironmentExists
		}
		return st.Publish(ctx, environmentEvent(domain.EventEnvironmentCreated, e, by))
	})
	return e, err
}

// UpdateEnvironment replaces an environment's policy. ifMatch is the
// environment's ETag. The release it serves keeps serving; the policy
// governs the next publish and what may be promoted into it.
func (s *Service) UpdateEnvironment(ctx context.Context, project uuid.UUID, name string, ifMatch int, policy domain.Policy) (domain.Environment, error) {
	return s.ConfigureEnvironment(ctx, project, name, ifMatch, policy, nil)
}

func environmentEvent(typ string, e domain.Environment, by string) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateEnvironment, AggregateID: e.ProjectID.String() + "/" + e.Name, Actor: outbox.Actor(by),
		Payload: domain.EnvironmentChanged{
			ProjectID: e.ProjectID.String(), Environment: e.Name, Kind: string(e.Kind), Branch: e.Branch,
			States: e.Policy.States, IncludeOutdated: e.Policy.IncludeOutdated, Approval: e.Approval,
			Version: e.Version, By: by,
		},
	}
}

// ListDeployments lists an environment's pointer moves, newest first.
func (s *Service) ListDeployments(ctx context.Context, project uuid.UUID, name string, page pagination.Page) ([]domain.Deployment, *string, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, nil, err
	}
	if !validName(name) {
		return nil, nil, ErrNotFound
	}
	before, err := beforeInt(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Deployment
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		if _, err := st.Environment(ctx, project, name, false); err != nil {
			return err
		}
		var err error
		rows, err = st.Deployments(ctx, project, name, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(d domain.Deployment) string { return strconv.Itoa(d.Number) })
	return items, next, nil
}

// isNotFound reports whether err is Release's not-found.
func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
