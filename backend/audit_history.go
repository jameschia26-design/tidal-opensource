package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Audit history (R26): each financial change stores the record it touched, and owners or admins can read
// and filter it. Events stay append-only through the original trigger.

// execer is satisfied by both the pool and a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// recordAudit writes one event with the entity it changed.
func recordAudit(ctx context.Context, q execer, orgID, userID int64, kind string, entityID int64, action string, detail any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail,entity_kind,entity_id)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,0))`, orgID, userID, action, detail, kind, entityID)
	return err
}

// listAuditEvents: GET /api/v1/audit-events?org_id&from&to&user_id&entity_kind&entity_id&limit&offset
func (a *app) listAuditEvents(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	limit, ok := parseAuditLimit(q.Get("limit"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	offset, ok := parseAccountID(q.Get("offset"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	args := []any{access.ID}
	where := []string{"org_id=$1"}
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, clause)
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		if _, ok := parseDay(v); !ok {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		add(`e.created_at >= $`+strconv.Itoa(len(args)+1), v)
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		if _, ok := parseDay(v); !ok {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		add(`e.created_at < ($`+strconv.Itoa(len(args)+1)+`::date + 1)`, v)
	}
	if v := strings.TrimSpace(q.Get("user_id")); v != "" {
		id, ok := parseAccountID(v)
		if !ok || id == 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		add(`user_id=$`+strconv.Itoa(len(args)+1), id)
	}
	if v := strings.TrimSpace(q.Get("entity_kind")); v != "" {
		if !validTaxText(v, 40) {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		add(`entity_kind=$`+strconv.Itoa(len(args)+1), v)
	}
	if v := strings.TrimSpace(q.Get("entity_id")); v != "" {
		id, ok := parseAccountID(v)
		if !ok || id == 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		add(`entity_id=$`+strconv.Itoa(len(args)+1), id)
	}
	args = append(args, limit, offset)
	rows, err := a.db.Query(r.Context(), `SELECT e.id,e.created_at,e.user_id,COALESCE(u.email,''),e.action,e.entity_kind,e.entity_id,e.detail
		FROM audit_events e LEFT JOIN users u ON u.id=e.user_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY e.id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var created any
		var userID *int64
		var email, action string
		var kind *string
		var entityID *int64
		var detail []byte
		if err := rows.Scan(&id, &created, &userID, &email, &action, &kind, &entityID, &detail); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "created_at": created, "user_id": userID, "user_email": email,
			"action": action, "entity_kind": kind, "entity_id": entityID, "detail": json.RawMessage(detail)})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if q.Get("format") == "csv" {
		// The same rows as a file, so an owner can hand the history to an investigator or auditor.
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-history.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "created_at", "user_email", "action", "entity_kind", "entity_id", "detail"})
		for _, e := range out {
			_ = cw.Write([]string{
				strconv.FormatInt(e["id"].(int64), 10), fmt.Sprint(e["created_at"]), csvSafeCell(fmt.Sprint(e["user_email"])),
				csvSafeCell(fmt.Sprint(e["action"])), csvSafeCell(fmt.Sprint(e["entity_kind"])), fmt.Sprint(e["entity_id"]),
				csvSafeCell(string(e["detail"].(json.RawMessage))),
			})
		}
		cw.Flush()
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// parseAuditLimit caps a page at 500 events.
func parseAuditLimit(raw string) (int64, bool) {
	if strings.TrimSpace(raw) == "" {
		return 100, true
	}
	v, ok := parseAccountID(raw)
	if !ok || v == 0 || v > 500 {
		return 0, false
	}
	return v, true
}
