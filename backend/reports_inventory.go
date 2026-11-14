package main

import (
	"net/http"
	"time"
)

// stockRow is one tracked item's stock position, in quantity and at movement cost.
type stockRow struct {
	ItemID              int64  `json:"item_id"`
	SKU                 string `json:"sku"`
	Name                string `json:"name"`
	Unit                string `json:"unit"`
	ItemType            string `json:"item_type"`
	UnitCostCents       int64  `json:"unit_cost_cents"`
	SalePriceCents      int64  `json:"sale_price_cents"`
	PurchaseDescription string `json:"purchase_description"`
	Qty                 int64  `json:"qty"`
	ValueCents          int64  `json:"value_cents"`
}

// inventoryItemList: GET /api/v1/reports/inventory-items?org_id&as_of — stock on hand per tracked item at a date.
// Only items with track_inventory appear; value uses the cost recorded on each movement.
func (a *app) inventoryItemList(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf := day(time.Now())
	if s := r.URL.Query().Get("as_of"); s != "" {
		d, okDay := parseDay(s)
		if !okDay {
			writeError(w, 400, "invalid_input")
			return
		}
		asOf = day(d)
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,i.sku,i.name,i.unit,i.item_type,i.cost_price_cents,i.selling_price_cents,i.purchase_description,
		COALESCE(SUM(m.qty_delta),0)::bigint,
		COALESCE(SUM(m.qty_delta*m.unit_cost_cents),0)::bigint
		FROM items i LEFT JOIN stock_movements m ON m.org_id=i.org_id AND m.item_id=i.id AND m.movement_date<=$2::date
		WHERE i.org_id=$1 AND i.track_inventory
		GROUP BY i.id ORDER BY i.name,i.id LIMIT 500`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []stockRow{}
	var total int64
	for rows.Next() {
		var v stockRow
		if err := rows.Scan(&v.ItemID, &v.SKU, &v.Name, &v.Unit, &v.ItemType, &v.UnitCostCents, &v.SalePriceCents,
			&v.PurchaseDescription, &v.Qty, &v.ValueCents); err != nil {
			serverError(w, err)
			return
		}
		total += v.ValueCents
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"as_of": asOf, "rows": out, "total_value_cents": total})
}

// inventoryItemSummary: GET /api/v1/reports/inventory-summary?org_id&from&to — opening, in, out and closing per item.
func (a *app) inventoryItemSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
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
	type summaryRow struct {
		stockRow
		OpenQty    int64 `json:"opening_qty"`
		OpenCents  int64 `json:"opening_value_cents"`
		InQty      int64 `json:"in_qty"`
		InCents    int64 `json:"in_value_cents"`
		OutQty     int64 `json:"out_qty"`
		OutCents   int64 `json:"out_value_cents"`
		CloseQty   int64 `json:"closing_qty"`
		CloseCents int64 `json:"closing_value_cents"`
		// Purchases are stock received on supplier bills; sales are stock issued on invoices, valued at
		// the movement cost that posted the cost of goods sold. Adjustments cover stock adjustments and
		// stocktakes. An invoice line carries no item id, so a sale valued at the item's selling price
		// cannot be sourced and is not reported.
		PurchaseQty     int64 `json:"purchase_qty"`
		PurchaseCents   int64 `json:"purchase_value_cents"`
		SalesQty        int64 `json:"sales_qty"`
		COGSCents       int64 `json:"cogs_value_cents"`
		AdjustmentCents int64 `json:"adjustment_value_cents"`
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,i.sku,i.name,i.unit,i.item_type,i.cost_price_cents,i.selling_price_cents,i.purchase_description,
		COALESCE(SUM(CASE WHEN m.movement_date<$2::date THEN m.qty_delta END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date<$2::date THEN m.qty_delta*m.unit_cost_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.qty_delta>0 THEN m.qty_delta END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.qty_delta>0 THEN m.qty_delta*m.unit_cost_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.qty_delta<0 THEN -m.qty_delta END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.qty_delta<0 THEN -m.qty_delta*m.unit_cost_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date<=$3::date THEN m.qty_delta END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date<=$3::date THEN m.qty_delta*m.unit_cost_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.source_kind='bill' THEN m.qty_delta END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.source_kind='bill' THEN m.qty_delta*m.unit_cost_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.source_kind='invoice' THEN -m.qty_delta END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.source_kind='invoice' THEN -m.qty_delta*m.unit_cost_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN m.movement_date BETWEEN $2::date AND $3::date AND m.source_kind IN ('adjustment','stocktake') THEN m.qty_delta*m.unit_cost_cents END),0)::bigint
		FROM items i LEFT JOIN stock_movements m ON m.org_id=i.org_id AND m.item_id=i.id
		WHERE i.org_id=$1 AND i.track_inventory
		GROUP BY i.id ORDER BY i.name,i.id LIMIT 500`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []summaryRow{}
	for rows.Next() {
		var v summaryRow
		if err := rows.Scan(&v.ItemID, &v.SKU, &v.Name, &v.Unit, &v.ItemType, &v.UnitCostCents, &v.SalePriceCents,
			&v.PurchaseDescription, &v.OpenQty, &v.OpenCents, &v.InQty, &v.InCents, &v.OutQty, &v.OutCents,
			&v.CloseQty, &v.CloseCents, &v.PurchaseQty, &v.PurchaseCents, &v.SalesQty, &v.COGSCents, &v.AdjustmentCents); err != nil {
			serverError(w, err)
			return
		}
		v.Qty, v.ValueCents = v.CloseQty, v.CloseCents
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": out})
}

// inventoryItemDetails: GET /api/v1/reports/inventory-details?org_id&from&to[&item_id] — each stock movement
// in the period with its source document and running quantity for that item.
func (a *app) inventoryItemDetails(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	var itemID int64
	if s := r.URL.Query().Get("item_id"); s != "" {
		id, okID := parseOrgID(w, r.URL.Query().Get("item_id"))
		if !okID {
			return
		}
		itemID = id
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	type detailRow struct {
		ItemID      int64  `json:"item_id"`
		SKU         string `json:"sku"`
		Name        string `json:"name"`
		Movement    string `json:"movement_date"`
		SourceKind  string `json:"source_kind"`
		Reference   string `json:"reference"`
		Description string `json:"description"`
		Contact     string `json:"contact"`
		Qty         int64  `json:"qty_delta"`
		CostCents   int64  `json:"unit_cost_cents"`
		ValueCents  int64  `json:"value_cents"`
		RunningQty  int64  `json:"running_qty"`
	}
	// Running quantity starts from everything up to the period end, then keeps only rows inside the period,
	// so a movement in March carries the opening balance with it (B8).
	rows, err := a.db.Query(r.Context(), `WITH mv AS (
			SELECT m.id,m.item_id,m.movement_date,m.qty_delta,m.unit_cost_cents,m.source_kind,m.source_id,
				SUM(m.qty_delta) OVER (PARTITION BY m.item_id ORDER BY m.movement_date,m.id) AS running
			FROM stock_movements m WHERE m.org_id=$1 AND m.movement_date <= $3::date)
		SELECT mv.item_id,i.sku,i.name,mv.movement_date,mv.source_kind,
			COALESCE(CASE WHEN mv.source_kind='invoice' THEN 'Invoice '||COALESCE(inv.number::text,inv.id::text)
				ELSE b.supplier_reference END,''),
			CASE WHEN mv.source_kind='invoice' THEN i.sales_description ELSE i.purchase_description END,
			COALESCE(ci.name,cb.name,''),
			mv.qty_delta,mv.unit_cost_cents,mv.qty_delta*mv.unit_cost_cents,mv.running
		FROM mv JOIN items i ON i.org_id=$1 AND i.id=mv.item_id
		LEFT JOIN invoices inv ON inv.org_id=$1 AND mv.source_kind='invoice' AND inv.id=mv.source_id
		LEFT JOIN bills b ON b.org_id=$1 AND mv.source_kind='bill' AND b.id=mv.source_id
		LEFT JOIN contacts ci ON ci.org_id=$1 AND ci.id=inv.contact_id
		LEFT JOIN contacts cb ON cb.org_id=$1 AND cb.id=b.contact_id
		WHERE mv.movement_date >= $2::date AND ($4::bigint=0 OR mv.item_id=$4)
		ORDER BY i.name,i.id,mv.movement_date,mv.id LIMIT 5000`, access.ID, from, to, itemID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []detailRow{}
	for rows.Next() {
		var v detailRow
		var d time.Time
		if err := rows.Scan(&v.ItemID, &v.SKU, &v.Name, &d, &v.SourceKind, &v.Reference, &v.Description, &v.Contact,
			&v.Qty, &v.CostCents, &v.ValueCents, &v.RunningQty); err != nil {
			serverError(w, err)
			return
		}
		v.Movement = day(d)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": out, "truncated": len(out) == 5000})
}
