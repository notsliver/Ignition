package api

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/notsliver/Ignition/services/internal/compose"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/store"
)

// provisionExtras are settings beyond the basic resource input, used by imports and duplication.
type provisionExtras struct {
	Volumes       []compose.Volume // besides dataPath's "data" volume
	HealthCommand string           // a command health check (from a compose healthcheck)
	Restart       string           // always, on-failure, never; "" keeps the default
	Build         *compose.Build
	Engine        string
	NoDeploy      bool // create it without deploying (e.g. external databases, duplicates on request)
	Trigger       string
}

// provision creates one resource (and its first deployment) inside tx. Callers validate in first.
func (s *Server) provision(ctx context.Context, tx pgx.Tx, u *store.User, projectID, envID string, in resourceInput, x provisionExtras) (string, string, error) {
	defs := s.defaults(ctx)
	if in.MemoryMB == 0 {
		in.MemoryMB = defs.MemoryMB
	}
	if in.CPUs == 0 {
		in.CPUs = defs.CPUs
	}
	id, err := s.Store.CreateResource(ctx, tx, store.NewResource{
		ProjectID: projectID, EnvironmentID: envID, Name: in.Name, Type: in.Type, SourceType: in.SourceType, Image: in.Image,
		RepoURL: in.RepoURL, Branch: in.Branch, Dockerfile: in.Dockerfile, Command: in.Command, DataPath: in.DataPath, CreatedBy: u.ID, SourceID: in.SourceID,
		Defaults: &store.ResourceDefaults{HealthSeconds: defs.HealthSeconds, StopGrace: defs.StopGrace, RestartPolicy: defs.RestartPolicy, BuildMode: defs.BuildMode},
		Port:     in.Port, MemoryMB: in.MemoryMB, CPUs: in.CPUs, Engine: x.Engine,
	})
	if err != nil {
		return "", "", err
	}
	for _, e := range in.Env {
		sealed, err := s.Secrets.Seal(e.Value, id+":"+e.Key)
		if err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO env_vars (resource_id, key, value_enc, secret, updated_by) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (resource_id, key) DO UPDATE SET value_enc = EXCLUDED.value_enc, secret = EXCLUDED.secret`, id, e.Key, sealed, e.Secret, optionalID(u.ID)); err != nil {
			return "", "", err
		}
	}
	for _, v := range x.Volumes {
		if _, err := tx.Exec(ctx, `INSERT INTO resource_volumes (resource_id, name, mount_path) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, v.Name, v.Path); err != nil {
			return "", "", err
		}
	}
	if x.Restart != "" {
		if _, err := tx.Exec(ctx, `UPDATE resources SET restart_policy = $2 WHERE id = $1`, id, x.Restart); err != nil {
			return "", "", err
		}
	}
	if b := x.Build; b != nil {
		args := []string{}
		for k, v := range b.Args {
			args = append(args, k+"="+v)
		}
		ctxDir := b.Context
		if ctxDir == "." || ctxDir == "./" {
			ctxDir = ""
		}
		if _, err := tx.Exec(ctx, `UPDATE resources SET build_context = $2, dockerfile = $3, build_target = $4, build_args = $5, build_mode = 'dockerfile' WHERE id = $1`,
			id, trimDot(ctxDir), b.Dockerfile, b.Target, args); err != nil {
			return "", "", err
		}
	}
	if x.HealthCommand != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO resource_health (resource_id, enabled, kind, command, interval_seconds, timeout_seconds, retries, start_period_seconds, restart_unhealthy)
			VALUES ($1, true, 'command', $2, 15, 10, 5, 60, false)`, id, x.HealthCommand); err != nil {
			return "", "", err
		}
	}
	if x.NoDeploy {
		return id, "", nil
	}
	trigger := x.Trigger
	if trigger == "" {
		trigger = "create"
	}
	depID, err := s.Store.CreateDeployment(ctx, tx, id, trigger, "", in.Branch, "", u.ID)
	if err != nil {
		return "", "", err
	}
	if _, err := jobs.Enqueue(ctx, tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: id, DeploymentID: depID, CreatedBy: u.ID}); err != nil {
		return "", "", err
	}
	return id, depID, nil
}

func trimDot(p string) string {
	for len(p) > 2 && p[:2] == "./" {
		p = p[2:]
	}
	if len(p) > 0 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	return p
}

// optionalID is nil for "", so a missing user doesn't violate the foreign key.
func optionalID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}
