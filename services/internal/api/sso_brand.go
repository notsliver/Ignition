package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

// Presets guide configuration; discovery and signed ID-token verification still apply.
func validateSSOPreset(provider, issuer string) string {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(issuer), "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "Enter an HTTPS issuer URL without credentials or a query."
	}
	switch provider {
	case "", "custom", "okta", "authentik", "keycloak", "auth0":
	case "google":
		if u.String() != "https://accounts.google.com" {
			return "The Google issuer is https://accounts.google.com."
		}
	case "discord":
		if u.String() != "https://discord.com" {
			return "The Discord issuer is https://discord.com."
		}
	case "entra":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if u.Host != "login.microsoftonline.com" || len(parts) != 2 || parts[0] == "" || parts[0] == "common" || parts[0] == "organizations" || parts[0] == "consumers" || parts[1] != "v2.0" {
			return "Use a tenant-specific Entra issuer: https://login.microsoftonline.com/YOUR-TENANT-ID/v2.0."
		}
	default:
		return "Choose a supported preset or Custom OpenID Connect."
	}
	return ""
}

type ssoBrand struct {
	File string `json:"file"`
}

func (s *Server) ssoIconURL(ctx context.Context) string {
	var brand ssoBrand
	s.Store.GetSetting(ctx, "sso_brand", &brand)
	if !imageFileRE.MatchString(brand.File) {
		return ""
	}
	return "/api/auth/oidc/icon?v=" + strings.TrimSuffix(brand.File, ".png")
}

// This deliberately public instance logo is visible before sign-in. Account/team images remain private.
func (s *Server) getSSOIcon(w http.ResponseWriter, r *http.Request) error {
	var brand ssoBrand
	s.Store.GetSetting(r.Context(), "sso_brand", &brand)
	if s.UploadsDir == "" || !imageFileRE.MatchString(brand.File) {
		return httpx.NotFound("SSO icon")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, filepath.Join(s.UploadsDir, "sso", brand.File))
	return nil
}
func (s *Server) changeSSOIcon(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if !platformAccountAdmin(u) || u.ServiceResourceID != "" || u.CredentialTokenID != "" {
		return httpx.Forbidden("Only signed-in instance administrators can change the SSO icon.")
	}
	if s.UploadsDir == "" {
		return httpx.Unavailable("Image storage is not configured.")
	}
	var old ssoBrand
	s.Store.GetSetting(r.Context(), "sso_brand", &old)
	file := ""
	if r.Method != http.MethodDelete {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIconBytes))
		if err != nil {
			return httpx.Invalid(map[string]string{"file": "Choose an image up to 2 MB."})
		}
		data, err := normalizeIcon(raw)
		if err != nil {
			return err
		}
		dir := filepath.Join(s.UploadsDir, "sso")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return err
		}
		file = hex.EncodeToString(random) + ".png"
		if err = os.WriteFile(filepath.Join(dir, file), data, 0600); err != nil {
			s.removeIcon("sso", file)
			return err
		}
	}
	if err := s.Store.SetSetting(r.Context(), "sso_brand", ssoBrand{File: file}); err != nil {
		s.removeIcon("sso", file)
		return err
	}
	s.removeIcon("sso", old.File)
	s.Store.Audit(r.Context(), u, "security.sso_icon_updated", "settings", "security", map[string]any{"removed": file == ""}, clientIP(r))
	httpx.JSON(w, 200, map[string]string{"url": s.ssoIconURL(r.Context())})
	return nil
}
