package main

import "net/http"

// salesOverview aggregates all documents rather than capped list endpoints.
func (a *app) salesOverview(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf, ok := parseDay(r.URL.Query().Get("as_of"))
	if !ok {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var draft, awaitingApproval, awaiting, overdue, outstanding, quotesDraft, quotesSent, quotesAccepted, quotesExpired int64
	err := a.db.QueryRow(r.Context(), `WITH balances AS (
		SELECT i.status,i.due_date,i.total_cents
		- COALESCE((SELECT sum(amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id AND p.payment_date <= $2),0)
		- COALESCE((SELECT sum(amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id AND n.credit_date <= $2),0)
		- COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications c WHERE c.org_id=i.org_id AND c.invoice_id=i.id AND c.applied_on <= $2),0) AS balance
		FROM invoices i WHERE i.org_id=$1 AND i.invoice_date <= $2
	) SELECT
		count(*) FILTER (WHERE status='draft'),
		count(*) FILTER (WHERE status='awaiting_approval'),
		count(*) FILTER (WHERE status='posted' AND balance>0),
		count(*) FILTER (WHERE status='posted' AND balance>0 AND due_date<$2),
		COALESCE(sum(balance) FILTER (WHERE status='posted' AND balance>0),0),
		(SELECT count(*) FROM quotes WHERE org_id=$1 AND quote_date<=$2 AND status='draft'),
		(SELECT count(*) FROM quotes WHERE org_id=$1 AND quote_date<=$2 AND status='sent' AND expiry_date>=$2),
		(SELECT count(*) FROM quotes WHERE org_id=$1 AND quote_date<=$2 AND status='accepted'),
		(SELECT count(*) FROM quotes WHERE org_id=$1 AND quote_date<=$2 AND status='sent' AND expiry_date<$2)
		FROM balances`, access.ID, asOf).Scan(&draft, &awaitingApproval, &awaiting, &overdue, &outstanding, &quotesDraft, &quotesSent, &quotesAccepted, &quotesExpired)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"draft_invoices": draft, "awaiting_approval": awaitingApproval, "awaiting_payment": awaiting, "overdue": overdue, "outstanding_cents": outstanding, "draft_quotes": quotesDraft, "sent_quotes": quotesSent, "accepted_quotes": quotesAccepted, "expired_quotes": quotesExpired})
}
