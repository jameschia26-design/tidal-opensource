package main

import (
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Documents: an org-wide file store with an Inbox (no folder), folders and a Trash.
// ponytail: files live in the database like attachments; 500 live files per company. Autoscan (reading receipts) is not built.

const maxDocuments = 500

func (a *app) listDocuments(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	// view: all (not trashed), inbox, trash, or folder with folder_id
	where, args := "trashed_at IS NULL", []any{access.ID}
	switch v := r.URL.Query().Get("view"); v {
	case "", "all":
	case "inbox":
		where += " AND folder_id IS NULL"
	case "trash":
		where = "trashed_at IS NOT NULL"
	case "folder":
		fid, err := strconv.ParseInt(r.URL.Query().Get("folder_id"), 10, 64)
		if err != nil || fid <= 0 {
			writeError(w, 400, "invalid_input")
			return
		}
		where += " AND folder_id=$2"
		args = append(args, fid)
	default:
		writeError(w, 400, "invalid_input")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,COALESCE(folder_id,0),filename,octet_length(contents),created_at::date::text FROM documents WHERE org_id=$1 AND `+where+` ORDER BY id DESC LIMIT 500`, args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, folder int64
		var name, day string
		var size int
		if err := rows.Scan(&id, &folder, &name, &size, &day); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "folder_id": folder, "filename": name, "size": size, "added_on": day})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"documents": out})
}

func (a *app) uploadDocument(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, true)
	if !ok {
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
	var folder *int64
	if v := r.URL.Query().Get("folder_id"); v != "" {
		fid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || fid <= 0 {
			writeError(w, 400, "invalid_input")
			return
		}
		folder = &fid
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "documents:"+strconv.FormatInt(access.ID, 10)); err != nil {
		serverError(w, err)
		return
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM documents WHERE org_id=$1`, access.ID).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	if n >= maxDocuments {
		writeError(w, 422, "attachment_limit")
		return
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO documents(org_id,folder_id,filename,content_type,contents,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, access.ID, folder, name, http.DetectContentType(contents), contents, u.ID).Scan(&id); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, 404, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'document.uploaded')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "filename": name})
}

func (a *app) downloadDocument(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	var name string
	var contents []byte
	if err := a.db.QueryRow(r.Context(), `SELECT filename,contents FROM documents WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&name, &contents); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 404, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	// Always a download, never rendered in the browser: uploads are untrusted.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r > 126 {
			return '_'
		}
		return r
	}, name)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(contents)
}

// documentAction moves a document to a folder or the inbox, trashes it, restores it, or deletes it for good (trash only).
func (a *app) documentAction(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID    int64  `json:"org_id"`
		Action   string `json:"action"`
		FolderID int64  `json:"folder_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validStage(in.Action, "move", "trash", "restore", "delete") || in.FolderID < 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	var sql string
	var args []any
	dropReceiptLink := false
	switch in.Action {
	case "move":
		sql, args = `UPDATE documents SET folder_id=NULLIF($3,0) WHERE org_id=$1 AND id=$2 AND trashed_at IS NULL`, []any{access.ID, id, in.FolderID}
	case "trash":
		sql, args = `UPDATE documents SET trashed_at=now() WHERE org_id=$1 AND id=$2 AND trashed_at IS NULL`, []any{access.ID, id}
	case "restore":
		sql, args = `UPDATE documents SET trashed_at=NULL WHERE org_id=$1 AND id=$2 AND trashed_at IS NOT NULL`, []any{access.ID, id}
	default:
		// Retention first, so the refusal is a clear 409 rather than a database error (R25).
		var retained bool
		if err := a.db.QueryRow(ctx, `SELECT d.legal_hold OR EXISTS (
				SELECT 1 FROM expense_receipts er JOIN expenses e ON e.org_id=er.org_id AND e.id=er.expense_id
				WHERE er.org_id=d.org_id AND er.document_id=d.id
				  AND e.expense_date > current_date - make_interval(years => o.retention_years))
			FROM documents d JOIN organizations o ON o.id=d.org_id
			WHERE d.org_id=$1 AND d.id=$2`, access.ID, id).Scan(&retained); err != nil {
			serverError(w, err)
			return
		}
		if retained {
			writeError(w, http.StatusConflict, "document_retained")
			return
		}
		sql, args = `DELETE FROM documents WHERE org_id=$1 AND id=$2 AND trashed_at IS NOT NULL`, []any{access.ID, id}
		// Past retention the link row may go with it; inside retention the handler already refused.
		dropReceiptLink = true
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if dropReceiptLink {
		if _, err := tx.Exec(ctx, `DELETE FROM expense_receipts WHERE org_id=$1 AND document_id=$2`, access.ID, id); err != nil {
			serverError(w, err)
			return
		}
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, 404, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,$3)`, access.ID, u.ID, "document."+in.Action); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (a *app) documentFolders(w http.ResponseWriter, r *http.Request, u currentUser) {
	if r.Method == http.MethodGet {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, false)
		if !ok {
			return
		}
		rows, err := a.db.Query(r.Context(), `SELECT f.id,f.name,(SELECT count(*) FROM documents d WHERE d.org_id=f.org_id AND d.folder_id=f.id AND d.trashed_at IS NULL) FROM document_folders f WHERE f.org_id=$1 ORDER BY lower(f.name) LIMIT 200`, access.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, n int64
			var name string
			if err := rows.Scan(&id, &name, &n); err != nil {
				serverError(w, err)
				return
			}
			out = append(out, map[string]any{"id": id, "name": name, "documents": n})
		}
		if err := rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"folders": out})
		return
	}
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 100) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "document-folders:"+strconv.FormatInt(access.ID, 10)); err != nil {
		serverError(w, err)
		return
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO document_folders(org_id,name) SELECT $1,$2 WHERE (SELECT count(*) FROM document_folders WHERE org_id=$1)<200 RETURNING id`, access.ID, in.Name).Scan(&id); err != nil {
		switch {
		case err == pgx.ErrNoRows:
			writeError(w, 422, "attachment_limit")
		case isUniqueViolation(err):
			writeError(w, 409, "extras_exists")
		default:
			serverError(w, err)
		}
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// deleteDocumentFolder removes an empty folder. Any document in it (trashed too) blocks the delete.
func (a *app) deleteDocumentFolder(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, true)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM document_folders f WHERE f.org_id=$1 AND f.id=$2 AND NOT EXISTS (SELECT 1 FROM documents d WHERE d.org_id=f.org_id AND d.folder_id=f.id)`, access.ID, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, 409, "folder_not_empty")
		} else {
			serverError(w, err)
		}
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 409, "folder_not_empty")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
