package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func attachmentTable(kind string) (string, int) {
	switch kind {
	case "contacts":
		return "contacts", 10
	case "invoices":
		return "invoices", 10
	case "quotes":
		return "quotes", 5
	case "bills":
		return "bills", 5
	case "items":
		return "items", 10
	case "expenses":
		return "expenses", 10
	case "sales-receipts":
		return "sales_receipts", 5
	case "payments-received":
		return "payments_received", 5
	case "payments-made":
		return "payments_made", 5
	case "billing-plans":
		return "billing_plans", 5
	case "billing-addons":
		return "billing_addons", 5
	case "credit-notes":
		return "customer_credit_notes", 5
	case "purchase-orders":
		return "purchase_orders", 10
	case "purchase-receives":
		return "purchase_receives", 5
	}
	return "", 0
}

func (a *app) entityAttachments(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, r.Method != "GET")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	table, limit := attachmentTable(kind)
	if table == "" {
		writeError(w, 400, "invalid_input")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var found int64
	// Table name comes exclusively from the allowlist above. Row lock serializes upload limits.
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE org_id=$1 AND id=$2 FOR UPDATE`, table), access.ID, id).Scan(&found); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 404, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if r.Method == http.MethodGet {
		rows, err := tx.Query(ctx, `SELECT id,filename,octet_length(contents) FROM attachments WHERE org_id=$1 AND entity_kind=$2 AND entity_id=$3 ORDER BY id`, access.ID, kind, id)
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		for rows.Next() {
			var aid int64
			var name string
			var size int
			if err := rows.Scan(&aid, &name, &size); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			out = append(out, map[string]any{"id": aid, "filename": name, "size": size})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"attachments": out})
		return
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE org_id=$1 AND entity_kind=$2 AND entity_id=$3`, access.ID, kind, id).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= limit {
		writeError(w, 422, "attachment_limit")
		return
	}
	const max = 10 << 20
	r.Body = http.MaxBytesReader(w, r.Body, max+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		writeError(w, 400, "invalid_upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "invalid_upload")
		return
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil || len(contents) == 0 || len(contents) > max {
		writeError(w, 422, "invalid_upload")
		return
	}
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if !validTaxText(name, 200) || name == "" {
		writeError(w, 400, "invalid_upload")
		return
	}
	var aid int64
	if err := tx.QueryRow(ctx, `INSERT INTO attachments(org_id,entity_kind,entity_id,filename,content_type,contents,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, access.ID, kind, id, name, http.DetectContentType(contents), contents, u.ID).Scan(&aid); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'attachment.uploaded')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": aid, "filename": name, "size": len(contents)})
}

func (a *app) downloadAttachment(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var name, typ string
	var contents []byte
	err := a.db.QueryRow(r.Context(), `SELECT filename,content_type,contents FROM attachments WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&name, &typ, &contents)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 404, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	disposition := "attachment"
	contentType := "application/octet-stream"
	// ponytail: previews allow only browser-supported raster types; add other types after a security review.
	if r.URL.Query().Get("preview") == "1" {
		switch http.DetectContentType(contents) {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			contentType = http.DetectContentType(contents)
			disposition = "inline"
		default:
			writeError(w, http.StatusUnsupportedMediaType, "invalid_input")
			return
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	w.Header().Set("Content-Security-Policy", "sandbox")
	_, _ = w.Write(contents)
}
