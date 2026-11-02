package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestSSOPresetIssuer(t *testing.T) {
	for _, c := range []struct {
		provider, issuer string
		valid            bool
	}{
		{"google", "https://accounts.google.com", true}, {"discord", "https://discord.com", true},
		{"google", "https://attacker.test", false}, {"discord", "https://discord.com@attacker.test", false},
		{"entra", "https://login.microsoftonline.com/tenant-id/v2.0", true},
		{"entra", "https://login.microsoftonline.com/common/v2.0", false},
		{"custom", "https://auth.example.com/realm", true}, {"custom", "http://auth.example.com", false},
		{"unknown", "https://auth.example.com", false},
	} {
		if (validateSSOPreset(c.provider, c.issuer) == "") != c.valid {
			t.Errorf("%s %s", c.provider, c.issuer)
		}
	}
}
func TestSSOIconStorageAndAccess(t *testing.T) {
	dsn := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set IGNITION_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{DB: pool}
	s := &Server{Store: st, UploadsDir: t.TempDir()}
	admin, err := st.CreateUser(ctx, strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")+"@sso-icon.test", "Admin", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/settings/security/oidc/icon", bytes.NewReader(iconFixture(t)))
	if err = s.changeSSOIcon(httptest.NewRecorder(), req, &store.User{Role: "owner", InstanceRole: "viewer"}); err == nil {
		t.Fatal("team owner changed instance branding")
	}
	if err = s.changeSSOIcon(httptest.NewRecorder(), req, admin); err != nil {
		t.Fatal(err)
	}
	url := s.ssoIconURL(ctx)
	if !strings.HasPrefix(url, "/api/auth/oidc/icon?v=") {
		t.Fatal(url)
	}
	out := httptest.NewRecorder()
	if err = s.getSSOIcon(out, httptest.NewRequest("GET", url, nil)); err != nil {
		t.Fatal(err)
	}
	if out.Code != 200 || out.Header().Get("Content-Type") != "image/png" || out.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(out.Code, out.Header())
	}
	if err = s.changeSSOIcon(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader("<svg/>")), admin); err == nil {
		t.Fatal("accepted active-content SVG")
	}
	if err = s.changeSSOIcon(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/", nil), admin); err != nil {
		t.Fatal(err)
	}
	if s.ssoIconURL(ctx) != "" {
		t.Fatal("logo remained after removal")
	}
	if err = s.getSSOIcon(httptest.NewRecorder(), httptest.NewRequest("GET", url, nil)); err == nil {
		t.Fatal("removed icon served")
	}
}
