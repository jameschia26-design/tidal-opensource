package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Per-type prefixes and next numbers. Documents are numbered when a draft starts; posting never
// renumbers them, so the next number is a setting the company can change at any time.
type numberingSettings struct {
	InvoicePrefix       string `json:"invoice_prefix"`
	InvoiceNext         int64  `json:"invoice_next"`
	BillPrefix          string `json:"bill_prefix"`
	BillNext            int64  `json:"bill_next"`
	CreditNotePrefix    string `json:"credit_note_prefix"`
	CreditNoteNext      int64  `json:"credit_note_next"`
	PurchaseOrderPrefix string `json:"purchase_order_prefix"`
	PurchaseOrderNext   int64  `json:"purchase_order_next"`
	QuotePrefix         string `json:"quote_prefix"`
	QuoteNext           int64  `json:"quote_next"`
}

func (a *app) documentNumbering(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := a.db.Exec(ctx, `INSERT INTO document_numbering(org_id) VALUES($1) ON CONFLICT (org_id) DO NOTHING`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var s numberingSettings
	err := a.db.QueryRow(ctx, `SELECT invoice_prefix,invoice_next,bill_prefix,bill_next,credit_note_prefix,credit_note_next,
		purchase_order_prefix,purchase_order_next,quote_prefix,quote_next FROM document_numbering WHERE org_id=$1`, access.ID).
		Scan(&s.InvoicePrefix, &s.InvoiceNext, &s.BillPrefix, &s.BillNext, &s.CreditNotePrefix, &s.CreditNoteNext,
			&s.PurchaseOrderPrefix, &s.PurchaseOrderNext, &s.QuotePrefix, &s.QuoteNext)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *app) saveDocumentNumbering(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in numberingSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	prefixes := []*string{&in.InvoicePrefix, &in.BillPrefix, &in.CreditNotePrefix, &in.PurchaseOrderPrefix, &in.QuotePrefix}
	for _, p := range prefixes {
		*p = strings.TrimSpace(*p)
		if !validTaxText(*p, 10) {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	numbers := []int64{in.InvoiceNext, in.BillNext, in.CreditNoteNext, in.PurchaseOrderNext, in.QuoteNext}
	for _, n := range numbers {
		if n < 1 || n > 1000000000 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	ctx := r.Context()
	_, err := a.db.Exec(ctx, `INSERT INTO document_numbering(org_id,invoice_prefix,invoice_next,bill_prefix,bill_next,credit_note_prefix,credit_note_next,purchase_order_prefix,purchase_order_next,quote_prefix,quote_next,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
		ON CONFLICT (org_id) DO UPDATE SET invoice_prefix=EXCLUDED.invoice_prefix,invoice_next=EXCLUDED.invoice_next,
		bill_prefix=EXCLUDED.bill_prefix,bill_next=EXCLUDED.bill_next,credit_note_prefix=EXCLUDED.credit_note_prefix,
		credit_note_next=EXCLUDED.credit_note_next,purchase_order_prefix=EXCLUDED.purchase_order_prefix,
		purchase_order_next=EXCLUDED.purchase_order_next,quote_prefix=EXCLUDED.quote_prefix,quote_next=EXCLUDED.quote_next,updated_at=now()`,
		access.ID, in.InvoicePrefix, in.InvoiceNext, in.BillPrefix, in.BillNext, in.CreditNotePrefix, in.CreditNoteNext,
		in.PurchaseOrderPrefix, in.PurchaseOrderNext, in.QuotePrefix, in.QuoteNext)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}
