package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxIconBytes = 2 << 20

var imageFileRE = regexp.MustCompile(`^[a-f0-9]{32}\.png$`)

// DecodeConfig checks dimensions before allocating a decoded image. Re-encoding strips metadata.
func normalizeIcon(raw []byte) ([]byte, error) {
	invalid := func(message string) ([]byte, error) { return nil, httpx.Invalid(map[string]string{"file": message}) }
	if len(raw) > maxIconBytes {
		return invalid("Choose an image up to 2 MB.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") {
		return invalid("Choose a PNG, JPEG or WebP image.")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return invalid("Choose an image with at most 16 million pixels and sides up to 8192 pixels.")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return invalid("This image couldn't be read. Choose another image.")
	}
	crop := src.Bounds()
	side := min(crop.Dx(), crop.Dy())
	crop.Min.X += (crop.Dx() - side) / 2
	crop.Min.Y += (crop.Dy() - side) / 2
	crop.Max = crop.Min.Add(image.Pt(side, side))
	dst := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (s *Server) removeIcon(kind, file string) {
	if file == "" || !imageFileRE.MatchString(file) {
		return
	}
	if err := os.Remove(filepath.Join(s.UploadsDir, kind, file)); err != nil && !os.IsNotExist(err) {
		slog.Warn("couldn't remove old icon", "kind", kind, "file", file, "error", err)
	}
}

func (s *Server) changeIcon(w http.ResponseWriter, r *http.Request, u *store.User, kind, id string) error {
	if u.ServiceResourceID != "" || u.CredentialTokenID != "" {
		return httpx.Forbidden("Use your signed-in account to change icons.")
	}
	if s.UploadsDir == "" {
		return httpx.Unavailable("Image storage is not configured.")
	}
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
		dir := filepath.Join(s.UploadsDir, kind)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		file = hex.EncodeToString(random) + ".png"
		// A unique file is fully written before the database points to it.
		if err := os.WriteFile(filepath.Join(dir, file), data, 0600); err != nil {
			s.removeIcon(kind, file)
			return err
		}
	}
	old, err := s.Store.SwapImage(r.Context(), kind, id, file)
	if err != nil {
		s.removeIcon(kind, file)
		return err
	}
	s.removeIcon(kind, old)
	s.Store.Audit(r.Context(), u, "image.update", kind, id, map[string]any{"removed": file == ""}, clientIP(r))
	httpx.JSON(w, 200, map[string]string{"url": store.ImageURL(kind, file)})
	return nil
}

func (s *Server) accountAvatar(w http.ResponseWriter, r *http.Request, u *store.User) error {
	return s.changeIcon(w, r, u, "avatars", u.ID)
}

func (s *Server) teamIcon(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := teamAdmin(u); err != nil {
		return err
	}
	return s.changeIcon(w, r, u, "teams", u.TeamID)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request, u *store.User) error {
	// Do not retain private images in browser/proxy caches after logout or revocation.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	kind, file := r.PathValue("kind"), r.PathValue("filename")
	if s.UploadsDir == "" || (kind != "avatars" && kind != "teams") || !imageFileRE.MatchString(file) {
		return httpx.NotFound("Image")
	}
	if u.ServiceResourceID != "" || u.CredentialTokenID != "" {
		return httpx.Forbidden("Sign in to view account images.")
	}
	visible, err := s.Store.ImageVisible(r.Context(), kind, file, u.ID, roleRank[u.Role] >= roleRank["admin"])
	if err != nil {
		return err
	}
	if !visible {
		return httpx.NotFound("Image")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, filepath.Join(s.UploadsDir, kind, file))
	return nil
}
