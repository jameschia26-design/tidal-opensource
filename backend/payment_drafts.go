package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

const maxPaymentDrafts = 200

// paymentDrafts lists (GET) and saves (POST) unposted payment forms. A draft is just the saved form; it books nothing.
func (a *app) paymentDrafts(w http.ResponseWriter, r *http.Request, u currentUser) {
	if r.Method == http.MethodGet {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, false)
		if !ok {
			return
		}
		side := r.URL.Query().Get("side")
		if side != "received" && side != "made" {
			writeError(w, 400, "invalid_input")
			return
		}
		rows, err := a.db.Query(r.Context(), `SELECT id,data,created_at::date::text FROM payment_drafts WHERE org_id=$1 AND side=$2 ORDER BY id DESC LIMIT $3`, access.ID, side, maxPaymentDrafts)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var data json.RawMessage
			var day string
			if err := rows.Scan(&id, &data, &day); err != nil {
				serverError(w, err)
				return
			}
			out = append(out, map[string]any{"id": id, "data": data, "saved_on": day})
		}
		if err := rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"drafts": out})
		return
	}
	var in struct {
		OrgID int64           `json:"org_id"`
		Side  string          `json:"side"`
		Data  json.RawMessage `json:"data"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var obj map[string]any
	if (in.Side != "received" && in.Side != "made") || len(in.Data) > 20000 || json.Unmarshal(in.Data, &obj) != nil || obj == nil {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		tag, err := a.db.Exec(r.Context(), `UPDATE payment_drafts SET data=$1 WHERE org_id=$2 AND id=$3 AND side=$4`, []byte(in.Data), access.ID, id, in.Side)
		if err != nil {
			serverError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "payment-drafts:"+strconv.FormatInt(access.ID, 10)); err != nil {
		serverError(w, err)
		return
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM payment_drafts WHERE org_id=$1`, access.ID).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	if n >= maxPaymentDrafts {
		writeError(w, 422, "draft_limit")
		return
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO payment_drafts(org_id,side,data,created_by) VALUES($1,$2,$3,$4) RETURNING id`, access.ID, in.Side, []byte(in.Data), u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *app) deletePaymentDraft(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	tag, err := a.db.Exec(r.Context(), `DELETE FROM payment_drafts WHERE org_id=$1 AND id=$2`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
