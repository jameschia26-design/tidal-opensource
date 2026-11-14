package main

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Receivables/payables aging from posted documents; credits remain visible as negative balances.
type agingRow struct {
	Party       string           `json:"party"`
	DocType     string           `json:"document_type"`
	Document    string           `json:"document"`
	InvoiceDate string           `json:"invoice_date"`
	DueDate     string           `json:"due_date"`
	Balance     int64            `json:"balance_cents"`
	Bucket      string           `json:"bucket"`
	Buckets     map[string]int64 `json:"bucket_amounts_cents"`
}

type agingSummaryRow struct {
	Party   string           `json:"party"`
	DocType string           `json:"document_type,omitempty"`
	Balance int64            `json:"balance_cents"`
	Buckets map[string]int64 `json:"bucket_amounts_cents"`
}

func agingParam(r *http.Request, name string, fallback, min, max int) (int, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n >= min && n <= max
}

func agingBucket(days, length, periods int) string {
	if days <= 0 {
		return "current"
	}
	upper := ((days-1)/length + 1) * length
	if upper > length*periods {
		return fmt.Sprintf("%d+", length*periods+1)
	}
	return fmt.Sprintf("%d-%d", upper-length+1, upper)
}

var agingMonthLabels = []string{"current", "1 month", "2 months", "3+ months"}

// agingMonthBucket uses whole calendar months, not 30-day blocks. Anything past due but less than a
// month old sits in the first month band; three or more months sits in the last band.
func agingMonthBucket(past int, from, asOf time.Time) string {
	if past <= 0 {
		return "current"
	}
	months := (asOf.Year()-from.Year())*12 + int(asOf.Month()) - int(from.Month())
	if asOf.Day() < from.Day() {
		months--
	}
	if months < 1 {
		months = 1
	}
	if months > 3 {
		months = 3
	}
	if months == 3 {
		return "3+ months"
	}
	if months == 1 {
		return "1 month"
	}
	return "2 months"
}

