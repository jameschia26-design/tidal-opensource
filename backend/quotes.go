package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/taxrules"
)

type quoteInput struct {
	OrgID       int64            `json:"org_id"`
	QuoteNumber string           `json:"quote_number"`
	ContactID   int64            `json:"contact_id"`
	QuoteDate   string           `json:"quote_date"`
	ExpiryDate  string           `json:"expiry_date"`
	Reference   string           `json:"reference"`
	Notes       string           `json:"notes"`
	Terms       string           `json:"terms"`
	Lines       []invoiceLineIn  `json:"lines"`
	Commercial  commercialFields `json:"commercial"`
}

type quoteOut struct {
	ID               int64           `json:"id"`
	QuoteNumber      string          `json:"quote_number"`
	ContactID        int64           `json:"contact_id"`
	CustomerName     string          `json:"customer_name"`
	QuoteDate        string          `json:"quote_date"`
	ExpiryDate       string          `json:"expiry_date"`
	Reference        string          `json:"reference"`
	Notes            string          `json:"notes"`
	Terms            string          `json:"terms"`
	Lines            json.RawMessage `json:"lines"`
	TotalCents       int64           `json:"total_cents"`
	Status           string          `json:"status"`
	InvoiceID        *int64          `json:"invoice_id"`
	SellerSnapshot   json.RawMessage `json:"seller_snapshot"`
	CustomerSnapshot json.RawMessage `json:"customer_snapshot"`
	Commercial       json.RawMessage `json:"commercial"`
}

const quoteSelect = `SELECT q.id,q.quote_number,q.contact_id,COALESCE(q.customer_snapshot->>'name',c.name),q.quote_date::text,q.expiry_date::text,q.reference,q.notes,q.terms,q.lines,q.total_cents,q.status,q.invoice_id,q.seller_snapshot,q.customer_snapshot,q.commercial FROM quotes q JOIN contacts c ON c.org_id=q.org_id AND c.id=q.contact_id`

func scanQuote(s scanner) (quoteOut, error) {
	var q quoteOut
	err := s.Scan(&q.ID, &q.QuoteNumber, &q.ContactID, &q.CustomerName, &q.QuoteDate, &q.ExpiryDate, &q.Reference, &q.Notes, &q.Terms, &q.Lines, &q.TotalCents, &q.Status, &q.InvoiceID, &q.SellerSnapshot, &q.CustomerSnapshot, &q.Commercial)
	return q, err
}

func (a *app) listQuotes(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), quoteSelect+` WHERE q.org_id=$1 ORDER BY q.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []quoteOut{}
	for rows.Next() {
		q, err := scanQuote(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"quotes": out})
}

func (a *app) saveQuote(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in quoteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	qdate, qok := parseDay(in.QuoteDate)
	expiry, eok := parseDay(in.ExpiryDate)
	in.QuoteNumber = strings.TrimSpace(in.QuoteNumber)
	if in.ContactID <= 0 || !qok || !eok || expiry.Before(qdate) || !validTaxText(in.Reference, 100) || !validTaxText(in.Notes, 4000) || !validTaxText(in.Terms, 4000) || !validTaxText(in.QuoteNumber, 50) {
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
	// Share-lock the company row so GST registration cannot change mid-save.
	registered, err := gstRegisteredOn(ctx, tx, access.ID, day(qdate))
	if err != nil {
		serverError(w, err)
		return
	}
	lines, net, gst, code := buildSalesLines(in.Lines, registered, taxrules.GSTRateBP(qdate), false)
	if code == "unsupported_gst" {
		writeError(w, http.StatusUnprocessableEntity, code)
		return
	}
	if code != "" {
		writeError(w, 400, code)
		return
	}
	hasSR := false
	for _, l := range lines {
		hasSR = hasSR || l.GSTCode == "SR"
	}
	if hasSR && (in.Commercial.DiscountCents != 0 || in.Commercial.DiscountBasisPoints != 0 || in.Commercial.ShippingCents != 0 || in.Commercial.AdjustmentCents != 0) {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_gst")
		return
	}
	total, valid := commercialTotal(net, in.Commercial)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	total += gst
	if total > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	commercial, _ := json.Marshal(in.Commercial)
	for _, l := range lines {
		if l.ItemID <= 0 {
			continue
		}
		var iid int64
		if err := tx.QueryRow(ctx, `SELECT id FROM items WHERE org_id=$1 AND id=$2`, access.ID, l.ItemID).Scan(&iid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 422, "invalid_item")
				return
			}
			serverError(w, err)
			return
		}
	}
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2 FOR SHARE`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
		writeError(w, 400, "invalid_customer")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	data, err := json.Marshal(lines)
	if err != nil {
		serverError(w, err)
		return
	}
	var id int64
	status, action := 201, "quote.created"
	if r.Method == http.MethodPut {
		id, ok = pathID(w, r)
		if !ok {
			return
		}
		var state string
		err = tx.QueryRow(ctx, `SELECT status FROM quotes WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if state != "draft" {
			writeError(w, 409, "quote_not_draft")
			return
		}
		_, err = tx.Exec(ctx, `UPDATE quotes SET contact_id=$3,quote_date=$4,expiry_date=$5,reference=$6,notes=$7,terms=$8,lines=$9,total_cents=$10,commercial=$11,quote_number=COALESCE(NULLIF($12,''),quote_number),updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, in.ContactID, qdate, expiry, in.Reference, in.Notes, in.Terms, data, total, commercial, in.QuoteNumber)
		status, action = 200, "quote.updated"
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO quotes(org_id,contact_id,quote_date,expiry_date,reference,notes,terms,lines,total_cents,created_by,commercial,quote_number) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')) RETURNING id,quote_number`, access.ID, in.ContactID, qdate, expiry, in.Reference, in.Notes, in.Terms, data, total, u.ID, commercial, in.QuoteNumber).Scan(&id, &in.QuoteNumber)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('quote_id',$4::bigint))`, access.ID, u.ID, action, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"id": id, "status": "draft", "total_cents": total, "quote_number": in.QuoteNumber})
}

