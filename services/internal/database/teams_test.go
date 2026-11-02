package database

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
)

// Uses a separate empty disposable database, never the normal integration database.
func TestTeamsUpgradePreservesLegacyWorkloads(t *testing.T) {
	url := os.Getenv("IGNITION_TEST_MIGRATION_DATABASE_URL")
	if url == "" {
		t.Skip("IGNITION_TEST_MIGRATION_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var exists bool
	pool.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists)
	if exists {
		t.Skip("upgrade fixture requires an empty disposable database")
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	for _, name := range names {
		if strings.Contains(name, "0025_") {
			break
		}
		raw, _ := migrations.ReadFile(name)
		tx, _ := pool.Begin(ctx)
		if _, err = tx.Exec(ctx, string(raw)); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var user, project, environment, resource string
	if err = pool.QueryRow(ctx, `INSERT INTO users(email,name,password_hash,role,permissions) VALUES('legacy@example.test','Legacy','hash','developer','{"grant":["projects.create"],"deny":["variables.reveal"]}') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO projects(name,created_by) VALUES('Existing',$1) RETURNING id`, user).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO environments(project_id,name) VALUES($1,'Production') RETURNING id`, project).Scan(&environment); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO resources(project_id,environment_id,name,type,source_type,image,container_id) VALUES($1,$2,'legacy','worker','image','busybox','existing-container') RETURNING id`, project, environment).Scan(&resource); err != nil {
		t.Fatal(err)
	}
	ciphertext := []byte{1, 2, 3, 4, 5}
	pool.Exec(ctx, `INSERT INTO env_vars(resource_id,key,value_enc,secret) VALUES($1,'SECRET',$2,true)`, resource, ciphertext)
	pool.Exec(ctx, `INSERT INTO settings(key,value) VALUES('onboarding','{"useCase":"hosting","pending":false}')`)
	pool.Exec(ctx, `INSERT INTO user_tokens(kind,token_hash,email,role,expires_at) VALUES('invite',$1,'pending@example.test','viewer',now()+interval '1 day')`, []byte("legacy-invitation"))
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var team, afterProject, afterEnvironment, container, role string
	var after []byte
	if err = pool.QueryRow(ctx, `SELECT r.team_id,r.project_id,r.environment_id,r.container_id,v.value_enc FROM resources r JOIN env_vars v ON v.resource_id=r.id WHERE r.id=$1`, resource).Scan(&team, &afterProject, &afterEnvironment, &container, &after); err != nil {
		t.Fatal(err)
	}
	if team != "00000000-0000-4000-8000-000000000001" || afterProject != project || afterEnvironment != environment || container != "existing-container" || !bytes.Equal(after, ciphertext) {
		t.Fatal("upgrade changed resource identity, deployment context or ciphertext")
	}
	var allowed, denied bool
	if err = pool.QueryRow(ctx, `SELECT role,permissions->'grant' ? 'projects.create',permissions->'deny' ? 'variables.reveal' FROM team_memberships WHERE team_id=$1 AND user_id=$2`, team, user).Scan(&role, &allowed, &denied); err != nil || role != "developer" || !allowed || !denied {
		t.Fatalf("membership did not preserve legacy permission overrides: %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM teams`).Scan(&n)
	if n != 1 {
		t.Fatal("migration replay duplicated the default team")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM project_environments WHERE project_id=$1 AND environment_id=$2`, project, environment).Scan(&n)
	if n != 1 {
		t.Fatal("legacy environment grouping was lost")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM team_experiences WHERE team_id=$1`, team).Scan(&n)
	if n != 2 {
		t.Fatal("Hosting and Infrastructure are not attached to the same team")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM user_tokens WHERE kind='invite' AND team_id=$1`, team).Scan(&n)
	if n != 1 {
		t.Fatal("pending legacy invitation lost its team")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM resources WHERE team_id IS NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("upgrade left unowned resources")
	}
}
