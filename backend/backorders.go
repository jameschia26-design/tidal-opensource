package main

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// recordBackorders stores the part of an invoice's tracked-item quantity that stock could not cover.
// The invoice still posts in full: the customer has been promised the goods, and the promise is what a
// backorder records. Re-posting the same invoice cannot duplicate a row.
func recordBackorders(ctx context.Context, tx pgx.Tx, orgID, invoiceID int64, on string) error {
	rows, err := tx.Query(ctx, `SELECT il.item_id, SUM(il.quantity)::bigint FROM invoice_lines il
		JOIN items i ON i.org_id=il.org_id AND i.id=il.item_id
		WHERE il.org_id=$1 AND il.invoice_id=$2 AND i.track_inventory GROUP BY il.item_id`, orgID, invoiceID)
	if err != nil {
		return err
	}
	needed := map[int64]int64{}
	for rows.Next() {
		var id, qty int64
		if err := rows.Scan(&id, &qty); err != nil {
			rows.Close()
			return err
		}
		needed[id] = qty
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for item, qty := range needed {
		var onHand int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(qty_delta),0) FROM stock_movements WHERE org_id=$1 AND item_id=$2`, orgID, item).Scan(&onHand); err != nil {
			return err
		}
		if short := qty - onHand; short > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO backorders(org_id,item_id,invoice_id,contact_id,ordered_on,qty)
				SELECT $1,$2,$3,i.contact_id,$5,$4 FROM invoices i WHERE i.org_id=$1 AND i.id=$3
				ON CONFLICT (org_id,item_id,invoice_id) DO NOTHING`, orgID, item, invoiceID, short, on); err != nil {
				return err
			}
		}
	}
	return nil
}

// fulfilBackorders closes open promises when a bill brings an item in, oldest promise first. This does
// not change stock: the bill's own movement already did that.
func fulfilBackorders(ctx context.Context, tx pgx.Tx, orgID, itemID, billID int64, on string, received int64) error {
	if received <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id, qty-fulfilled_qty FROM backorders
		WHERE org_id=$1 AND item_id=$2 AND fulfilled_qty < qty ORDER BY ordered_on, id FOR UPDATE`, orgID, itemID)
	if err != nil {
		return err
	}
	type open struct{ id, left int64 }
	var list []open
	for rows.Next() {
		var v open
		if err := rows.Scan(&v.id, &v.left); err != nil {
			rows.Close()
			return err
		}
		list = append(list, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, v := range list {
		if received <= 0 {
			break
		}
		take := v.left
		if take > received {
			take = received
		}
		if _, err := tx.Exec(ctx, `INSERT INTO backorder_fulfilments(org_id,backorder_id,bill_id,fulfilled_on,qty) VALUES($1,$2,$3,$4,$5)`, orgID, v.id, billID, on, take); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE backorders SET fulfilled_qty = fulfilled_qty + $2 WHERE org_id=$1 AND id=$3`, orgID, take, v.id); err != nil {
			return err
		}
		received -= take
	}
	return nil
}

// listBackorders: GET /api/v1/backorders?org_id&status=open|all&from&to
func (a *app) listBackorders(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	status := q.Get("status")
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "all" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var from, to any
	if v := q.Get("from"); v != "" {
		t, valid := parseDay(v)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, valid := parseDay(v)
		if !valid || (from != nil && t.Before(from.(time.Time))) {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		to = t
	}
	rows, err := a.db.Query(r.Context(), `SELECT b.id,i.name,COALESCE(i.sku,''),c.name,b.ordered_on::text,b.qty,b.fulfilled_qty,
		COALESCE(inv.document_number,inv.number::text,''),COALESCE((SELECT sum(il.quantity) FROM invoice_lines il WHERE il.org_id=b.org_id AND il.invoice_id=b.invoice_id AND il.item_id=b.item_id),0)
		FROM backorders b JOIN items i ON i.org_id=b.org_id AND i.id=b.item_id
		JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id
		LEFT JOIN invoices inv ON inv.org_id=b.org_id AND inv.id=b.invoice_id
		WHERE b.org_id=$1 AND ($2='all' OR b.fulfilled_qty < b.qty)
		AND ($3::date IS NULL OR b.ordered_on >= $3::date) AND ($4::date IS NULL OR b.ordered_on <= $4::date)
		ORDER BY b.ordered_on, b.id LIMIT 500`, access.ID, status, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, qty, done, ordered int64
		var item, sku, contact, date, invoice string
		if err := rows.Scan(&id, &item, &sku, &contact, &date, &qty, &done, &invoice, &ordered); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "item": item, "sku": sku, "contact": contact, "ordered_on": date, "qty": qty, "fulfilled_qty": done, "open_qty": qty - done, "invoice_number": invoice, "total_ordered": ordered})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backorders": out})
}

// backorderReport: GET /api/v1/reports/backorders — the report form of the same list, for the reports
// index, with fulfilled promises included when asked.
func (a *app) backorderReport(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.listBackorders(w, r, u)
}

// fulfilBackorderNow: POST /api/v1/backorders/{id}/fulfil {org_id, qty, fulfilled_on} — a manual
// fulfilment for stock that arrived without a bill (for example an opening balance or an adjustment).
func (a *app) fulfilBackorderNow(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID       int64  `json:"org_id"`
		Qty         int64  `json:"qty"`
		FulfilledOn string `json:"fulfilled_on"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	on, valid := parseDay(in.FulfilledOn)
	if !valid || in.Qty <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var qty, done int64
	if err := tx.QueryRow(r.Context(), `SELECT qty, fulfilled_qty FROM backorders WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&qty, &done); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if in.Qty > qty-done {
		writeError(w, http.StatusUnprocessableEntity, "invalid_qty")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO backorder_fulfilments(org_id,backorder_id,fulfilled_on,qty) VALUES($1,$2,$3,$4)`, access.ID, id, day(on), in.Qty); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE backorders SET fulfilled_qty = fulfilled_qty + $2 WHERE org_id=$1 AND id=$3`, access.ID, in.Qty, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "open_qty": qty - done - in.Qty})
}

// fulfilBackordersForBill closes promises from the quantities a posted bill actually brings in.
func fulfilBackordersForBill(ctx context.Context, tx pgx.Tx, orgID, billID int64, on string) error {
	rows, err := tx.Query(ctx, `SELECT bl.item_id, SUM(bl.quantity)::bigint FROM bill_lines bl
		JOIN items i ON i.org_id=bl.org_id AND i.id=bl.item_id
		WHERE bl.org_id=$1 AND bl.bill_id=$2 AND i.track_inventory GROUP BY bl.item_id`, orgID, billID)
	if err != nil {
		return err
	}
	type line struct{ item, qty int64 }
	var lines []line
	for rows.Next() {
		var v line
		if err := rows.Scan(&v.item, &v.qty); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range lines {
		if err := fulfilBackorders(ctx, tx, orgID, l.item, billID, on, l.qty); err != nil {
			return err
		}
	}
	return nil
}

var _ = time.Now
