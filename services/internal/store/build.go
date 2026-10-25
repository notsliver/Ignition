package store

import (
	"context"
	"time"
)

type BuildSettings struct {
	Mode           string   `json:"mode"` // auto: Dockerfile if present, otherwise detect the stack; dockerfile: require one
	Dockerfile     string   `json:"dockerfile"`
	Context        string   `json:"context"`
	Target         string   `json:"target"`
	Args           []string `json:"args"`
	TimeoutMinutes int      `json:"timeoutMinutes"`
	// Deploy
	Command        string `json:"command"`
	Port           *int   `json:"port"`
	HealthSeconds  int    `json:"healthSeconds"`
	StopGrace      int    `json:"stopGraceSeconds"`
	ReleaseCommand string `json:"releaseCommand"`
}

func (s *Store) GetBuildSettings(ctx context.Context, resourceID string) (BuildSettings, error) {
	var b BuildSettings
	err := s.DB.QueryRow(ctx, `
		SELECT build_mode, dockerfile, build_context, build_target, build_args, build_timeout_minutes,
		       command, port, health_seconds, stop_grace_seconds, release_command
		FROM resources WHERE id = $1`, resourceID).Scan(&b.Mode, &b.Dockerfile, &b.Context, &b.Target, &b.Args, &b.TimeoutMinutes,
		&b.Command, &b.Port, &b.HealthSeconds, &b.StopGrace, &b.ReleaseCommand)
	return b, noRows(err)
}

func (s *Store) SetBuildSettings(ctx context.Context, resourceID string, b BuildSettings) error {
	if b.Args == nil {
		b.Args = []string{}
	}
	_, err := s.DB.Exec(ctx, `
		UPDATE resources SET build_mode = $2, dockerfile = $3, build_context = $4, build_target = $5, build_args = $6,
		       build_timeout_minutes = $7, command = $8, port = $9, health_seconds = $10, stop_grace_seconds = $11,
		       release_command = $12, updated_at = now()
		WHERE id = $1`, resourceID, b.Mode, b.Dockerfile, b.Context, b.Target, b.Args, b.TimeoutMinutes,
		b.Command, b.Port, b.HealthSeconds, b.StopGrace, b.ReleaseCommand)
	return err
}

func (s *Store) SetDeploymentNoCache(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE deployments SET no_cache = true WHERE id = $1`, id)
	return err
}

func (s *Store) DeploymentNoCache(ctx context.Context, id string) bool {
	var v bool
	s.DB.QueryRow(ctx, `SELECT no_cache FROM deployments WHERE id = $1`, id).Scan(&v)
	return v
}

func (s *Store) SetBuildInfo(ctx context.Context, id, info string) error {
	_, err := s.DB.Exec(ctx, `UPDATE deployments SET build_info = $2 WHERE id = $1`, id, info)
	return err
}

// LastBuild is the newest deployment that produced an image.
type LastBuild struct {
	Number    int       `json:"number"`
	Status    string    `json:"status"`
	ImageRef  string    `json:"imageRef"`
	Commit    string    `json:"commit"`
	Branch    string    `json:"branch"`
	BuildInfo string    `json:"buildInfo"`
	At        time.Time `json:"at"`
	Current   bool      `json:"current"`
}

func (s *Store) LastBuild(ctx context.Context, resourceID string) (*LastBuild, error) {
	var b LastBuild
	err := s.DB.QueryRow(ctx, `
		SELECT d.number, d.status, d.image_ref, d.commit_sha, d.branch, d.build_info, COALESCE(d.finished_at, d.created_at),
		       r.current_deployment_id IS NOT DISTINCT FROM d.id
		FROM deployments d JOIN resources r ON r.id = d.resource_id
		WHERE d.resource_id = $1 AND d.image_ref <> '' ORDER BY d.created_at DESC LIMIT 1`, resourceID).
		Scan(&b.Number, &b.Status, &b.ImageRef, &b.Commit, &b.Branch, &b.BuildInfo, &b.At, &b.Current)
	if err != nil {
		return nil, noRows(err)
	}
	return &b, nil
}
