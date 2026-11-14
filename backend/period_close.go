package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Period close moves forward only. Once closed through a date, the database refuses new journals on or before it.
// There is deliberately no reopen endpoint.

func (a *app) getPeriodClose(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	var through time.Time
	err := a.db.QueryRow(r.Context(), `SELECT closed_through FROM period_closes WHERE org_id=$1`, access.ID).Scan(&through)
	out := map[string]any{"closed_through": nil}
	if err == nil {
		out["closed_through"] = day(through)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if preview := r.URL.Query().Get("through"); preview != "" {
		if to, ok := parseDay(preview); ok {
			counts, perr := periodPreview(r.Context(), a.db, access.ID, to)
			if perr != nil {
				serverError(w, perr)
				return
			}
			out["preview"] = counts
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// periodPreview lists what closing through a date would lock, so the close screen can show it before
// the owner confirms. Counts only; nothing is changed.
func periodPreview(ctx context.Context, q rowQueryer, orgID int64, through time.Time) (map[string]any, error) {
	var journals, stock, drafts int64
	if err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM journals WHERE org_id=$1 AND entry_date<=$2),
		(SELECT count(*) FROM stock_movements WHERE org_id=$1 AND movement_date<=$2),
		(SELECT count(*) FROM invoices WHERE org_id=$1 AND status='draft' AND invoice_date<=$2)
		+ (SELECT count(*) FROM bills WHERE org_id=$1 AND status='draft' AND bill_date<=$2)
		+ (SELECT count(*) FROM expenses WHERE org_id=$1 AND expense_date<=$2)`, orgID, day(through)).Scan(&journals, &stock, &drafts); err != nil {
		return nil, err
	}
	return map[string]any{"journals": journals, "stock_movements": stock, "drafts_to_review": drafts}, nil
}

func (a *app) closePeriod(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID         int64  `json:"org_id"`
		ClosedThrough string `json:"closed_through"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	through, ok := parseDay(in.ClosedThrough)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var current *time.Time
	err = tx.QueryRow(ctx, `SELECT closed_through FROM period_closes WHERE org_id=$1 FOR UPDATE`, access.ID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if current != nil && through.Before(*current) {
		writeError(w, http.StatusUnprocessableEntity, "period_already_closed")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO period_closes (org_id, closed_through, closed_by) VALUES ($1,$2,$3)
		ON CONFLICT (org_id) DO UPDATE SET closed_through=EXCLUDED.closed_through, closed_by=EXCLUDED.closed_by, closed_at=now()`,
		access.ID, through, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := recordAudit(ctx, tx, access.ID, u.ID, "period_close", access.ID, "period.closed", map[string]any{"through": day(through)}); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"closed_through": day(through)})
}

// isPeriodClosed reports whether a database error came from the period-close trigger.
func isPeriodClosed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "period closed through")
}
