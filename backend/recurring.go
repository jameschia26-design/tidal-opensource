package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/taxrules"
)

// Recurring invoices: a template creates one invoice DRAFT per due run. Nothing is posted or sent automatically.

type recurringInput struct {
	OrgID      int64           `json:"org_id"`
	ContactID  int64           `json:"contact_id"`
	Name       string          `json:"name"`
	Frequency  string          `json:"frequency"`
	StartDate  string          `json:"start_date"`
	TermDays   int             `json:"term_days"`
	DueDay     *int            `json:"due_day"`
	EndDate    string          `json:"end_date"`
	Reference  string          `json:"reference"`
	Notes      string          `json:"notes"`
	Terms      string          `json:"terms"`
	AmountsAre string          `json:"amounts_are"`
	Lines      []invoiceLineIn `json:"lines"`
}

// templateLines turns stored template lines back into invoice input. Legacy lines carry no tax code, which reads as no GST.
func templateLines(stored []invoiceLineOut) []invoiceLineIn {
	in := make([]invoiceLineIn, len(stored))
	for i, l := range stored {
		in[i] = invoiceLineIn{Description: l.Description, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents, GSTCode: l.GSTCode, DiscountBP: l.DiscountBP}
	}
	return in
}

// recurringDueDate applies the optional day-of-month rule: the due date falls on due_day of the
// month after the invoice month, clamped to short months. Blank keeps the day-count term.
func recurringDueDate(invDate time.Time, termDays, dueDay int) time.Time {
	if dueDay < 1 || dueDay > 31 {
		return invDate.AddDate(0, 0, termDays)
	}
	first := time.Date(invDate.Year(), invDate.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if dueDay > last {
		dueDay = last
	}
	return time.Date(first.Year(), first.Month(), dueDay, 0, 0, 0, 0, time.UTC)
}

func (a *app) createRecurring(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in recurringInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	start, ok := parseDay(in.StartDate)
	if !ok || in.ContactID <= 0 || in.Name == "" || !validTaxText(in.Name, 200) || !validStage(in.Frequency, "monthly", "quarterly", "yearly") ||
		in.TermDays < 0 || in.TermDays > 365 || !validTaxText(in.Reference, 100) || !validTaxText(in.Notes, 4000) || !validTaxText(in.Terms, 4000) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	end, endOK := parseDay(in.EndDate)
	if in.EndDate != "" && (!endOK || !end.After(start)) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if in.AmountsAre != "" && in.AmountsAre != "exclusive" && in.AmountsAre != "inclusive" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	dueDay := 0
	if in.DueDay != nil {
		// A set day overrides the day-count term with Xero's "of the following month" rule.
		if *in.DueDay < 1 || *in.DueDay > 31 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		dueDay = *in.DueDay
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	gstRegistered, err := gstRegisteredOn(ctx, a.db, access.ID, day(start))
	if err != nil {
		serverError(w, err)
		return
	}
	// Validate with the invoice rules at the first run date. SR needs a GST-registered company.
	checked, _, _, code := buildSalesLines(in.Lines, gstRegistered, taxrules.GSTRateBP(start), in.AmountsAre == "inclusive")
	if code != "" {
		status := http.StatusBadRequest
		if code == "unsupported_gst" {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, code)
		return
	}
	// Recurring templates do not validate linked item, account or project references.
	for _, l := range in.Lines {
		if l.ItemID != 0 || l.RevenueAccountID != 0 || l.ProjectID != 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	// The template stores the typed (pre-GST-split) amounts so each run can recompute GST at its own date's rate.
	plain := make([]invoiceLineIn, len(in.Lines))
	copy(plain, in.Lines)
	for i := range plain {
		plain[i].GSTCode = ""
	}
	lines, typed, _ := buildInvoiceLines(plain)
	for i := range lines {
		lines[i].GSTCode = checked[i].GSTCode
	}
	amountsAre := amountsAreFlag(in.AmountsAre)
	var kind string
	err = a.db.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
		writeError(w, http.StatusBadRequest, "invalid_customer")
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
	if err := a.db.QueryRow(ctx, `INSERT INTO recurring_templates (org_id,contact_id,name,frequency,next_run,anchor_date,term_days,due_day,lines,total_cents,reference,notes,terms,created_by,end_date,amounts_are)
		VALUES ($1,$2,$3,$4,$5,$5,$6,NULLIF($7,0),$8,$9,$10,$11,$12,$13,CASE WHEN $14::text='' THEN NULL ELSE $14::date END,$15) RETURNING id`,
		access.ID, in.ContactID, in.Name, in.Frequency, start, in.TermDays, dueDay, data, typed, in.Reference, in.Notes, in.Terms, u.ID, in.EndDate, amountsAre).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "next_run": day(start), "total_cents": typed})
}

func (a *app) listRecurring(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT t.id, t.name, c.name, t.frequency, t.next_run::text, t.total_cents, t.active
		FROM recurring_templates t JOIN contacts c ON c.org_id=t.org_id AND c.id=t.contact_id WHERE t.org_id=$1 ORDER BY t.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type tmpl struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Customer   string `json:"customer_name"`
		Frequency  string `json:"frequency"`
		NextRun    string `json:"next_run"`
		TotalCents int64  `json:"total_cents"`
		Active     bool   `json:"active"`
	}
	out := []tmpl{}
	for rows.Next() {
		var t tmpl
		if err := rows.Scan(&t.ID, &t.Name, &t.Customer, &t.Frequency, &t.NextRun, &t.TotalCents, &t.Active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

// runRecurring creates one draft invoice for each active template whose next run is due on or before the given date.
// It advances next_run in the same transaction. The unique (template, run date) constraint makes repeats safe.
func (a *app) runRecurring(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		AsOf  string `json:"as_of"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	asOf, ok := parseDay(in.AsOf)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	rows, err := tx.Query(ctx, `SELECT id, contact_id, frequency, next_run, anchor_date, term_days, COALESCE(due_day,0), lines, reference, notes, terms, amounts_are FROM recurring_templates
		WHERE org_id=$1 AND active AND next_run <= $2 AND (end_date IS NULL OR next_run <= end_date) ORDER BY id FOR UPDATE`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	type due struct {
		id, contact                         int64
		freq, ref, notes, terms, amountsAre string
		run, anchor                         time.Time
		term, dueDay                        int
		lines                               []byte
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.contact, &d.freq, &d.run, &d.anchor, &d.term, &d.dueDay, &d.lines, &d.ref, &d.notes, &d.terms, &d.amountsAre); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	created := []int64{}
	skipped := []int64{}
	for _, d := range dues {
		var stored []invoiceLineOut
		if err := json.Unmarshal(d.lines, &stored); err != nil {
			serverError(w, err)
			return
		}
		gstRegistered, err := gstRegisteredOn(ctx, tx, access.ID, day(d.run))
		if err != nil {
			serverError(w, err)
			return
		}
		lines, net, gst, code := buildSalesLines(templateLines(stored), gstRegistered, taxrules.GSTRateBP(d.run), d.amountsAre == "inclusive")
		if code != "" {
			// One unusable template (e.g. SR after deregistration) must not block the others; it stays due.
			skipped = append(skipped, d.id)
			continue
		}
		total := net + gst
		invDate := d.run
		due := recurringDueDate(invDate, d.term, d.dueDay)
		var invID int64
		err = tx.QueryRow(ctx, `INSERT INTO invoices (org_id,contact_id,invoice_date,due_date,total_cents,created_by,reference,notes,terms,recurring_template_id,recurring_run_date,gst_cents,amounts_are)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (org_id, recurring_template_id, recurring_run_date) DO NOTHING RETURNING id`,
			access.ID, d.contact, invDate, due, total, u.ID, d.ref, d.notes, d.terms, d.id, d.run, gst, d.amountsAre).Scan(&invID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Already generated for this run: advance schedule without a second draft.
		} else if err != nil {
			serverError(w, err)
			return
		} else {
			for _, l := range lines {
				if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines (org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents,gst_code,gst_rate_bp,gst_cents,item_id,revenue_account_id,discount_basis_points,project_id)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11::bigint,0),NULLIF($12::bigint,0),$13,NULLIF($14::bigint,0))`,
					access.ID, invID, l.LineNo, l.Description, l.Quantity, l.UnitPriceCents, l.AmountCents, l.GSTCode, l.GSTRateBP, l.GSTCents, l.ItemID, l.RevenueAccountID, l.DiscountBP, l.ProjectID); err != nil {
					serverError(w, err)
					return
				}
			}
			created = append(created, invID)
		}
		// Keep the originally scheduled day through shorter months and leap years.
		months := (d.run.Year()-d.anchor.Year())*12 + int(d.run.Month()-d.anchor.Month())
		step := 1
		if d.freq == "quarterly" {
			step = 3
		} else if d.freq == "yearly" {
			step = 12
		}
		next := d.run
		for !next.After(asOf) {
			months += step
			next = nthRun(d.anchor, months, "month", 1)
		}
		if _, err := tx.Exec(ctx, `UPDATE recurring_templates SET next_run=$3 WHERE org_id=$1 AND id=$2`, access.ID, d.id, next); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'recurring.run',jsonb_build_object('created',$3::int))`, access.ID, u.ID, len(created)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created_invoice_ids": created, "skipped_template_ids": skipped})
}
