package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/taxrules"
)

// Purchase orders: draft -> issued -> received (when every line is fully received) or cancelled.
// "Save and send" in the map is "issue" here: Tidal sends no email. Nothing posts to the ledger.

type poLineIn struct {
	Description    string `json:"description"`
	AccountID      int64  `json:"account_id"`
	Quantity       int64  `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	ItemID         int64  `json:"item_id"`
	// GSTCode is read by purchase orders, sales receipts and customer credit notes.
	GSTCode string `json:"gst_code"`
	// Tracking is one option per tracking category for this line: {"<category_id>": <option_id>}.
	Tracking map[string]int64 `json:"tracking"`
}
type poLine struct {
	LineNo         int    `json:"line_no"`
	Description    string `json:"description"`
	AccountID      int64  `json:"account_id"`
	Quantity       int64  `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	AmountCents    int64  `json:"amount_cents"`
	ItemID         int64  `json:"item_id"`
	// Tracking is one option per tracking category for this line: {"<category_id>": <option_id>}.
	// A purchase order does not post, so the values are carried onto the bill that is raised from it.
	Tracking map[string]int64 `json:"tracking,omitempty"`
}
type poInput struct {
	OrgID              int64            `json:"org_id"`
	ContactID          int64            `json:"contact_id"`
	PONumber           string           `json:"po_number"`
	Reference          string           `json:"reference"`
	PODate             string           `json:"po_date"`
	DeliveryDate       string           `json:"delivery_date"`
	DeliveryAddress    string           `json:"delivery_address"`
	ShipmentPreference string           `json:"shipment_preference"`
	Lines              []poLineIn       `json:"lines"`
	Commercial         commercialFields `json:"commercial"`
	Notes              string           `json:"notes"`
	Terms              string           `json:"terms"`
}

func buildPOLines(in []poLineIn) ([]poLine, int64, bool) {
	if len(in) == 0 || len(in) > 200 {
		return nil, 0, false
	}
	out := make([]poLine, 0, len(in))
	var total int64
	for i, l := range in {
		desc := strings.TrimSpace(l.Description)
		if desc == "" || !validTaxText(desc, 200) || l.AccountID <= 0 || l.Quantity < 1 || l.Quantity > maxQty || l.UnitPriceCents < 0 || l.UnitPriceCents > maxMoneyCents/l.Quantity {
			return nil, 0, false
		}
		amount := l.Quantity * l.UnitPriceCents
		if total += amount; total > maxMoneyCents {
			return nil, 0, false
		}
		out = append(out, poLine{LineNo: i + 1, Description: desc, AccountID: l.AccountID, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, AmountCents: amount, ItemID: l.ItemID, Tracking: l.Tracking})
	}
	return out, total, total > 0
}

type poOut struct {
	ID                 int64           `json:"id"`
	ContactID          int64           `json:"contact_id"`
	VendorName         string          `json:"vendor_name"`
	PONumber           string          `json:"po_number"`
	Reference          string          `json:"reference"`
	PODate             string          `json:"po_date"`
	DeliveryDate       string          `json:"delivery_date"`
	DeliveryAddress    string          `json:"delivery_address"`
	ShipmentPreference string          `json:"shipment_preference"`
	Lines              json.RawMessage `json:"lines"`
	Commercial         json.RawMessage `json:"commercial"`
	TotalCents         int64           `json:"total_cents"`
	Notes              string          `json:"notes"`
	Terms              string          `json:"terms"`
	Status             string          `json:"status"`
	LifecycleStatus    string          `json:"lifecycle_status"`
	SentAt             string          `json:"sent_at"`
	BilledCents        int64           `json:"billed_cents"`
	BilledPercent      int64           `json:"billed_percent"`
	Files              int             `json:"files"`
	Received           map[int]int64   `json:"received"`
	InTransit          map[int]int64   `json:"in_transit"`
}