func (a *app) agingReport(w http.ResponseWriter, r *http.Request, u currentUser, receivable bool) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf, ok := parseDay(r.URL.Query().Get("as_of"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	basis := r.URL.Query().Get("basis")
	if basis == "" {
		basis = "due"
	}
	if basis != "due" && basis != "invoice" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	periodBasis := r.URL.Query().Get("period_basis")
	if periodBasis == "" {
		periodBasis = "days"
	}
	if periodBasis != "days" && periodBasis != "months" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	periods, valid := agingParam(r, "periods", 4, 1, 12)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	length, valid := agingParam(r, "period_length", 30, 1, 365)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	contact := r.URL.Query().Get("contact")
	if len(contact) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var q string
	if receivable {
		q = `SELECT c.name, i.id, i.invoice_date, i.due_date, i.total_cents - COALESCE((SELECT sum(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id AND p.payment_date<=$2),0) - COALESCE((SELECT sum(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id AND n.credit_date<=$2),0) - COALESCE((SELECT sum(ca.amount_cents) FROM customer_credit_applications ca WHERE ca.org_id=i.org_id AND ca.invoice_id=i.id AND ca.applied_on<=$2),0)
		 FROM invoices i JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date<=$2 AND ($3='' OR c.name ILIKE '%'||$3||'%') AND (i.total_cents - COALESCE((SELECT sum(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id AND p.payment_date<=$2),0) - COALESCE((SELECT sum(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id AND n.credit_date<=$2),0) - COALESCE((SELECT sum(ca.amount_cents) FROM customer_credit_applications ca WHERE ca.org_id=i.org_id AND ca.invoice_id=i.id AND ca.applied_on<=$2),0)) <> 0 ORDER BY i.due_date, i.id`
	} else {
		q = `SELECT c.name, b.supplier_reference, b.bill_date, b.due_date, b.total_cents - COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$2
			AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)),0)
			- COALESCE((SELECT sum(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id AND sc.credit_date<=$2),0)
		 FROM bills b JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date<=$2
			AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=b.org_id AND r.doc_kind='bill' AND r.doc_id=b.id) AND ($3='' OR c.name ILIKE '%'||$3||'%') AND (b.total_cents - COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$2 AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)),0) - COALESCE((SELECT sum(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id AND sc.credit_date<=$2),0)) <> 0 ORDER BY b.due_date, b.id`
	}
	args := []any{access.ID, asOf, contact}
	useSnapshot := false
	if !receivable {
		var snapshotImportID int64
		if err := a.db.QueryRow(r.Context(), `SELECT import_id FROM ptb_payables_snapshots WHERE org_id=$1 AND as_of=$2 ORDER BY created_at DESC LIMIT 1`, access.ID, asOf).Scan(&snapshotImportID); err == nil {
			useSnapshot = true
			q = `SELECT sr.party_name,sr.reference,sr.bill_date,sr.due_date,sr.open_cents,sr.bucket_0_30_cents,sr.bucket_31_60_cents,sr.bucket_61_90_cents,sr.bucket_over_90_cents FROM ptb_payables_snapshots s JOIN ptb_payables_snapshot_rows sr ON sr.snapshot_id=s.id WHERE s.org_id=$1 AND s.import_id=$2 AND s.as_of=$3 AND ($4='' OR sr.party_name ILIKE '%'||$4||'%') ORDER BY sr.due_date,sr.reference`
			args = []any{access.ID, snapshotImportID, asOf, contact}
		} else if err != pgx.ErrNoRows {
			serverError(w, err)
			return
		}
	}
	rows, err := a.db.Query(r.Context(), q, args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []agingRow{}
	totals := map[string]int64{}
	snapshotLabels := []string{"0-30", "31-60", "61-90", "91+"}
	for rows.Next() {
		var party, doc string
		var invoiceDate, due time.Time
		var balance int64
		if useSnapshot {
			var b0, b1, b2, b3 int64
			if err := rows.Scan(&party, &doc, &invoiceDate, &due, &balance, &b0, &b1, &b2, &b3); err != nil {
				serverError(w, err)
				return
			}
			amounts := map[string]int64{"0-30": b0, "31-60": b1, "61-90": b2, "91+": b3}
			for label, cents := range amounts {
				totals[label] += cents
			}
			bucket := ""
			if balance > 0 {
				for _, label := range snapshotLabels {
					if amounts[label] > 0 {
						bucket = label
						break
					}
				}
			}
			out = append(out, agingRow{Party: party, DocType: "bill", Document: doc, InvoiceDate: day(invoiceDate), DueDate: day(due), Balance: balance, Bucket: bucket, Buckets: amounts})
			continue
		}
		if receivable {
			var id int64
			if err := rows.Scan(&party, &id, &invoiceDate, &due, &balance); err != nil {
				serverError(w, err)
				return
			}
			doc = "Invoice " + strconv.FormatInt(id, 10)
		} else {
			if err := rows.Scan(&party, &doc, &invoiceDate, &due, &balance); err != nil {
				serverError(w, err)
				return
			}
		}
		ageFrom := due
		if basis == "invoice" {
			ageFrom = invoiceDate
		}
		past := int(asOf.Sub(ageFrom).Hours() / 24)
		b := agingBucket(past, length, periods)
		if periodBasis == "months" {
			b = agingMonthBucket(past, ageFrom, asOf)
		}
		amounts := map[string]int64{b: balance}
		totals[b] += balance
		docType := "bill"
		if receivable {
			docType = "invoice"
		}
		out = append(out, agingRow{Party: party, DocType: docType, Document: doc, InvoiceDate: day(invoiceDate), DueDate: day(due), Balance: balance, Bucket: b, Buckets: amounts})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if receivable {
		// A credit note that is not yet applied to an invoice is still money the customer holds, so it
		// appears as a negative document row. Without it the ageing would overstate what is owed.
		credits, err := a.db.Query(r.Context(), `SELECT c.name, cn.credit_number, cn.credit_date,
			cn.total_cents
			- COALESCE((SELECT sum(x.amount_cents) FROM customer_credit_applications x WHERE x.org_id=cn.org_id AND x.credit_note_id=cn.id AND x.applied_on<=$2),0)
			- COALESCE((SELECT sum(r.amount_cents) FROM customer_credit_refunds r WHERE r.org_id=cn.org_id AND r.credit_note_id=cn.id AND r.refund_date<=$2),0)
			FROM customer_credit_notes cn JOIN contacts c ON c.org_id=cn.org_id AND c.id=cn.contact_id
			WHERE cn.org_id=$1 AND cn.status='open' AND cn.credit_date<=$2 AND ($3='' OR c.name ILIKE '%'||$3||'%')
			ORDER BY cn.credit_date, cn.id`, access.ID, asOf, contact)
		if err != nil {
			serverError(w, err)
			return
		}
		defer credits.Close()
		for credits.Next() {
			var party, number string
			var creditDate time.Time
			var balance int64
			if err := credits.Scan(&party, &number, &creditDate, &balance); err != nil {
				serverError(w, err)
				return
			}
			if balance <= 0 {
				continue
			}
			ageFrom := creditDate
			b := agingBucket(int(asOf.Sub(ageFrom).Hours()/24), length, periods)
			if periodBasis == "months" {
				b = agingMonthBucket(int(asOf.Sub(ageFrom).Hours()/24), ageFrom, asOf)
			}
			totals[b] -= balance
			out = append(out, agingRow{Party: party, DocType: "credit", Document: "Credit " + number, InvoiceDate: day(creditDate), DueDate: day(creditDate), Balance: -balance, Bucket: b, Buckets: map[string]int64{b: -balance}})
		}
		if err := credits.Err(); err != nil {
			serverError(w, err)
			return
		}
	}
	// Summary views reuse the per-document bucket amounts already computed. Contact totals are one row
	// per party; document-type rows split the same amounts by invoice, bill or credit.
	contactTotals := map[string]*agingSummaryRow{}
	typeTotals := map[string]*agingSummaryRow{}
	for _, row := range out {
		c := contactTotals[row.Party]
		if c == nil {
			c = &agingSummaryRow{Party: row.Party, Buckets: map[string]int64{}}
			contactTotals[row.Party] = c
		}
		c.Balance += row.Balance
		for label, cents := range row.Buckets {
			c.Buckets[label] += cents
		}
		key := row.Party + "\x00" + row.DocType
		t := typeTotals[key]
		if t == nil {
			t = &agingSummaryRow{Party: row.Party, DocType: row.DocType, Buckets: map[string]int64{}}
			typeTotals[key] = t
		}
		t.Balance += row.Balance
		for label, cents := range row.Buckets {
			t.Buckets[label] += cents
		}
	}
	summary := make([]agingSummaryRow, 0, len(contactTotals))
	for _, row := range contactTotals {
		summary = append(summary, *row)
	}
	sort.Slice(summary, func(i, j int) bool { return summary[i].Party < summary[j].Party })
	summaryByType := make([]agingSummaryRow, 0, len(typeTotals))
	for _, row := range typeTotals {
		summaryByType = append(summaryByType, *row)
	}
	sort.Slice(summaryByType, func(i, j int) bool {
		if summaryByType[i].Party != summaryByType[j].Party {
			return summaryByType[i].Party < summaryByType[j].Party
		}
		return summaryByType[i].DocType < summaryByType[j].DocType
	})
	// Percentage of the report total per bucket, so a reader can see the shape of the debt without
	// dividing by hand. A zero total yields zeros rather than a division error.
	percent := map[string]float64{}
	var grand int64
	for _, cents := range totals {
		grand += cents
	}
	for label, cents := range totals {
		if grand != 0 {
			percent[label] = math.Round(float64(cents)*10000/float64(grand)) / 100
		} else {
			percent[label] = 0
		}
	}
	result := map[string]any{"as_of": day(asOf), "basis": basis, "period_basis": periodBasis, "periods": periods, "period_length": length, "rows": out, "totals_by_bucket": totals, "percent_by_bucket": percent, "summary": summary, "summary_by_document_type": summaryByType}
	if periodBasis == "months" {
		result["bucket_labels"] = agingMonthLabels
	}
	if useSnapshot {
		result["bucket_labels"] = snapshotLabels
		result["source"] = "payables_snapshot"
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) receivablesAging(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.agingReport(w, r, u, true)
}

func (a *app) payablesAging(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.agingReport(w, r, u, false)
}
