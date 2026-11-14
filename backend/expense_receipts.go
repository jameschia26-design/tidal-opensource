package main

import (
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Receipts Inbox: receipt files already in Documents, shown as unlinked until attached to an expense.
// Linking writes one row to expense_receipts; the expense itself is never changed.

// listUnlinkedReceipts returns documents that are not yet linked to any expense.
func (a *app) listUnlinkedReceipts(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT d.id,d.filename,octet_length(d.contents),d.created_at::date::text FROM documents d
		WHERE d.org_id=$1 AND d.trashed_at IS NULL AND NOT EXISTS (SELECT 1 FROM expense_receipts x WHERE x.org_id=d.org_id AND x.document_id=d.id)
		ORDER BY d.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, day string
		var size int
		if err := rows.Scan(&id, &name, &size, &day); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "filename": name, "size": size, "added_on": day})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": out})
}

// linkReceipt attaches an unlinked receipt document to one expense. Each receipt and each expense link at most once.
func (a *app) linkReceipt(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID     int64 `json:"org_id"`
		ExpenseID int64 `json:"expense_id"`
	}
	if !decodeJSON(w, r, &in) || in.ExpenseID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	documentID, ok := pathID(w, r)
	if !ok {
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
	var found int64
	if err := tx.QueryRow(ctx, `SELECT id FROM documents WHERE org_id=$1 AND id=$2 AND trashed_at IS NULL FOR UPDATE`, access.ID, documentID).Scan(&found); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO expense_receipts(org_id,document_id,expense_id) VALUES($1,$2,$3)`, access.ID, documentID, in.ExpenseID); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "receipt_already_linked")
		} else if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'receipt.linked',jsonb_build_object('document_id',$3::bigint,'expense_id',$4::bigint))`, access.ID, u.ID, documentID, in.ExpenseID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"document_id": documentID, "expense_id": in.ExpenseID})
}
