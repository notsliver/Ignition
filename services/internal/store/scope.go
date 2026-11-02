package store

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Scope is immutable request authority. Background authentication and worker dispatch
// use the privileged pool explicitly; all application queries use SQL(ctx).
type Scope struct {
	TeamID, UserID, Permission string
	CredentialResourceID       string
	MinRank                    int
	// InstanceAdmin: a platform administrator working in a team they aren't a member of, as its
	// owner. Row security lets them through, and every audit entry says so.
	InstanceAdmin bool
}
type scopeKey struct{}

func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}
func ScopeFrom(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(Scope)
	return s, ok
}

type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

func (s *Store) SQL(ctx context.Context) Querier {
	if scope, ok := ScopeFrom(ctx); ok {
		return scopedDB{s: s, scope: scope}
	}
	return s.DB
}

type scopedDB struct {
	s     *Store
	scope Scope
}

func (d scopedDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := d.s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE ignition_request`); err == nil {
		_, err = tx.Exec(ctx, `SELECT set_config('ignition.team_id',$1,true),set_config('ignition.user_id',$2,true),set_config('ignition.permission',$3,true),set_config('ignition.min_rank',$4,true),set_config('ignition.resource_id',$5,true),set_config('ignition.instance_admin',$6,true)`, d.scope.TeamID, d.scope.UserID, d.scope.Permission, strconv.Itoa(d.scope.MinRank), d.scope.CredentialResourceID, strconv.FormatBool(d.scope.InstanceAdmin))
	}
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func (d scopedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx, err := d.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return tag, err
	}
	return tag, tx.Commit(ctx)
}
func (d scopedDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx, err := d.Begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return &scopedRows{Rows: rows, tx: tx, ctx: ctx}, nil
}
func (d scopedDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tx, err := d.Begin(ctx)
	if err != nil {
		return scopedRow{err: err}
	}
	return scopedRow{row: tx.QueryRow(ctx, sql, args...), tx: tx, ctx: ctx}
}

type scopedRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
	err error
}

func (r scopedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.tx.Rollback(r.ctx)
	if err := r.row.Scan(dest...); err != nil {
		return err
	}
	return r.tx.Commit(r.ctx)
}

type scopedRows struct {
	pgx.Rows
	tx   pgx.Tx
	ctx  context.Context
	done bool
	err  error
}

func (r *scopedRows) Close() {
	if r.done {
		return
	}
	r.done = true
	r.Rows.Close()
	if r.Rows.Err() != nil {
		r.err = r.Rows.Err()
		r.tx.Rollback(r.ctx)
	} else {
		r.err = r.tx.Commit(r.ctx)
	}
}
func (r *scopedRows) Next() bool {
	if r.done {
		return false
	}
	if r.Rows.Next() {
		return true
	}
	r.Close()
	return false
}
func (r *scopedRows) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.Rows.Err()
}

type auditTeamKey struct{}

func WithAuditTeam(ctx context.Context, team string) context.Context {
	return context.WithValue(ctx, auditTeamKey{}, team)
}
