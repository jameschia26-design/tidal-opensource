package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// defaultLocation returns the company's default stock location, creating it once if the company has
// never used locations. Movements with no location belong here.
func defaultLocation(ctx context.Context, q rowQueryer, orgID int64) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT id FROM inventory_locations WHERE org_id=$1 AND is_default`, orgID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return 0, err
	}
	if err := q.QueryRow(ctx, `INSERT INTO inventory_locations(org_id,name,is_default) VALUES($1,'Main',true)
		ON CONFLICT (org_id) WHERE is_default DO UPDATE SET name=inventory_locations.name RETURNING id`, orgID).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

type stockLocation struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"is_default"`
}

// listStockLocations: GET /api/v1/stock/locations?org_id
func (a *app) listStockLocations(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	if _, err := defaultLocation(r.Context(), a.db, access.ID); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,is_default FROM inventory_locations WHERE org_id=$1 ORDER BY is_default DESC,name`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []stockLocation{}
	for rows.Next() {
		var v stockLocation
		if err := rows.Scan(&v.ID, &v.Name, &v.Default); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": out})
}

// createStockLocation: POST /api/v1/stock/locations {org_id,name}
func (a *app) createStockLocation(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, err := defaultLocation(r.Context(), a.db, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var id int64
	err := a.db.QueryRow(r.Context(), `INSERT INTO inventory_locations(org_id,name) VALUES($1,$2) RETURNING id`, access.ID, name).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "location_exists")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name})
}

type locationStockRow struct {
	ItemID       int64  `json:"item_id"`
	SKU          string `json:"sku"`
	Name         string `json:"name"`
	LocationID   int64  `json:"location_id"`
	LocationName string `json:"location"`
	Qty          int64  `json:"qty"`
}

// stockByLocation: GET /api/v1/reports/stock-by-location?org_id&as_of — quantity on hand per item and
// location. Documents move stock at the default location; transfers move it between locations, so the
// sum across locations equals the item's total on hand.
func (a *app) stockByLocation(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf := day(time.Now())
	if s := r.URL.Query().Get("as_of"); s != "" {
		d, okDay := parseDay(s)
		if !okDay {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		asOf = day(d)
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	def, err := defaultLocation(r.Context(), a.db, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `
		WITH moved AS (
			SELECT item_id, COALESCE(location_id,$2) AS location_id, SUM(qty_delta)::bigint AS qty
			FROM stock_movements WHERE org_id=$1 AND movement_date<=$3::date GROUP BY 1,2
			UNION ALL
			SELECT item_id, from_location_id, -SUM(qty)::bigint FROM stock_transfers WHERE org_id=$1 AND transfer_date<=$3::date GROUP BY 1,2
			UNION ALL
			SELECT item_id, to_location_id, SUM(qty)::bigint FROM stock_transfers WHERE org_id=$1 AND transfer_date<=$3::date GROUP BY 1,2
		)
		SELECT i.id,i.sku,i.name,l.id,l.name,COALESCE(SUM(m.qty),0)::bigint
		FROM items i
		CROSS JOIN inventory_locations l
		LEFT JOIN moved m ON m.item_id=i.id AND m.location_id=l.id
		WHERE i.org_id=$1 AND i.track_inventory AND l.org_id=$1
		GROUP BY i.id,i.sku,i.name,l.id,l.name
		ORDER BY i.name,i.id,l.is_default DESC,l.name`, access.ID, def, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []locationStockRow{}
	for rows.Next() {
		var v locationStockRow
		if err := rows.Scan(&v.ItemID, &v.SKU, &v.Name, &v.LocationID, &v.LocationName, &v.Qty); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"as_of": asOf, "rows": out})
}

// createStockTransfer: POST /api/v1/stock/transfers — move quantity between two locations. The
// source location must hold enough stock at the transfer date, so a transfer can never invent units.
func (a *app) createStockTransfer(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID          int64  `json:"org_id"`
		ItemID         int64  `json:"item_id"`
		FromLocationID int64  `json:"from_location_id"`
		ToLocationID   int64  `json:"to_location_id"`
		TransferDate   string `json:"transfer_date"`
		Qty            int64  `json:"qty"`
		Note           string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	transferDate, valid := parseDay(in.TransferDate)
	if !valid || in.Qty <= 0 || in.ItemID <= 0 || in.FromLocationID == in.ToLocationID || in.FromLocationID <= 0 || in.ToLocationID <= 0 || len(in.Note) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	date := day(transferDate)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var tracked bool
	if err := tx.QueryRow(r.Context(), `SELECT track_inventory FROM items WHERE org_id=$1 AND id=$2`, access.ID, in.ItemID).Scan(&tracked); err != nil || !tracked {
		writeError(w, http.StatusUnprocessableEntity, "invalid_item")
		return
	}
	var locationCount int
	if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM inventory_locations WHERE org_id=$1 AND id IN ($2,$3)`, access.ID, in.FromLocationID, in.ToLocationID).Scan(&locationCount); err != nil {
		serverError(w, err)
		return
	}
	if locationCount != 2 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_location")
		return
	}
	def, err := defaultLocation(r.Context(), tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var held int64
	if err := tx.QueryRow(r.Context(), `SELECT
		COALESCE((SELECT SUM(qty_delta) FROM stock_movements WHERE org_id=$1 AND item_id=$2 AND movement_date<=$3::date AND COALESCE(location_id,$5)=$4),0)
		- COALESCE((SELECT SUM(qty) FROM stock_transfers WHERE org_id=$1 AND item_id=$2 AND transfer_date<=$3::date AND from_location_id=$4),0)
		+ COALESCE((SELECT SUM(qty) FROM stock_transfers WHERE org_id=$1 AND item_id=$2 AND transfer_date<=$3::date AND to_location_id=$4),0)`,
		access.ID, in.ItemID, date, in.FromLocationID, def).Scan(&held); err != nil {
		serverError(w, err)
		return
	}
	if held < in.Qty {
		writeError(w, http.StatusUnprocessableEntity, "insufficient_stock")
		return
	}
	var id int64
	if err := tx.QueryRow(r.Context(), `INSERT INTO stock_transfers(org_id,item_id,from_location_id,to_location_id,transfer_date,qty,note,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		access.ID, in.ItemID, in.FromLocationID, in.ToLocationID, date, in.Qty, strings.TrimSpace(in.Note), u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}
