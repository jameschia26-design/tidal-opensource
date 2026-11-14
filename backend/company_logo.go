package main

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Company logo: one image per company, printed in the corner of every document PDF.
// PNG and JPEG only; the bytes are stored as sent and decoded again at print time.
// ponytail: no theme-specific logos and no cropping controls; add when documents need per-theme logos.

const (
	logoMaxBytes = 1 << 20
	// logoMaxPixels caps each side before decoding: a tiny file can describe a huge image (R19).
	logoMaxPixels = 4000
)

// logoTypeOf returns the stored type name for an image, or "" when the bytes are neither PNG nor JPEG.
func logoTypeOf(b []byte) string {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return ""
	}
	if cfg.Width > logoMaxPixels || cfg.Height > logoMaxPixels || cfg.Width <= 0 || cfg.Height <= 0 {
		return ""
	}
	switch format {
	case "png", "jpeg":
		return format
	}
	return ""
}

// canEditLogo mirrors the company profile rule: owners and administrators only.
func (a *app) canEditLogo(w http.ResponseWriter, r *http.Request, u currentUser, orgID int64) (int64, bool) {
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return 0, false
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return 0, false
	}
	return access.ID, true
}

// uploadCompanyLogo stores one PNG or JPEG, replacing any earlier logo.
func (a *app) uploadCompanyLogo(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.PathValue("id"))
	if !ok {
		return
	}
	id, ok := a.canEditLogo(w, r, u, orgID)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, logoMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, logoMaxBytes+1))
	if err != nil || len(contents) == 0 || len(contents) > logoMaxBytes {
		writeError(w, http.StatusUnprocessableEntity, "invalid_upload")
		return
	}
	kind := logoTypeOf(contents)
	if kind == "" {
		writeError(w, http.StatusUnprocessableEntity, "logo_type_invalid")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE organizations SET logo_bytes=$2, logo_type=$3 WHERE id=$1`, id, contents, kind); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"has_logo": true, "logo_type": kind})
}

// deleteCompanyLogo removes the logo; documents then print without one.
func (a *app) deleteCompanyLogo(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.PathValue("id"))
	if !ok {
		return
	}
	id, ok := a.canEditLogo(w, r, u, orgID)
	if !ok {
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE organizations SET logo_bytes=NULL, logo_type=NULL WHERE id=$1`, id); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getCompanyLogo streams the stored bytes for the settings preview. Any member may look.
func (a *app) getCompanyLogo(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.PathValue("id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	var contents []byte
	var kind string
	err := a.db.QueryRow(r.Context(), `SELECT COALESCE(logo_bytes,''), COALESCE(logo_type,'') FROM organizations WHERE id=$1`, access.ID).Scan(&contents, &kind)
	if errors.Is(err, pgx.ErrNoRows) || len(contents) == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	contentType := "image/png"
	if kind == "jpeg" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(contents)
}
