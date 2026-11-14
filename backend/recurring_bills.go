package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/taxrules"
)

// Recurring bills: a profile holds one bill template; each run creates one DRAFT bill per due date.
// Nothing posts by itself and nothing runs by itself. A run stops with an error if the template no longer passes the bill checks.

type recurringBillInput struct {
	OrgID        int64     `json:"org_id"`
	Name         string    `json:"name"`
	RepeatEvery  int       `json:"repeat_every"`
	RepeatUnit   string    `json:"repeat_unit"`
	StartDate    string    `json:"start_date"`
	EndsOn       string    `json:"ends_on"`
	Bill         billInput `json:"bill"`
	Mode         string    `json:"mode"`
	AutoGenerate bool      `json:"auto_generate"`
}

// billFromProfile turns the stored template into a bill for one run date. The reference gets the run date so
// the supplier-reference uniqueness rule holds; the due date follows the template's payment terms.
func billFromProfile(data []byte, terms int, run time.Time) (billInput, error) {
	var in billInput
	if err := json.Unmarshal(data, &in); err != nil {
		return in, err
	}
	in.SupplierReference += " " + day(run)
	in.BillDate, in.DueDate, in.PaymentTermsDays = day(run), day(run.AddDate(0, 0, terms)), terms
	return in, nil
}

func (a *app) createRecurringBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in recurringBillInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Bill.SupplierReference = strings.TrimSpace(in.Bill.SupplierReference)
	start, ok := parseDay(in.StartDate)
	var ends *time.Time
	if ok && in.EndsOn != "" {
		e, good := parseDay(in.EndsOn)
		ok, ends = good && !e.Before(start), &e
	}
	terms := in.Bill.PaymentTermsDays
	if in.Mode == "" {
		in.Mode = "draft"
	}
	if !ok || in.Name == "" || !validTaxText(in.Name, 200) || len(in.Bill.SupplierReference) > 80 ||
		in.RepeatEvery < 1 || in.RepeatEvery > 12 || !validStage(in.RepeatUnit, "week", "month", "year") ||
		!validStage(in.Mode, "draft", "approve") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	check, err := billFromProfile(mustJSON(in.Bill), terms, start)
	if err != nil {
		serverError(w, err)
		return
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
	if _, _, _, code := validBill(&check, gstRegistered, taxrules.GSTRateBP(start)); code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if st, code, err := checkBillRefs(ctx, tx, access.ID, &check); err != nil {
		serverError(w, err)
		return
	} else if code != "" {
		writeError(w, st, code)
		return
	}
	in.Bill.OrgID = 0
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO recurring_bill_profiles (org_id,name,repeat_every,repeat_unit,start_date,ends_on,next_run,data,term_days,created_by,mode,auto_generate)
		VALUES ($1,$2,$3,$4,$5,$6,$5,$7,$8,$9,$10,$11) RETURNING id`, access.ID, in.Name, in.RepeatEvery, in.RepeatUnit, start, ends, mustJSON(in.Bill), terms, u.ID, in.Mode, in.AutoGenerate).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'recurring_bill.created')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "next_run": day(start), "mode": in.Mode})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs of strings and numbers cannot fail to marshal
	}
	return b
}

func (a *app) listRecurringBills(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	// Profiles that opted in catch up on due runs as the list is opened; the run-date constraint keeps
	// this from creating a bill twice.
	if err := a.autoRunRecurringBills(r.Context(), access.ID, u.ID, time.Now()); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT p.id,p.name,p.repeat_every,p.repeat_unit,p.start_date::text,COALESCE(p.ends_on::text,''),p.next_run::text,p.active,
		(data->>'contact_id')::bigint,(SELECT count(*) FROM bills b WHERE b.org_id=p.org_id AND b.recurring_profile_id=p.id),p.mode,p.auto_generate
		FROM recurring_bill_profiles p WHERE p.org_id=$1 ORDER BY p.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type profile struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		RepeatEvery  int    `json:"repeat_every"`
		RepeatUnit   string `json:"repeat_unit"`
		StartDate    string `json:"start_date"`
		EndsOn       string `json:"ends_on"`
		NextRun      string `json:"next_run"`
		Active       bool   `json:"active"`
		ContactID    int64  `json:"contact_id"`
		Generated    int64  `json:"generated_count"`
		Mode         string `json:"mode"`
		AutoGenerate bool   `json:"auto_generate"`
	}
	out := []profile{}
	for rows.Next() {
		var p profile
		if err := rows.Scan(&p.ID, &p.Name, &p.RepeatEvery, &p.RepeatUnit, &p.StartDate, &p.EndsOn, &p.NextRun, &p.Active, &p.ContactID, &p.Generated, &p.Mode, &p.AutoGenerate); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// stopRecurringBill turns a profile off or back on. Bills already generated stay.
func (a *app) stopRecurringBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID  int64 `json:"org_id"`
		ID     int64 `json:"id"`
		Active bool  `json:"active"`
	}
	if !decodeJSON(w, r, &in) || in.ID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE recurring_bill_profiles SET active=$3 WHERE org_id=$1 AND id=$2`, access.ID, in.ID, in.Active)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "active": in.Active})
}