func (a *app) setQuoteStage(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validStage(in.Stage, "sent", "accepted", "rejected") {
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
	var state string
	err = tx.QueryRow(ctx, `SELECT status FROM quotes WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if state == in.Stage {
		writeJSON(w, 200, map[string]any{"id": id, "status": state})
		return
	}
	if !((state == "draft" && in.Stage == "sent") || (state == "sent" && validStage(in.Stage, "accepted", "rejected"))) {
		writeError(w, 422, "invalid_transition")
		return
	}
	if in.Stage == "sent" {
		_, err = tx.Exec(ctx, `UPDATE quotes q SET status='sent',updated_at=now(),
   seller_snapshot=(SELECT jsonb_build_object('name',o.name,'legal_name',o.legal_name,'uen',o.uen,'contact_email',o.contact_email,'phone',o.phone,'address',o.address) FROM organizations o WHERE o.id=q.org_id),
   customer_snapshot=(SELECT jsonb_build_object('name',c.name,'company_name',c.company_name,'uen',c.uen,'email',c.email,'phone',c.phone,'billing_address',c.billing_address,'shipping_address',c.shipping_address) FROM contacts c WHERE c.org_id=q.org_id AND c.id=q.contact_id)
   WHERE q.org_id=$1 AND q.id=$2`, access.ID, id)
	} else {
		_, err = tx.Exec(ctx, `UPDATE quotes SET status=$3,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, in.Stage)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'quote.stage_changed',jsonb_build_object('quote_id',$3::bigint,'status',$4::text))`, access.ID, u.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": in.Stage})
}

func (a *app) convertQuote(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID       int64  `json:"org_id"`
		InvoiceDate string `json:"invoice_date"`
		DueDate     string `json:"due_date"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	idate, iok := parseDay(in.InvoiceDate)
	ddate, dok := parseDay(in.DueDate)
	if !iok || !dok || ddate.Before(idate) {
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
	q, err := scanQuote(tx.QueryRow(ctx, quoteSelect+` WHERE q.org_id=$1 AND q.id=$2 FOR UPDATE OF q`, access.ID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if q.InvoiceID != nil {
		writeJSON(w, 200, map[string]any{"invoice_id": *q.InvoiceID})
		return
	}
	if q.Status != "accepted" {
		writeError(w, 409, "quote_not_accepted")
		return
	}
	qdate, _ := parseDay(q.QuoteDate)
	if idate.Before(qdate) {
		writeError(w, 400, "invalid_input")
		return
	}
	var kind string
	if err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2 FOR SHARE`, access.ID, q.ContactID).Scan(&kind); err != nil {
		serverError(w, err)
		return
	}
	if kind == "supplier" {
		writeError(w, 400, "invalid_customer")
		return
	}
	var lines []invoiceLineOut
	if err = json.Unmarshal(q.Lines, &lines); err != nil {
		serverError(w, err)
		return
	}
	var invoiceID int64
	var gstTotal int64
	hasSR := false
	for i := range lines {
		if lines[i].GSTCode == "" {
			lines[i].GSTCode = "NONE" // quotes saved before GST support carry no tax fields
		}
		hasSR = hasSR || lines[i].GSTCode == "SR"
		gstTotal += lines[i].GSTCents
	}
	invTotal := q.TotalCents
	if hasSR {
		// GST follows the invoice date's rate, not the quote date's. Quotes with SR never carry commercial adjustments.
		registered, regErr := gstRegisteredOn(ctx, tx, access.ID, day(idate))
		if err = regErr; err != nil {
			serverError(w, err)
			return
		}
		in := make([]invoiceLineIn, len(lines))
		for i, l := range lines {
			in[i] = invoiceLineIn{Description: l.Description, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents, GSTCode: l.GSTCode, ItemID: l.ItemID, DiscountBP: l.DiscountBP}
		}
		rebuilt, net, gst, code := buildSalesLines(in, registered, taxrules.GSTRateBP(idate), false)
		if code != "" {
			writeError(w, http.StatusUnprocessableEntity, code)
			return
		}
		lines, gstTotal, invTotal = rebuilt, gst, net+gst
	}
	err = tx.QueryRow(ctx, `INSERT INTO invoices(org_id,contact_id,invoice_date,due_date,total_cents,gst_cents,created_by,reference,notes,terms,commercial) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, access.ID, q.ContactID, idate, ddate, invTotal, gstTotal, u.ID, q.Reference, q.Notes, q.Terms, q.Commercial).Scan(&invoiceID)
	if err != nil {
		serverError(w, err)
		return
	}
	for _, l := range lines {
		if _, err = tx.Exec(ctx, `INSERT INTO invoice_lines(org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents,gst_code,gst_rate_bp,gst_cents,discount_basis_points,item_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12::bigint,0))`, access.ID, invoiceID, l.LineNo, l.Description, l.Quantity, l.UnitPriceCents, l.AmountCents, l.GSTCode, l.GSTRateBP, l.GSTCents, l.DiscountBP, l.ItemID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE quotes SET invoice_id=$3,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, invoiceID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'quote.converted',jsonb_build_object('quote_id',$3::bigint,'invoice_id',$4::bigint))`, access.ID, u.ID, id, invoiceID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"invoice_id": invoiceID})
}