const poSelect = `SELECT p.id,p.contact_id,c.name,p.po_number,p.reference,p.po_date::text,p.delivery_date::text,p.delivery_address,p.shipment_preference,p.lines,p.commercial,p.total_cents,p.notes,p.terms,p.status,CASE p.status WHEN 'issued' THEN 'approved' ELSE p.status END,COALESCE(to_char(p.sent_at,'YYYY-MM-DD'),''),p.billed_cents,CASE WHEN p.total_cents>0 THEN p.billed_cents*100/p.total_cents ELSE 0 END,(SELECT count(*) FROM attachments a WHERE a.org_id=p.org_id AND a.entity_kind='purchase-orders' AND a.entity_id=p.id) FROM purchase_orders p JOIN contacts c ON c.org_id=p.org_id AND c.id=p.contact_id`

// receiptTotals sums quantities per PO line from receives, split by status. excludeID skips one receive (the one being promoted).
func receiptTotals(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, orgID, poID, excludeID int64) (received, transit map[int]int64, err error) {
	received, transit = map[int]int64{}, map[int]int64{}
	rows, err := q.Query(ctx, `SELECT r.status,(i->>'line_no')::int,sum((i->>'quantity')::bigint) FROM purchase_receives r, jsonb_array_elements(r.items) i
		WHERE r.org_id=$1 AND r.purchase_order_id=$2 AND r.id<>$3 AND r.status IN ('draft','received') GROUP BY 1,2`, orgID, poID, excludeID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var line int
		var qty int64
		if err = rows.Scan(&status, &line, &qty); err != nil {
			return
		}
		if status == "received" {
			received[line] = qty
		} else {
			transit[line] = qty
		}
	}
	err = rows.Err()
	return
}