// runRecurringBills creates a draft bill for every due date up to as_of (at most 60 per profile per call).
// The unique (profile, run date) constraint and the company lock make a repeated run create nothing twice.
func (a *app) runRecurringBills(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	created, skipped, code, err := runRecurringBillsTx(ctx, tx, access.ID, u.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created_bill_ids": created, "skipped_profile_ids": skipped})
}

// runRecurringBillsTx creates one bill for every due date up to as_of (at most 60 per profile per
// call). A profile set to "approve" also posts each generated bill; "draft" (the default) leaves it
// for review. Sending is deliberately absent. The unique (profile, run date) constraint and the
// company lock make a repeated run create nothing twice.
func runRecurringBillsTx(ctx context.Context, tx pgx.Tx, orgID, userID int64, asOf time.Time) (created, skipped []int64, code string, err error) {
	type due struct {
		id, runs, terms int64
		every           int
		unit, mode      string
		data            []byte
		start, next     time.Time
		ends            *time.Time
	}
	rows, err := tx.Query(ctx, `SELECT id,runs_done,term_days,repeat_every,repeat_unit,data,start_date,next_run,ends_on,mode
		FROM recurring_bill_profiles WHERE org_id=$1 AND active AND next_run<=$2 ORDER BY id FOR UPDATE`, orgID, asOf)
	if err != nil {
		return nil, nil, "", err
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.runs, &d.terms, &d.every, &d.unit, &d.data, &d.start, &d.next, &d.ends, &d.mode); err != nil {
			rows.Close()
			return nil, nil, "", err
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, "", err
	}
	created, skipped = []int64{}, []int64{}
	for _, d := range dues {
		for n := 0; n < 60 && !d.next.After(asOf) && (d.ends == nil || !d.next.After(*d.ends)); n++ {
			bill, err := billFromProfile(d.data, int(d.terms), d.next)
			if err != nil {
				return nil, nil, "", err
			}
			gstRegistered, err := gstRegisteredOn(ctx, tx, orgID, day(d.next))
			if err != nil {
				return nil, nil, "", err
			}
			catalogue, err := taxCatalogueOn(ctx, tx, "purchase", day(d.next))
			if err != nil {
				return nil, nil, "", err
			}
			lines, net, gst, selfGST, billCode := validBillRates(&bill, gstRegistered, taxrules.GSTRateBP(d.next), catalogue)
			total := net + gst
			if billCode != "" {
				return created, skipped, billCode, nil
			}
			if st, billCode, err := checkBillRefs(ctx, tx, orgID, &bill); err != nil {
				return nil, nil, "", err
			} else if billCode != "" {
				return nil, nil, "", fmt.Errorf("recurring bill profile %d: %s (%d)", d.id, billCode, st)
			}
			var id int64
			err = tx.QueryRow(ctx, `INSERT INTO bills(org_id,contact_id,supplier_reference,bill_date,due_date,notes,total_cents,gst_cents,self_gst_cents,created_by,order_number,subject,payment_terms_days,discount_account_id,recurring_profile_id,recurring_run_date)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT (org_id,recurring_profile_id,recurring_run_date) DO NOTHING RETURNING id`,
				orgID, bill.ContactID, bill.SupplierReference, bill.BillDate, bill.DueDate, bill.Notes, total, gst, selfGST, userID, bill.OrderNumber, bill.Subject, bill.PaymentTermsDays, bill.DiscountAccountID, d.id, d.next).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				// Already generated for this run date: only move the schedule on.
			} else if err != nil {
				if isUniqueViolation(err) {
					return created, skipped, "bill_reference_exists", nil
				}
				return nil, nil, "", err
			} else {
				for i, l := range lines {
					if _, err := tx.Exec(ctx, `INSERT INTO bill_lines(org_id,bill_id,line_no,account_id,description,quantity,unit_price_cents,amount_cents,discount_cents,discount_basis_points,customer_id,discount_account_id,gst_code,gst_rate_bp,gst_cents) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
						orgID, id, l.LineNo, bill.Lines[i].AccountID, l.Description, l.Quantity, l.UnitPriceCents, l.AmountCents, bill.Lines[i].DiscountCents, bill.Lines[i].DiscountBasisPoints, bill.Lines[i].CustomerID, bill.Lines[i].DiscountAccountID, l.GSTCode, l.GSTRateBP, l.GSTCents); err != nil {
						return nil, nil, "", err
					}
				}
				if d.mode == "approve" {
					// Automatic approval posts the same journal an explicit approval would.
					if _, postCode, err := postBillTx(ctx, tx, orgID, userID, id, nil, false); err != nil {
						return nil, nil, "", err
					} else if postCode != "" {
						return created, skipped, postCode, nil
					}
				}
				created = append(created, id)
			}
			next := nextRunDate(d.next, d.start, d.every, d.unit)
			if _, err := tx.Exec(ctx, `UPDATE recurring_bill_profiles SET next_run=$3,runs_done=runs_done+1 WHERE org_id=$1 AND id=$2`, orgID, d.id, next); err != nil {
				return nil, nil, "", err
			}
			d.next = next
		}
		// A schedule that has generated its last run must retire itself, or it keeps appearing as
		// active forever and the next manual run silently does nothing.
		if d.ends != nil && d.next.After(*d.ends) {
			if _, err := tx.Exec(ctx, `UPDATE recurring_bill_profiles SET active=false WHERE org_id=$1 AND id=$2`, orgID, d.id); err != nil {
				return nil, nil, "", err
			}
		}
	}
	return created, skipped, "", nil
}

// nextRunDate keeps the original day of the month through shorter months and leap years.
func nextRunDate(run, anchor time.Time, every int, unit string) time.Time {
	step := every
	if step < 1 {
		step = 1
	}
	months := (run.Year()-anchor.Year())*12 + int(run.Month()-anchor.Month())
	switch unit {
	case "week":
		return run.AddDate(0, 0, 7*step)
	case "year":
		months += 12 * step
	default:
		months += step
	}
	return nthRun(anchor, months, "month", 1)
}

// autoRunRecurringBills generates due bills for profiles that opted in, so a due date does not need
// a button press. It is safe to call on every list: the run-date constraint stops duplicates.
func (a *app) autoRunRecurringBills(ctx context.Context, orgID, userID int64, asOf time.Time) error {
	var due int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM recurring_bill_profiles WHERE org_id=$1 AND active AND auto_generate AND next_run<=$2`, orgID, asOf).Scan(&due); err != nil || due == 0 {
		return err
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, orgID); err != nil {
		return err
	}
	if _, _, _, err := runRecurringBillsTx(ctx, tx, orgID, userID, asOf); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
