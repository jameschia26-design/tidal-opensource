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
	"strconv"
)

// Customer statement: opening balance, posted invoices, payments, credit notes and refunds for one customer.
// A statement can also be emailed to the customer through the same mailer that sends sign-in codes.

type custStatementRow struct {
	Date        string `json:"date"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Charge      int64  `json:"charge_cents"`
	Credit      int64  `json:"credit_cents"`
	Balance     int64  `json:"balance_cents"`
	id          int64
}

// statementTotals says whether one statement already exists in the books, so a resend never posts.
type statementBody struct {
	Contact   string             `json:"contact"`
	AccountNo string             `json:"account_number"`
	Address   json.RawMessage    `json:"address"`
	From      string             `json:"from"`
	To        string             `json:"to"`
	Brought   int64              `json:"brought_forward_cents"`
	Rows      []custStatementRow `json:"rows"`
	Balance   int64              `json:"balance_cents"`
	Email     string             `json:"email"`
}

func (a *app) customerStatement(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	orgID, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	contactID, err := strconv.ParseInt(q.Get("contact_id"), 10, 64)
	from, okFrom := parseDay(q.Get("from"))
	to, okTo := parseDay(q.Get("to"))
	if err != nil || contactID <= 0 || !okFrom || !okTo || to.Before(from) || to.Sub(from).Hours() > 24*366 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	st, err := a.buildStatement(r.Context(), access.ID, contactID, from, to)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contact": st.Contact, "account_number": st.AccountNo, "address": st.Address, "from": st.From, "to": st.To, "brought_forward_cents": st.Brought, "rows": st.Rows, "balance_cents": st.Balance})
}

// emailStatement sends the same statement as plain text to the customer's email address.
func (a *app) emailStatement(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID     int64  `json:"org_id"`
		ContactID int64  `json:"contact_id"`
		From      string `json:"from"`
		To        string `json:"to"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	contactID := in.ContactID
	from, okFrom := parseDay(in.From)
	to, okTo := parseDay(in.To)
	if contactID <= 0 || !okFrom || !okTo || to.Before(from) || to.Sub(from).Hours() > 24*366 {
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
	st, err := a.buildStatement(r.Context(), access.ID, contactID, from, to)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if st.Email == "" {
		writeError(w, http.StatusUnprocessableEntity, "contact_email_missing")
		return
	}
	if !a.mail.configured() {
		writeError(w, http.StatusServiceUnavailable, "email_unavailable")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s statement %s to %s\n", st.Contact, st.From, st.To)
	fmt.Fprintf(&b, "Brought forward: %s\n", moneyText(st.Brought))
	for _, row := range st.Rows {
		fmt.Fprintf(&b, "%s  %s  charge %s  credit %s  balance %s\n", row.Date, row.Description, moneyText(row.Charge), moneyText(row.Credit), moneyText(row.Balance))
	}
	fmt.Fprintf(&b, "Balance: %s\n", moneyText(st.Balance))
	if err := a.mail.send(st.Email, st.Contact+" statement", b.String()); err != nil {
		if errors.Is(err, errEmailUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "email_unavailable")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'statement.emailed',jsonb_build_object('contact_id',$3::bigint))`, access.ID, u.ID, contactID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent_to": st.Email})
}

// statementSummary lists every customer with the balance their statement would show, so the
// statements table can be read and sent without opening each customer one at a time.
func (a *app) statementSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	orgID, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, okFrom := parseDay(q.Get("from"))
	to, okTo := parseDay(q.Get("to"))
	if !okFrom || !okTo || to.Before(from) || to.Sub(from).Hours() > 24*366 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `
		SELECT c.id, c.name, COALESCE(c.email,''), COALESCE(c.account_number,''), c.billing_address,
			CASE WHEN c.opening_balance_date <= $2 THEN c.opening_balance_cents ELSE 0 END
			+ COALESCE((SELECT sum(total_cents) FROM invoices WHERE org_id=c.org_id AND contact_id=c.id AND status='posted' AND invoice_date<=$2),0)
			- COALESCE((SELECT sum(p.amount_cents) FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id WHERE p.org_id=c.org_id AND i.contact_id=c.id AND p.payment_date<=$2),0)
			- COALESCE((SELECT sum(amount_cents) FROM credit_notes WHERE org_id=c.org_id AND contact_id=c.id AND credit_date<=$2),0)
			- COALESCE((SELECT sum(total_cents) FROM customer_credit_notes WHERE org_id=c.org_id AND contact_id=c.id AND status='open' AND credit_date<=$2),0)
			+ COALESCE((SELECT sum(r.amount_cents) FROM customer_credit_refunds r JOIN customer_credit_notes n ON n.org_id=r.org_id AND n.id=r.credit_note_id WHERE r.org_id=c.org_id AND n.contact_id=c.id AND r.refund_date<=$2),0),
			COALESCE((SELECT SUM(GREATEST(x.total - COALESCE(x.paid,0) - COALESCE(x.credited,0), 0)) FROM (
				SELECT i.total_cents total,
					(SELECT SUM(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id) paid,
					(SELECT SUM(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id)
						+ (SELECT SUM(cc.amount_cents) FROM customer_credit_applications cc WHERE cc.org_id=i.org_id AND cc.invoice_id=i.id) credited
				FROM invoices i WHERE i.org_id=c.org_id AND i.contact_id=c.id AND i.status='posted' AND i.due_date < $2) x),0)
		FROM contacts c WHERE c.org_id=$1 AND c.kind IN ('customer','both') AND NOT c.archived ORDER BY c.name, c.id`, access.ID, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type statementSummaryRow struct {
		ContactID int64           `json:"contact_id"`
		Name      string          `json:"name"`
		Email     string          `json:"email"`
		AccountNo string          `json:"account_number"`
		Address   json.RawMessage `json:"address"`
		Balance   int64           `json:"balance_cents"`
		Overdue   int64           `json:"overdue_cents"`
	}
	out := []statementSummaryRow{}
	for rows.Next() {
		var s statementSummaryRow
		if err := rows.Scan(&s.ContactID, &s.Name, &s.Email, &s.AccountNo, &s.Address, &s.Balance, &s.Overdue); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": day(from), "to": day(to), "customers": out})
}

// moneyText renders cents for a plain-text email.
func moneyText(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

// buildStatement assembles one customer's statement, so the screen and the email cannot disagree.
func (a *app) buildStatement(ctx context.Context, orgID, contactID int64, from, to time.Time) (statementBody, error) {
	var name, email, accountNo string
	var address json.RawMessage
	var opening int64
	var openingDate string
	if err := a.db.QueryRow(ctx, `SELECT name, COALESCE(email,''), COALESCE(account_number,''), billing_address, opening_balance_cents, COALESCE(to_char(opening_balance_date,'YYYY-MM-DD'),'')
		FROM contacts WHERE org_id=$1 AND id=$2 AND kind IN ('customer','both')`, orgID, contactID).Scan(&name, &email, &accountNo, &address, &opening, &openingDate); err != nil {
		return statementBody{}, err
	}
	// Every movement up to "to"; rows before "from" only feed the brought-forward balance.
	rows, err := a.db.Query(ctx, `
		SELECT invoice_date::text,'Invoice','Invoice '||COALESCE(number::text,id::text),total_cents,0::bigint,id FROM invoices WHERE org_id=$1 AND contact_id=$2 AND status='posted' AND invoice_date<=$3
		UNION ALL SELECT p.payment_date::text,'Payment','Payment',0,p.amount_cents,p.id FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id WHERE p.org_id=$1 AND i.contact_id=$2 AND p.payment_date<=$3
		UNION ALL SELECT credit_date::text,'Credit note','Credit note: '||reason,0,amount_cents,id FROM credit_notes WHERE org_id=$1 AND contact_id=$2 AND credit_date<=$3
		UNION ALL SELECT credit_date::text,'Credit note','Credit note '||credit_number,0,total_cents,id FROM customer_credit_notes WHERE org_id=$1 AND contact_id=$2 AND status='open' AND credit_date<=$3
		UNION ALL SELECT r.refund_date::text,'Refund','Credit note refund',r.amount_cents,0,r.id FROM customer_credit_refunds r JOIN customer_credit_notes n ON n.org_id=r.org_id AND n.id=r.credit_note_id WHERE r.org_id=$1 AND n.contact_id=$2 AND r.refund_date<=$3
		ORDER BY 1,2,6`, orgID, contactID, to)
	if err != nil {
		return statementBody{}, err
	}
	defer rows.Close()
	var all []custStatementRow
	// Opening balance is a ledger entry, so it belongs in the brought-forward figure.
	if opening != 0 && openingDate != "" && openingDate <= day(to) {
		all = append(all, custStatementRow{Date: openingDate, Kind: "Opening balance", Description: "Opening balance", Charge: opening})
	}
	for rows.Next() {
		var s custStatementRow
		if err := rows.Scan(&s.Date, &s.Kind, &s.Description, &s.Charge, &s.Credit, &s.id); err != nil {
			return statementBody{}, err
		}
		all = append(all, s)
	}
	if err := rows.Err(); err != nil {
		return statementBody{}, err
	}
	var bal, brought int64
	out := []custStatementRow{}
	fromDay := day(from)
	for _, s := range all {
		bal += s.Charge - s.Credit
		if s.Date < fromDay {
			brought = bal
			continue
		}
		s.Balance = bal
		out = append(out, s)
	}
	return statementBody{Contact: name, AccountNo: accountNo, Address: address, From: fromDay, To: day(to), Brought: brought, Rows: out, Balance: bal, Email: email}, nil
}