func (a *app) listPurchaseOrders(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validStage(status, "draft", "awaiting_approval", "approved", "issued", "received", "billed", "cancelled") {
		writeError(w, 400, "invalid_input")
		return
	}
	rows, err := a.db.Query(r.Context(), poSelect+` WHERE p.org_id=$1 AND ($2='' OR (CASE p.status WHEN 'issued' THEN 'approved' ELSE p.status END)=$2) ORDER BY p.id DESC LIMIT 200`, access.ID, status)
	if err != nil {
		serverError(w, err)
		return
	}
	out := []poOut{}
	for rows.Next() {
		var p poOut
		if err := rows.Scan(&p.ID, &p.ContactID, &p.VendorName, &p.PONumber, &p.Reference, &p.PODate, &p.DeliveryDate, &p.DeliveryAddress, &p.ShipmentPreference, &p.Lines, &p.Commercial, &p.TotalCents, &p.Notes, &p.Terms, &p.Status, &p.LifecycleStatus, &p.SentAt, &p.BilledCents, &p.BilledPercent, &p.Files); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	for i := range out {
		if out[i].Received, out[i].InTransit, err = receiptTotals(r.Context(), a.db, access.ID, out[i].ID, 0); err != nil {
			serverError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"purchase_orders": out})
}

func (a *app) savePurchaseOrder(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in poInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.PONumber = strings.TrimSpace(in.PONumber)
	pdate, pok := parseDay(in.PODate)
	ddate, dok := parseDay(in.DeliveryDate)
	if in.ContactID <= 0 || !pok || !dok || !ddate.After(pdate) || !validTaxText(in.PONumber, 50) || !validTaxText(in.Reference, 100) || !validTaxText(in.DeliveryAddress, 1000) ||
		!validTaxText(in.ShipmentPreference, 200) || !validTaxText(in.Notes, 4000) || !validTaxText(in.Terms, 4000) {
		writeError(w, 400, "invalid_input")
		return
	}
	lines, subtotal, valid := buildPOLines(in.Lines)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	total, valid := commercialTotal(subtotal, in.Commercial)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	commercial, _ := json.Marshal(in.Commercial)
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	registered, regErr := gstRegisteredOn(ctx, tx, access.ID, day(pdate))
	if err = regErr; err != nil {
		serverError(w, err)
		return
	}
	// GST is per line on the net price; discounts and shipping are refused with it rather than apportioned, like invoices.
	c := in.Commercial
	taxed := make([]creditLine, len(lines))
	var gstTotal int64
	for i, l := range lines {
		cl := creditLine{poLine: l, GSTCode: "NONE"}
		switch strings.ToUpper(strings.TrimSpace(in.Lines[i].GSTCode)) {
		case "", "NONE":
		case "SR":
			if !registered || c.DiscountCents != 0 || c.DiscountBasisPoints != 0 || c.ShippingCents != 0 || c.AdjustmentCents != 0 {
				writeError(w, 422, "unsupported_gst")
				return
			}
			cl.GSTCode, cl.GSTRateBP = "SR", taxrules.GSTRateBP(pdate)
			cl.GSTCents = (l.AmountCents*cl.GSTRateBP + 5000) / 10000
			gstTotal += cl.GSTCents
		default:
			writeError(w, 422, "unsupported_gst")
			return
		}
		taxed[i] = cl
	}
	if total += gstTotal; total > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	data, _ := json.Marshal(taxed)
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "customer") {
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
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "expense" && typ != "asset") {
			writeError(w, 422, "invalid_expense_account")
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
	status, action := 201, "purchase_order.created"
	if r.Method == http.MethodPut {
		if id, ok = pathID(w, r); !ok {
			return
		}
		var state, number string
		err = tx.QueryRow(ctx, `SELECT status,po_number FROM purchase_orders WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state, &number)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if state != "draft" {
			writeError(w, 409, "purchase_order_not_draft")
			return
		}
		if in.PONumber == "" {
			in.PONumber = number
		}
		_, err = tx.Exec(ctx, `UPDATE purchase_orders SET contact_id=$3,po_number=$4,reference=$5,po_date=$6,delivery_date=$7,delivery_address=$8,shipment_preference=$9,lines=$10,commercial=$11,total_cents=$12,notes=$13,terms=$14,updated_at=now() WHERE org_id=$1 AND id=$2`,
			access.ID, id, in.ContactID, in.PONumber, in.Reference, pdate, ddate, in.DeliveryAddress, in.ShipmentPreference, data, commercial, total, in.Notes, in.Terms)
		status, action = 200, "purchase_order.updated"
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO purchase_orders(org_id,contact_id,po_number,reference,po_date,delivery_date,delivery_address,shipment_preference,lines,commercial,total_cents,notes,terms,created_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id,po_number`, access.ID, in.ContactID, in.PONumber, in.Reference, pdate, ddate, in.DeliveryAddress, in.ShipmentPreference, data, commercial, total, in.Notes, in.Terms, u.ID).Scan(&id, &in.PONumber)
	}
	if err != nil {
		if strings.Contains(err.Error(), "purchase_orders_org_id_po_number_key") {
			writeError(w, 409, "duplicate_number")
			return
		}
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('purchase_order_id',$4::bigint))`, access.ID, u.ID, action, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"id": id, "po_number": in.PONumber, "status": "draft", "total_cents": total})
}

func (a *app) setPurchaseOrderStage(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) || !validStage(in.Stage, "awaiting_approval", "issued", "cancelled") {
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
	err = tx.QueryRow(ctx, `SELECT status FROM purchase_orders WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state)
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
	// Submitting asks for approval; approving issues the order. Cancelling an issued or submitted PO
	// is refused once goods were received or are in transit.
	if !((state == "draft" && in.Stage == "awaiting_approval") ||
		((state == "draft" || state == "awaiting_approval") && in.Stage == "issued") ||
		(validStage(state, "draft", "awaiting_approval", "issued") && in.Stage == "cancelled")) {
		writeError(w, 422, "invalid_transition")
		return
	}
	if in.Stage == "cancelled" {
		var used int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM purchase_receives WHERE org_id=$1 AND purchase_order_id=$2 AND status IN ('draft','received')`, access.ID, id).Scan(&used); err != nil {
			serverError(w, err)
			return
		}
		if used > 0 {
			writeError(w, 422, "purchase_order_has_receives")
			return
		}
	}
	// Approval records when the order was sent; sending an email stays out of scope.
	if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET status=$3,updated_at=now(),sent_at=CASE WHEN $3='issued' THEN COALESCE(sent_at,now()) ELSE sent_at END WHERE org_id=$1 AND id=$2`, access.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'purchase_order.stage_changed',jsonb_build_object('purchase_order_id',$3::bigint,'status',$4::text))`, access.ID, u.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": in.Stage})
}

// ---- purchase receives ----

type receiveItem struct {
	LineNo   int   `json:"line_no"`
	Quantity int64 `json:"quantity"`
}
type receiveInput struct {
	OrgID           int64         `json:"org_id"`
	PurchaseOrderID int64         `json:"purchase_order_id"`
	ReceiveNumber   string        `json:"receive_number"`
	ReceivedDate    string        `json:"received_date"`
	TrackingNumber  string        `json:"tracking_number"`
	TrackingURL     string        `json:"tracking_url"`
	Items           []receiveItem `json:"items"`
	Notes           string        `json:"notes"`
	Stage           string        `json:"status"`
}

func (a *app) listPurchaseReceives(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT r.id,r.purchase_order_id,p.po_number,c.name,r.receive_number,r.received_date::text,r.tracking_number,r.tracking_url,r.items,r.notes,r.status
		FROM purchase_receives r JOIN purchase_orders p ON p.org_id=r.org_id AND p.id=r.purchase_order_id JOIN contacts c ON c.org_id=p.org_id AND c.id=p.contact_id
		WHERE r.org_id=$1 ORDER BY r.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type out struct {
		ID              int64           `json:"id"`
		PurchaseOrderID int64           `json:"purchase_order_id"`
		PONumber        string          `json:"po_number"`
		VendorName      string          `json:"vendor_name"`
		ReceiveNumber   string          `json:"receive_number"`
		ReceivedDate    string          `json:"received_date"`
		TrackingNumber  string          `json:"tracking_number"`
		TrackingURL     string          `json:"tracking_url"`
		Items           json.RawMessage `json:"items"`
		Notes           string          `json:"notes"`
		Status          string          `json:"status"`
	}
	list := []out{}
	for rows.Next() {
		var o out
		if err := rows.Scan(&o.ID, &o.PurchaseOrderID, &o.PONumber, &o.VendorName, &o.ReceiveNumber, &o.ReceivedDate, &o.TrackingNumber, &o.TrackingURL, &o.Items, &o.Notes, &o.Status); err != nil {
			serverError(w, err)
			return
		}
		list = append(list, o)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"purchase_receives": list})
}

// poComplete reports whether every PO line is fully received, given the received totals.
func poComplete(lines []poLine, received map[int]int64) bool {
	for _, l := range lines {
		if received[l.LineNo] < l.Quantity {
			return false
		}
	}
	return true
}

// checkReceive validates items against the PO inside the lock and returns the complete flag if this receive becomes "received".
func checkReceive(ctx context.Context, tx pgx.Tx, orgID, poID, excludeID int64, items []receiveItem, status string) (code string, complete bool, err error) {
	var state string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT status,lines FROM purchase_orders WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, poID).Scan(&state, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "invalid_purchase_order", false, nil
	}
	if err != nil {
		return
	}
	if state != "issued" {
		return "purchase_order_not_issued", false, nil
	}
	var lines []poLine
	if err = json.Unmarshal(raw, &lines); err != nil {
		return
	}
	received, transit, err := receiptTotals(ctx, tx, orgID, poID, excludeID)
	if err != nil {
		return
	}
	ordered := map[int]int64{}
	for _, l := range lines {
		ordered[l.LineNo] = l.Quantity
	}
	seen := map[int]bool{}
	for _, it := range items {
		if seen[it.LineNo] || ordered[it.LineNo] == 0 || it.Quantity < 1 || received[it.LineNo]+transit[it.LineNo]+it.Quantity > ordered[it.LineNo] {
			return "invalid_receive_quantity", false, nil
		}
		seen[it.LineNo] = true
	}
	if status == "received" {
		for _, it := range items {
			received[it.LineNo] += it.Quantity
		}
		complete = poComplete(lines, received)
	}
	return
}

func (a *app) createPurchaseReceive(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in receiveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.ReceiveNumber = strings.TrimSpace(in.ReceiveNumber)
	rdate, rok := parseDay(in.ReceivedDate)
	if in.Stage == "" {
		in.Stage = "draft"
	}
	link, err := url.Parse(in.TrackingURL)
	if in.TrackingURL != "" && (err != nil || (link.Scheme != "http" && link.Scheme != "https") || link.Host == "") {
		rok = false
	}
	if in.PurchaseOrderID <= 0 || !rok || !validStage(in.Stage, "draft", "received") || len(in.Items) == 0 || len(in.Items) > 200 ||
		!validTaxText(in.ReceiveNumber, 50) || !validTaxText(in.TrackingNumber, 100) || !validTaxText(in.TrackingURL, 500) || !validTaxText(in.Notes, 4000) {
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	code, complete, err := checkReceive(ctx, tx, access.ID, in.PurchaseOrderID, 0, in.Items, in.Stage)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, 422, code)
		return
	}
	if in.ReceiveNumber == "" {
		var n int64
		if err = tx.QueryRow(ctx, `SELECT count(*)+1 FROM purchase_receives WHERE org_id=$1`, access.ID).Scan(&n); err != nil {
			serverError(w, err)
			return
		}
		in.ReceiveNumber = fmt.Sprintf("PR-%05d", n)
	}
	data, _ := json.Marshal(in.Items)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO purchase_receives(org_id,purchase_order_id,receive_number,received_date,tracking_number,tracking_url,items,notes,status,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, access.ID, in.PurchaseOrderID, in.ReceiveNumber, rdate, in.TrackingNumber, in.TrackingURL, data, in.Notes, in.Stage, u.ID).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "purchase_receives_org_id_receive_number_key") {
			writeError(w, 409, "duplicate_number")
			return
		}
		serverError(w, err)
		return
	}
	if complete {
		if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET status='received',updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, in.PurchaseOrderID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'purchase_receive.created',jsonb_build_object('purchase_receive_id',$3::bigint,'status',$4::text))`, access.ID, u.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "receive_number": in.ReceiveNumber, "status": in.Stage})
}

// setPurchaseReceiveStage promotes a draft receive to received, or cancels it.
func (a *app) setPurchaseReceiveStage(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) || !validStage(in.Stage, "received", "cancelled") {
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var state string
	var poID int64
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT status,purchase_order_id,items FROM purchase_receives WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state, &poID, &raw)
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
	if state != "draft" {
		writeError(w, 422, "invalid_transition")
		return
	}
	if in.Stage == "received" {
		var items []receiveItem
		if err = json.Unmarshal(raw, &items); err != nil {
			serverError(w, err)
			return
		}
		code, complete, err := checkReceive(ctx, tx, access.ID, poID, id, items, "received")
		if err != nil {
			serverError(w, err)
			return
		}
		if code != "" {
			writeError(w, 422, code)
			return
		}
		if complete {
			if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET status='received',updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, poID); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE purchase_receives SET status=$3 WHERE org_id=$1 AND id=$2`, access.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'purchase_receive.stage_changed',jsonb_build_object('purchase_receive_id',$3::bigint,'status',$4::text))`, access.ID, u.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": in.Stage})
}
