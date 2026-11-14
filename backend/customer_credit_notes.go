package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/taxrules"
)

// Standalone customer credit notes. Posting (draft -> open) books Dr revenue per line, Cr Accounts Receivable.
// Discount, shipping and adjustment post against the first line's revenue account (no separate account choice yet).

type customerCreditInput struct {
	OrgID       int64            `json:"org_id"`
	ContactID   int64            `json:"contact_id"`
	Number      string           `json:"credit_number"`
	Reference   string           `json:"reference"`
	CreditDate  string           `json:"credit_date"`
	Lines       []creditLineIn   `json:"lines"`
	Commercial  commercialFields `json:"commercial"`
	Notes       string           `json:"notes"`
	Terms       string           `json:"terms"`
	AmountsAre  string           `json:"amounts_are"`
	Currency    string           `json:"currency"`
	FXRateMicro int64            `json:"fx_rate_micro"`
}

// creditLine is a purchase-order line plus the GST fields stored with it. The embedded fields keep
// the stored JSON shape identical to a plain line, so older rows still decode.
// creditLineIn is a purchase-order line plus an unchecked project link. It embeds poLineIn
// so the stored JSON shape stays identical to a plain line, keeping older rows decodable.
type creditLineIn struct {
	poLineIn
	ProjectID int64 `json:"project_id"`
}

type creditLine struct {
	poLine
	GSTCode   string `json:"gst_code"`
	GSTRateBP int64  `json:"gst_rate_bp"`
	GSTCents  int64  `json:"gst_cents"`
	// ProjectID is stored unchecked, like recurring templates store no linked references
	// with validation: the project select is a convenience link, not a posting rule.
	ProjectID int64 `json:"project_id"`
}

type customerCreditOut struct {
	ID           int64           `json:"id"`
	ContactID    int64           `json:"contact_id"`
	CustomerName string          `json:"customer_name"`
	Number       string          `json:"credit_number"`
	Reference    string          `json:"reference"`
	CreditDate   string          `json:"credit_date"`
	Lines        json.RawMessage `json:"lines"`
	Commercial   json.RawMessage `json:"commercial"`
	TotalCents   int64           `json:"total_cents"`
	BalanceCents int64           `json:"balance_cents"`
	Notes        string          `json:"notes"`
	Terms        string          `json:"terms"`
	Status       string          `json:"status"`
	AmountsAre   string          `json:"amounts_are"`
	Currency     string          `json:"currency"`
	FXRateMicro  int64           `json:"fx_rate_micro"`
}

