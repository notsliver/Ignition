package dbimport

import (
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	pg := EngineByID("postgresql")
	s, err := ParseURL(pg, "postgres://app:p%40ss@db.example.com/shop?sslmode=require")
	if err != nil || s.Host != "db.example.com" || s.Port != 5432 || s.User != "app" || s.Password != "p@ss" || s.Database != "shop" || !s.TLS {
		t.Fatalf("%+v %v", s, err)
	}
	bad := map[string]string{
		"mysql://u:p@h/db":                 "starts with postgres://",
		"postgres://u:p@localhost:5432/db": "localhost",
		"postgres://u:p@h:99999/db":        "port",
		"not a url":                        "enter a URL",
	}
	for raw, want := range bad {
		if _, err := ParseURL(pg, raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", raw, err, want)
		}
	}
	if _, err := ParseURL(EngineByID("mysql"), "mysql://u:p@h:3306"); err == nil {
		t.Error("MySQL needs a database name")
	}
	if _, err := ParseURL(EngineByID("redis"), "rediss://h:6380"); err == nil {
		t.Error("TLS replication isn't supported and must be refused")
	}
	if s, err := ParseURL(EngineByID("redis"), "redis://:pw@cache.internal:6380/0"); err != nil || s.Port != 6380 || s.Password != "pw" {
		t.Errorf("redis: %+v %v", s, err)
	}
}

func TestMasked(t *testing.T) {
	if got := Masked("postgres://app:secret@h:5432/db?sslmode=require&password=x"); got != "postgres://app@h:5432/db?sslmode=require" {
		t.Errorf("got %s", got)
	}
}

func TestEveryEngineHasScripts(t *testing.T) {
	for _, e := range Engines {
		if FileScript(&e) == "" || LiveScript(&e) == "" || len(e.Formats) == 0 {
			t.Errorf("%s is missing a script", e.ID)
		}
		if strings.Contains(FileScript(&e)+LiveScript(&e), "SRC_PASS\"") && strings.Contains(FileScript(&e)+LiveScript(&e), "echo \"$SRC_PASS") {
			t.Errorf("%s prints the source password", e.ID)
		}
	}
}
