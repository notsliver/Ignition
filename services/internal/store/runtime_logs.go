package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type RuntimeLine struct {
	Time    time.Time `json:"time"`
	Stream  string    `json:"stream"`
	Message string    `json:"message"`
}

// ArchiveRuntimeLogs stores a retired container's output under its deployment (replacing any earlier archive)
// and prunes archives beyond the newest keep per resource.
func (s *Store) ArchiveRuntimeLogs(ctx context.Context, deploymentID string, lines []RuntimeLine, keep int) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM runtime_logs WHERE deployment_id = $1`, deploymentID); err != nil {
		return err
	}
	rows := make([][]any, 0, len(lines))
	for i, l := range lines {
		stream := l.Stream
		if stream != "stderr" {
			stream = "stdout"
		}
		rows = append(rows, []any{deploymentID, i, l.Time, stream, l.Message})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"runtime_logs"}, []string{"deployment_id", "seq", "at", "stream", "message"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE deployments SET logs_archived_at = now(), logs_lines = $2 WHERE id = $1`, deploymentID, len(lines)); err != nil {
		return err
	}
	// Keep the newest archives of this resource; older ones are dropped.
	if _, err := tx.Exec(ctx, `
		WITH old AS (
			SELECT d.id FROM deployments d
			WHERE d.resource_id = (SELECT resource_id FROM deployments WHERE id = $1) AND d.logs_archived_at IS NOT NULL
			ORDER BY d.logs_archived_at DESC OFFSET $2
		), dropped AS (
			DELETE FROM runtime_logs WHERE deployment_id IN (SELECT id FROM old)
		)
		UPDATE deployments SET logs_archived_at = NULL, logs_lines = 0 WHERE id IN (SELECT id FROM old)`, deploymentID, keep); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ArchivedRuntimeLogs returns the newest `tail` lines of an archived deployment at or after since.
func (s *Store) ArchivedRuntimeLogs(ctx context.Context, deploymentID string, since time.Time, tail int) ([]RuntimeLine, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT at, stream, message FROM (
			SELECT seq, at, stream, message FROM runtime_logs
			WHERE deployment_id = $1 AND at >= $2 ORDER BY seq DESC LIMIT $3
		) t ORDER BY seq`, deploymentID, since, tail)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeLine{}
	for rows.Next() {
		var l RuntimeLine
		if err := rows.Scan(&l.Time, &l.Stream, &l.Message); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LogSource is a deployment whose runtime logs can be read: the running one (from Docker) or an archived one.
type LogSource struct {
	DeploymentID string     `json:"deploymentId"`
	Number       int        `json:"number"`
	Status       string     `json:"status"`
	Trigger      string     `json:"trigger"`
	Commit       string     `json:"commit"`
	Current      bool       `json:"current"`
	StartedAt    *time.Time `json:"startedAt"`
	ArchivedAt   *time.Time `json:"archivedAt"`
	Lines        int        `json:"lines"`
}

func (s *Store) LogSources(ctx context.Context, resourceID string) ([]LogSource, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.id, d.number, d.status, d.trigger, d.commit_sha, r.current_deployment_id IS NOT DISTINCT FROM d.id,
		       COALESCE(d.started_at, d.created_at), d.logs_archived_at, d.logs_lines
		FROM deployments d JOIN resources r ON r.id = d.resource_id
		WHERE d.resource_id = $1 AND (r.current_deployment_id IS NOT DISTINCT FROM d.id OR d.logs_archived_at IS NOT NULL)
		ORDER BY d.number DESC`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogSource{}
	for rows.Next() {
		var l LogSource
		if err := rows.Scan(&l.DeploymentID, &l.Number, &l.Status, &l.Trigger, &l.Commit, &l.Current, &l.StartedAt, &l.ArchivedAt, &l.Lines); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