func (a *app) saveCustomerCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in customerCreditInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Number = strings.TrimSpace(in.Number)
	date, dok := parseDay(in.CreditDate)
	if in.ContactID <= 0 || !dok || !validTaxText(in.Number, 50) || !validTaxText(in.Reference, 100) || !validTaxText(in.Notes, 4000) || !validTaxText(in.Terms, 4000) {
		writeError(w, 400, "invalid_input")
		return
	}
	if in.AmountsAre == "" {
		in.AmountsAre = "exclusive"
	}
	if in.AmountsAre != "exclusive" && in.AmountsAre != "inclusive" {
		writeError(w, 400, "invalid_input")
		return
	}
	plain := make([]poLineIn, len(in.Lines))
	for i, l := range in.Lines {
		plain[i] = l.poLineIn
	}
	lines, subtotal, valid := buildPOLines(plain)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	total, valid := commercialTotal(subtotal, in.Commercial)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	currency, fxRate, okCurrency := resolveDocumentCurrency(ctx, a.db, access.ID, in.Currency, in.FXRateMicro, in.CreditDate)
	if !okCurrency {
		writeError(w, 400, "invalid_currency")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	gstRegistered, regErr := gstRegisteredOn(ctx, tx, access.ID, day(date))
	if err = regErr; err != nil {
		serverError(w, err)
		return
	}
	// GST is charged per line on the net price; transaction-level discounts and shipping are refused
	// rather than apportioned, matching the invoice rule.
	c := in.Commercial
	if c.DiscountCents != 0 || c.DiscountBasisPoints != 0 || c.ShippingCents != 0 || c.AdjustmentCents != 0 {
		for _, l := range in.Lines {
			if strings.EqualFold(strings.TrimSpace(l.GSTCode), "SR") {
				writeError(w, 422, "unsupported_gst")
				return
			}
		}
	}
	rateBP := taxrules.GSTRateBP(date)
	inclusive := in.AmountsAre == "inclusive"
	creditLines := make([]creditLine, len(lines))
	var gstTotal, netSubtotal int64
	for i, l := range lines {
		code := strings.ToUpper(strings.TrimSpace(in.Lines[i].GSTCode))
		cl := creditLine{poLine: l, ProjectID: in.Lines[i].ProjectID}
		switch code {
		case "", "NONE":
			cl.GSTCode = "NONE"
		case "SR":
			if !gstRegistered {
				writeError(w, 422, "unsupported_gst")
				return
			}
			cl.GSTCode = "SR"
			cl.GSTRateBP = rateBP
			if inclusive {
				// The typed rate already contains GST, so the net is backed out and the GST kept separately.
				// Lines store quantity x net unit price, so the unit price is reduced to the net it carries.
				gross := l.AmountCents
				net := (gross*10000 + (10000+rateBP)/2) / (10000 + rateBP)
				cl.UnitPriceCents = net / l.Quantity
				cl.AmountCents = cl.UnitPriceCents * l.Quantity
				cl.GSTCents = gross - cl.AmountCents
			} else {
				cl.GSTCents = (l.AmountCents*rateBP + 5000) / 10000
			}
			gstTotal += cl.GSTCents
		default:
			writeError(w, 422, "unsupported_gst")
			return
		}
		netSubtotal += cl.AmountCents
		creditLines[i] = cl
	}
	if inclusive {
		// Totals are struck on net amounts; the typed gross only decided how the GST split.
		if total, valid = commercialTotal(netSubtotal, in.Commercial); !valid {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	total += gstTotal
	if total > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	commercial, _ := json.Marshal(in.Commercial)
	data, _ := json.Marshal(creditLines)
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
		writeError(w, 422, "invalid_contact")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	for _, l := range lines {
		var typ string
		err = tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, l.AccountID).Scan(&typ)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "revenue") {
			writeError(w, 422, "invalid_revenue_account")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if l.ItemID > 0 {
			var iid int64
			if err = tx.QueryRow(ctx, `SELECT id FROM items WHERE org_id=$1 AND id=$2`, access.ID, l.ItemID).Scan(&iid); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeError(w, 422, "invalid_item")
					return
				}
				serverError(w, err)
				return
			}
		}
	}
	var id int64
	status, action := 201, "customer_credit_note.created"
	if r.Method == http.MethodPut {
		if id, ok = pathID(w, r); !ok {
			return
		}
		var state, number string
		err = tx.QueryRow(ctx, `SELECT status,credit_number FROM customer_credit_notes WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state, &number)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if state != "draft" {
			writeError(w, 409, "credit_note_not_draft")
			return
		}
		if in.Number == "" {
			in.Number = number
		}
		_, err = tx.Exec(ctx, `UPDATE customer_credit_notes SET contact_id=$3,credit_number=$4,reference=$5,credit_date=$6,lines=$7,commercial=$8,total_cents=$9,notes=$10,terms=$11,amounts_are=$12,currency=$13,fx_rate_micro=$14 WHERE org_id=$1 AND id=$2`,
			access.ID, id, in.ContactID, in.Number, in.Reference, date, data, commercial, total, in.Notes, in.Terms, in.AmountsAre, currency, fxRate)
		status, action = 200, "customer_credit_note.updated"
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO customer_credit_notes(org_id,contact_id,credit_number,reference,credit_date,lines,commercial,total_cents,notes,terms,created_by,amounts_are,currency,fx_rate_micro)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id,credit_number`, access.ID, in.ContactID, in.Number, in.Reference, date, data, commercial, total, in.Notes, in.Terms, u.ID, in.AmountsAre, currency, fxRate).Scan(&id, &in.Number)
	}
	if err != nil {
		if strings.Contains(err.Error(), "customer_credit_notes_org_id_credit_number_key") {
			writeError(w, 409, "duplicate_number")
			return
		}
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('customer_credit_note_id',$4::bigint))`, access.ID, u.ID, action, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"id": id, "credit_number": in.Number, "status": "draft", "total_cents": total, "currency": currency, "fx_rate_micro": fxRate})
}

func (a *app) listCustomerCredits(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT n.id,n.contact_id,c.name,n.credit_number,n.reference,n.credit_date::text,n.lines,n.commercial,n.total_cents,n.total_cents - COALESCE((SELECT SUM(a.amount_cents) FROM customer_credit_applications a WHERE a.org_id=n.org_id AND a.credit_note_id=n.id),0) - COALESCE((SELECT SUM(r.amount_cents) FROM customer_credit_refunds r WHERE r.org_id=n.org_id AND r.credit_note_id=n.id),0),n.notes,n.terms,n.status,n.amounts_are,n.currency,n.fx_rate_micro
		FROM customer_credit_notes n JOIN contacts c ON c.org_id=n.org_id AND c.id=n.contact_id WHERE n.org_id=$1 ORDER BY n.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []customerCreditOut{}
	for rows.Next() {
		var n customerCreditOut
		if err := rows.Scan(&n.ID, &n.ContactID, &n.CustomerName, &n.Number, &n.Reference, &n.CreditDate, &n.Lines, &n.Commercial, &n.TotalCents, &n.BalanceCents, &n.Notes, &n.Terms, &n.Status, &n.AmountsAre, &n.Currency, &n.FXRateMicro); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"credit_notes": out})
}

// setCustomerCreditStage: draft -> open (posts the journal) or draft -> void.
func (a *app) setCustomerCreditStage(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) || !validStage(in.Stage, "open", "void") {
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var state, number string
	var total int64
	var date time.Time
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT status,credit_number,total_cents,credit_date,lines FROM customer_credit_notes WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state, &number, &total, &date, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if state == in.Stage && state == "open" {
		var jid int64
		if err := tx.QueryRow(ctx, `SELECT journal_id FROM customer_credit_notes WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&jid); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "status": "open", "journal_id": jid})
		return
	}
	if state != "draft" {
		writeError(w, 409, "credit_note_not_draft")
		return
	}
	var jid int64
	if in.Stage == "void" {
		if _, err = tx.Exec(ctx, `UPDATE customer_credit_notes SET status='void' WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
			serverError(w, err)
			return
		}
	} else {
		var lines []creditLine
		if err = json.Unmarshal(raw, &lines); err != nil {
			serverError(w, err)
			return
		}
		ids, err := accountIDs(ctx, tx, access.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		key := "cn-post:" + strconv.FormatInt(id, 10)
		hash := sha256.Sum256([]byte(key))
		if err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, access.ID, date, "Credit note "+number, key, hash[:], u.ID).Scan(&jid); err != nil {
			if isPeriodClosed(err) {
				writeError(w, 422, "period_closed")
			} else {
				serverError(w, err)
			}
			return
		}
		var subtotal, gstTotal int64
		n := 0
		for _, l := range lines {
			subtotal += l.AmountCents
			gstTotal += l.GSTCents
			if l.AmountCents == 0 {
				continue
			}
			n++
			if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,tracking) VALUES($1,$2,$3,$4,$5,0,$6)`, access.ID, jid, n, l.AccountID, l.AmountCents, trackingJSON(l.Tracking)); err != nil {
				serverError(w, err)
				return
			}
		}
		// Discount lowers the credit (Cr revenue); shipping/adjustment raise it (Dr revenue). Booked on the first line's account.
		// GST is already booked on the output account, so it is excluded from the difference.
		if diff := total - subtotal - gstTotal; diff != 0 {
			n++
			debit, credit := diff, int64(0)
			if diff < 0 {
				debit, credit = 0, -diff
			}
			if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,$5,$6)`, access.ID, jid, n, lines[0].AccountID, debit, credit); err != nil {
				serverError(w, err)
				return
			}
		}
		// The credit note reverses part of the GST charged on the original sale.
		if gstTotal > 0 {
			n++
			if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,$5,0)`, access.ID, jid, n, ids["2300"], gstTotal); err != nil {
				serverError(w, err)
				return
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,0,$5)`, access.ID, jid, n+1, ids[acctAR], total); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE customer_credit_notes SET status='open',journal_id=$3,posted_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, jid); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('customer_credit_note_id',$4::bigint))`, access.ID, u.ID, "customer_credit_note."+in.Stage, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": map[string]string{"open": "open", "void": "void"}[in.Stage], "journal_id": jid})
}
