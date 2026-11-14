package main

import (
	"context"
	"encoding/json"
	"net/http"

	"tidal/backend/internal/money"
	"tidal/backend/internal/taxrules"
)

// Draft GST F5 built from the ledger (R24). Box 6 is the output-tax account movement and box 7 the
// claimable input-tax movement, so the return cannot drift from the books. Nothing is filed.

type gstF5Row struct {
	Kind    string
	Code    string
	Taxable int64
	Tax     int64
}

// gstF5Ledger moves money on the GST control account for one direction within the period. Output and
// input tax share account 2300, so the tagged direction keeps boxes 6 and 7 apart. Untagged lines fall
// back to the old account split (2310 input, everything else output), which keeps historical and manual
// journals reading as they did; the reclassification journal is tagged 'none' and excluded.
func gstF5Ledger(ctx context.Context, q rowQueryer, orgID int64, direction, from, to string) (debit, credit int64, err error) {
	err = q.QueryRow(ctx, `SELECT COALESCE(SUM(l.debit_cents),0), COALESCE(SUM(l.credit_cents),0)
		FROM journal_lines l JOIN journals j ON j.org_id=l.org_id AND j.id=l.journal_id
		JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
		WHERE l.org_id=$1 AND a.code IN ('2300','2310') AND j.entry_date BETWEEN $2::date AND $3::date
		  AND COALESCE(l.tax_direction, CASE a.code WHEN '2310' THEN 'input' ELSE 'output' END)=$4`, orgID, from, to, direction).Scan(&debit, &credit)
	return debit, credit, err
}

