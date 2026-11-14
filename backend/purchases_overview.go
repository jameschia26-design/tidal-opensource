package main

import "net/http"

// purchasesOverview aggregates all bills and purchase orders without list limits.
func (a *app) purchasesOverview(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	var draft, awaitingApproval, awaiting, overdue, outstanding, ordersDraft, ordersIssued, ordersReceived int64
	err := a.db.QueryRow(r.Context(), `WITH balances AS (
 SELECT b.status,b.due_date,b.total_cents-COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$2),0) AS balance
 FROM bills b WHERE b.org_id=$1 AND b.bill_date<=$2)
 SELECT count(*) FILTER (WHERE status='draft'), count(*) FILTER (WHERE status='awaiting_approval'), count(*) FILTER (WHERE status='posted' AND balance>0),
 count(*) FILTER (WHERE status='posted' AND balance>0 AND due_date<$2), COALESCE(sum(balance) FILTER (WHERE status='posted' AND balance>0),0),
 (SELECT count(*) FROM purchase_orders WHERE org_id=$1 AND po_date<=$2 AND status='draft'),
 (SELECT count(*) FROM purchase_orders WHERE org_id=$1 AND po_date<=$2 AND status='issued'),
 (SELECT count(*) FROM purchase_orders WHERE org_id=$1 AND po_date<=$2 AND status='received') FROM balances`, access.ID, asOf).Scan(&draft, &awaitingApproval, &awaiting, &overdue, &outstanding, &ordersDraft, &ordersIssued, &ordersReceived)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"draft_bills": draft, "awaiting_approval": awaitingApproval, "awaiting_payment": awaiting, "overdue": overdue, "outstanding_cents": outstanding, "draft_orders": ordersDraft, "issued_orders": ordersIssued, "received_orders": ordersReceived})
}
