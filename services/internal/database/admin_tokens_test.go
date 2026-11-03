package database

import (
	"context"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
)

// A separate disposable database exercises the historical prototype upgrade, not live tables.
func TestLegacyAdminTokenUpgrade(t *testing.T) {
	dsn := os.Getenv("IGNITION_TEST_ADMIN_MIGRATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("IGNITION_TEST_ADMIN_MIGRATION_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var present bool
	pool.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&present)
	if present {
		t.Skip("requires an empty disposable database")
	}
	if _, e = pool.Exec(ctx, `CREATE TABLE schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		t.Fatal(e)
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	for _, name := range names {
		if strings.Contains(name, "0036_") {
			break
		}
		raw, _ := migrations.ReadFile(name)
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(raw)); e != nil {
			tx.Rollback(ctx)
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")); e != nil {
			tx.Rollback(ctx)
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	var user string
	if e = pool.QueryRow(ctx, `INSERT INTO users(email,name,password_hash,role) VALUES('migration-admin@test.example','Migration admin','hash','admin') RETURNING id`).Scan(&user); e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `CREATE TABLE admin_tokens(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),name text NOT NULL CHECK(length(name) BETWEEN 1 AND 60),token_hash bytea UNIQUE NOT NULL,prefix text NOT NULL,scopes text[] NOT NULL,allow_ips text[] NOT NULL DEFAULT '{}',created_by uuid NOT NULL REFERENCES users(id),expires_at timestamptz,last_used_at timestamptz,last_used_ip text NOT NULL DEFAULT '',uses bigint NOT NULL DEFAULT 0,rotated_at timestamptz,created_at timestamptz NOT NULL DEFAULT now())`)
	if e != nil {
		t.Fatal(e)
	}
	var legacy string
	e = pool.QueryRow(ctx, `INSERT INTO admin_tokens(name,token_hash,prefix,scopes,allow_ips,created_by,last_used_ip) VALUES('Legacy',decode(repeat('11',32),'hex'),'legacy-prefix',ARRAY['*'],ARRAY['127.0.0.1/32'],$1,'127.0.0.1') RETURNING id`, user).Scan(&legacy)
	if e != nil {
		t.Fatal(e)
	}
	pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES('0033_admin_tokens')`)
	if e = Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	var name, ip string
	var revoked, expires bool
	var scopes, networks []string
	if e = pool.QueryRow(ctx, `SELECT name,last_ip,revoked_at IS NOT NULL,expires_at IS NOT NULL,permissions,networks FROM admin_tokens WHERE id=$1`, legacy).Scan(&name, &ip, &revoked, &expires, &scopes, &networks); e != nil {
		t.Fatal(e)
	}
	if name != "Legacy" || ip != "127.0.0.1" || !revoked || !expires || len(scopes) != 0 || len(networks) != 1 || networks[0] != "127.0.0.1/32" {
		t.Fatal("legacy metadata or authority was migrated incorrectly")
	}
	var modern string
	e = pool.QueryRow(ctx, `INSERT INTO admin_tokens(name,token_hash,prefix,permissions,networks,created_by,expires_at) VALUES($1,decode(repeat('22',32),'hex'),'ign_admin_example',ARRAY['users.view'],'{}',$2,now()+interval '30 days') RETURNING id`, strings.Repeat("n", 80), user).Scan(&modern)
	if e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL,permissions FROM admin_tokens WHERE id=$1`, modern).Scan(&revoked, &scopes); e != nil {
		t.Fatal(e)
	}
	if revoked || len(scopes) != 1 || scopes[0] != "users.view" {
		t.Fatal("a subsequent startup invalidated the new credential")
	}
}