// draftF5: GET /api/v1/tax/gst-f5?org_id&from&to
func (a *app) draftF5(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	registered, err := gstRegisteredInPeriod(ctx, a.db, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	if !registered {
		writeError(w, http.StatusUnprocessableEntity, "gst_not_registered")
		return
	}
	rows, err := a.gstF5Rows(ctx, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	var b1, b2, b3, b5, b9 int64
	var lines []taxrules.GSTLine
	for _, row := range rows {
		code := row.Code
		if row.Kind == "sale" {
			switch code {
			case "SR", "DS":
				b1 += row.Taxable
				lines = append(lines, taxrules.GSTLine{Kind: "sale", Code: "SR", Net: money.Cents(row.Taxable), GST: money.Cents(row.Tax)})
			case "ZR":
				b2 += row.Taxable
				lines = append(lines, taxrules.GSTLine{Kind: "sale", Code: "ZR", Net: money.Cents(row.Taxable)})
			case "ES", "ES33", "ESN33":
				b3 += row.Taxable
				lines = append(lines, taxrules.GSTLine{Kind: "sale", Code: "ES33", Net: money.Cents(row.Taxable)})
			}
			continue
		}
		switch code {
		case "SR", "TX", "IM":
			b5 += row.Taxable
			tc := "TX"
			if code == "IM" {
				b9 += row.Taxable
				tc = "IM"
			}
			lines = append(lines, taxrules.GSTLine{Kind: "purchase", Code: tc, Net: money.Cents(row.Taxable), GST: money.Cents(row.Tax)})
		case "ZR", "ZP":
			b5 += row.Taxable
			lines = append(lines, taxrules.GSTLine{Kind: "purchase", Code: "ZP", Net: money.Cents(row.Taxable)})
		case "ME":
			b5 += row.Taxable
			b9 += row.Taxable
			lines = append(lines, taxrules.GSTLine{Kind: "purchase", Code: "ME", Net: money.Cents(row.Taxable)})
		}
	}
	start, _ := parseDay(from)
	end, _ := parseDay(to)
	res, err := taxrules.ComputeGSTF5(taxrules.GSTInput{PeriodStart: start, PeriodEnd: end, Lines: lines})
	if err != nil {
		serverError(w, err)
		return
	}
	outDebit, outCredit, err := gstF5Ledger(ctx, a.db, access.ID, "output", from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	inDebit, inCredit, err := gstF5Ledger(ctx, a.db, access.ID, "input", from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	outputTax := outCredit - outDebit
	inputTax := inDebit - inCredit
	boxes := make([]map[string]any, 0, len(res.Boxes))
	for _, b := range res.Boxes {
		value := b.Value
		switch b.Box {
		case "6":
			value = money.Cents(outputTax)
		case "7":
			value = money.Cents(inputTax)
		case "8":
			value = money.Cents(outputTax - inputTax)
		}
		boxes = append(boxes, map[string]any{"box": b.Box, "label": b.Label, "value_cents": value})
	}
	warnings := res.Warnings
	if got := boxValue(res.Boxes, "6"); got != money.Cents(outputTax) {
		warnings = append(warnings, "Box 6 was replaced with the output-tax account movement; check any manual GST journals.")
	}
	if got := boxValue(res.Boxes, "7"); got != money.Cents(inputTax) {
		warnings = append(warnings, "Box 7 was replaced with the input-tax account movement; check any manual GST journals.")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"draft": true, "filed": false,
		"period_start": from, "period_end": to, "due_date": day(res.DueDate),
		"boxes": boxes, "rate_bp": res.RateBP,
		"box_1_cents": b1, "box_2_cents": b2, "box_3_cents": b3, "box_5_cents": b5, "box_9_cents": b9,
		"output_tax_cents": outputTax, "input_tax_cents": inputTax, "net_payable_cents": outputTax - inputTax,
		"output_ledger_cents": outputTax, "input_ledger_cents": inputTax,
		"reconciles_output": boxValue(res.Boxes, "6") == money.Cents(outputTax),
		"reconciles_input":  boxValue(res.Boxes, "7") == money.Cents(inputTax),
		"warnings":          warnings,
	})
}

func boxValue(boxes []taxrules.GSTBox, box string) money.Cents {
	for _, b := range boxes {
		if b.Box == box {
			return b.Value
		}
	}
	return 0
}

// gstF5Rows reads the same base rows as the GST audit report, so boxes 1-5 cannot disagree with it.
func (a *app) gstF5Rows(ctx context.Context, orgID int64, from, to string) ([]gstF5Row, error) {
	rows, err := a.db.Query(ctx, `SELECT kind, gst_code, COALESCE(SUM(amount_cents),0), COALESCE(SUM(gst_cents),0)
		FROM (
			SELECT 'sale'::text AS kind, l.gst_code, l.amount_cents, l.gst_cents
			FROM invoice_lines l JOIN invoices i ON i.org_id=l.org_id AND i.id=l.invoice_id
			WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'sale'::text, COALESCE(l.gst_code,'NONE'), l.amount_cents, COALESCE(l.gst_cents,0)
			FROM sales_receipts s CROSS JOIN LATERAL jsonb_to_recordset(s.lines) AS l(amount_cents bigint,gst_code text,gst_cents bigint)
			WHERE s.org_id=$1 AND s.receipt_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'sale'::text, COALESCE(l.gst_code,'NONE'), -l.amount_cents, -COALESCE(l.gst_cents,0)
			FROM customer_credit_notes n CROSS JOIN LATERAL jsonb_to_recordset(n.lines) AS l(amount_cents bigint,gst_code text,gst_cents bigint)
			WHERE n.org_id=$1 AND n.status='open' AND n.credit_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'sale'::text, CASE WHEN i.gst_cents>0 THEN 'SR' ELSE 'ZR' END,
				-(c.amount_cents - CASE WHEN i.total_cents>0 THEN c.amount_cents*i.gst_cents/i.total_cents ELSE 0 END),
				-(CASE WHEN i.total_cents>0 THEN c.amount_cents*i.gst_cents/i.total_cents ELSE 0 END)
			FROM credit_notes c JOIN invoices i ON i.org_id=c.org_id AND i.id=c.invoice_id
			WHERE c.org_id=$1 AND c.credit_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'purchase'::text, e.gst_code, e.amount_cents-e.gst_cents, e.gst_cents
			FROM expenses e
			WHERE e.org_id=$1 AND e.expense_date BETWEEN $2::date AND $3::date AND e.gst_code <> 'NONE'
			UNION ALL
			SELECT 'purchase'::text, 'SR', -sc.net_cents, -sc.gst_cents
			FROM supplier_credits sc
			WHERE sc.org_id=$1 AND sc.credit_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'purchase'::text, l.gst_code, l.amount_cents, l.gst_cents
			FROM bill_lines l JOIN bills b ON b.org_id=l.org_id AND b.id=l.bill_id
			WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date BETWEEN $2::date AND $3::date
		) lines
		WHERE gst_code IS NOT NULL AND gst_code <> 'NONE'
		GROUP BY 1,2`, orgID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gstF5Row
	for rows.Next() {
		var row gstF5Row
		if err := rows.Scan(&row.Kind, &row.Code, &row.Taxable, &row.Tax); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type gstSnapshotInput struct {
	OrgID       int64  `json:"org_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

// saveGSTSnapshot: POST /api/v1/tax/gst-f5/snapshots — stores the current draft as a reviewable snapshot.
func (a *app) saveGSTSnapshot(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in gstSnapshotInput
	if !decodeJSON(w, r, &in) {
		return
	}
	start, ok1 := parseDay(in.PeriodStart)
	end, ok2 := parseDay(in.PeriodEnd)
	if !ok1 || !ok2 || end.Before(start) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	// Reuse the draft calculation so a snapshot can never hold different figures.
	status, body := a.gstDraftPayload(r.Context(), access.ID, in.PeriodStart, in.PeriodEnd)
	if status != http.StatusOK {
		writeJSON(w, status, body)
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO gst_f5_snapshots(org_id,period_start,period_end,payload,created_by)
		VALUES($1,$2::date,$3::date,$4,$5) RETURNING id`, access.ID, in.PeriodStart, in.PeriodEnd, body, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "draft", "payload": body})
}

// gstDraftPayload computes the draft and returns (status, payload) for reuse by the snapshot writer.
func (a *app) gstDraftPayload(ctx context.Context, orgID int64, from, to string) (int, map[string]any) {
	rows, err := a.gstF5Rows(ctx, orgID, from, to)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": "internal_error"}
	}
	start, _ := parseDay(from)
	end, _ := parseDay(to)
	var lines []taxrules.GSTLine
	for _, row := range rows {
		code := row.Code
		if row.Kind == "sale" {
			switch code {
			case "SR", "DS":
				lines = append(lines, taxrules.GSTLine{Kind: "sale", Code: "SR", Net: money.Cents(row.Taxable), GST: money.Cents(row.Tax)})
			case "ZR":
				lines = append(lines, taxrules.GSTLine{Kind: "sale", Code: "ZR", Net: money.Cents(row.Taxable)})
			case "ES", "ES33", "ESN33":
				lines = append(lines, taxrules.GSTLine{Kind: "sale", Code: "ES33", Net: money.Cents(row.Taxable)})
			}
			continue
		}
		switch code {
		case "SR", "TX", "IM":
			tc := "TX"
			if code == "IM" {
				tc = "IM"
			}
			lines = append(lines, taxrules.GSTLine{Kind: "purchase", Code: tc, Net: money.Cents(row.Taxable), GST: money.Cents(row.Tax)})
		case "ZR", "ZP":
			lines = append(lines, taxrules.GSTLine{Kind: "purchase", Code: "ZP", Net: money.Cents(row.Taxable)})
		case "ME":
			lines = append(lines, taxrules.GSTLine{Kind: "purchase", Code: "ME", Net: money.Cents(row.Taxable)})
		}
	}
	res, err := taxrules.ComputeGSTF5(taxrules.GSTInput{PeriodStart: start, PeriodEnd: end, Lines: lines})
	if err != nil {
		return http.StatusUnprocessableEntity, map[string]any{"error": "invalid_input"}
	}
	outDebit, outCredit, err := gstF5Ledger(ctx, a.db, orgID, "output", from, to)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": "internal_error"}
	}
	inDebit, inCredit, err := gstF5Ledger(ctx, a.db, orgID, "input", from, to)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": "internal_error"}
	}
	outputTax, inputTax := outCredit-outDebit, inDebit-inCredit
	boxes := []map[string]any{}
	for _, b := range res.Boxes {
		value := int64(b.Value)
		switch b.Box {
		case "6":
			value = outputTax
		case "7":
			value = inputTax
		case "8":
			value = outputTax - inputTax
		}
		boxes = append(boxes, map[string]any{"box": b.Box, "label": b.Label, "value_cents": value})
	}
	return http.StatusOK, map[string]any{
		"draft": true, "period_start": from, "period_end": to, "due_date": day(res.DueDate),
		"boxes": boxes, "output_tax_cents": outputTax, "input_tax_cents": inputTax, "net_payable_cents": outputTax - inputTax,
		"warnings": res.Warnings,
	}
}

// approveGSTSnapshot: POST /api/v1/tax/gst-f5/snapshots/{id}/approve
func (a *app) approveGSTSnapshot(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID int64 `json:"org_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE gst_f5_snapshots SET status='approved',approved_by=$3,approved_at=now()
		WHERE org_id=$1 AND id=$2 AND status='draft'`, access.ID, id, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "snapshot_not_draft")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "approved"})
}

// listGSTSnapshots: GET /api/v1/tax/gst-f5/snapshots?org_id
func (a *app) listGSTSnapshots(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,period_start::text,period_end::text,status,payload,created_at
		FROM gst_f5_snapshots WHERE org_id=$1 ORDER BY id DESC`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var start, end, status string
		var payload []byte
		var created any
		if err := rows.Scan(&id, &start, &end, &status, &payload, &created); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "period_start": start, "period_end": end, "status": status, "payload": json.RawMessage(payload)})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": out})
}
