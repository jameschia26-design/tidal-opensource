package main

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type itemInput struct {
	OrgID               int64  `json:"org_id"`
	Name                string `json:"name"`
	ItemType            string `json:"item_type"`
	Unit                string `json:"unit"`
	SKU                 string `json:"sku"`
	SellingPrice        int64  `json:"selling_price_cents"`
	CostPrice           int64  `json:"cost_price_cents"`
	SalesAccountID      int64  `json:"sales_account_id"`
	PurchaseAccountID   int64  `json:"purchase_account_id"`
	SalesDescription    string `json:"sales_description"`
	PurchaseDescription string `json:"purchase_description"`
	PreferredVendorID   int64  `json:"preferred_vendor_id"`
	TrackInventory      bool   `json:"track_inventory"`
	InventoryAssetID    int64  `json:"inventory_asset_account_id"`
	COGSAccountID       int64  `json:"cogs_account_id"`
	Archived            bool   `json:"archived"`
}

type itemOutput struct {
	ID int64 `json:"id"`
	itemInput
}

func (a *app) listItems(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,item_type,unit,sku,selling_price_cents,cost_price_cents,sales_account_id,COALESCE(purchase_account_id,0),sales_description,purchase_description,COALESCE(preferred_vendor_id,0),track_inventory,COALESCE(inventory_asset_account_id,0),COALESCE(cogs_account_id,0),archived FROM items WHERE org_id=$1 ORDER BY name,id LIMIT 500`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []itemOutput{}
	for rows.Next() {
		var v itemOutput
		v.OrgID = access.ID
		if err := rows.Scan(&v.ID, &v.Name, &v.ItemType, &v.Unit, &v.SKU, &v.SellingPrice, &v.CostPrice, &v.SalesAccountID, &v.PurchaseAccountID, &v.SalesDescription, &v.PurchaseDescription, &v.PreferredVendorID, &v.TrackInventory, &v.InventoryAssetID, &v.COGSAccountID, &v.Archived); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func (a *app) saveItem(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in itemInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.SKU = strings.TrimSpace(in.SKU)
	if in.Name == "" || (in.ItemType != "goods" && in.ItemType != "service") || in.SellingPrice < 0 || in.SellingPrice > maxMoneyCents || in.CostPrice < 0 || in.CostPrice > maxMoneyCents || in.SalesAccountID < 0 || in.PurchaseAccountID < 0 || in.PreferredVendorID < 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	for _, f := range []struct {
		value string
		limit int
	}{{in.Name, 200}, {in.Unit, 40}, {in.SKU, 100}, {in.SalesDescription, 4000}, {in.PurchaseDescription, 4000}} {
		if !utf8.ValidString(f.value) || utf8.RuneCountInString(f.value) > f.limit || strings.ContainsRune(f.value, 0) {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	// Xero only tracks stock for goods; a service has nothing to count.
	if in.InventoryAssetID < 0 || in.COGSAccountID < 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if in.TrackInventory && in.ItemType != "goods" {
		writeError(w, 422, "inventory_goods_only")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if !a.requireFeature(w, r.Context(), access.ID, u.ID, "items") {
		return
	}
	in.OrgID = access.ID
	ctx := r.Context()
	if in.InventoryAssetID > 0 {
		var ok bool
		if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2 AND type='asset')`, access.ID, in.InventoryAssetID).Scan(&ok); err != nil || !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_account")
			return
		}
	}
	if in.COGSAccountID > 0 {
		var ok bool
		if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2 AND type IN ('expense','cost_of_sales'))`, access.ID, in.COGSAccountID).Scan(&ok); err != nil || !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_account")
			return
		}
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.SalesAccountID == 0 {
		in.SalesAccountID = ids[acctSales]
	}
	for _, account := range []struct {
		id  int64
		typ string
	}{{in.SalesAccountID, "revenue"}, {in.PurchaseAccountID, "expense"}} {
		if account.id == 0 {
			continue
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2 AND type=$3)`, access.ID, account.id, account.typ).Scan(&valid); err != nil {
			serverError(w, err)
			return
		}
		if !valid {
			writeError(w, 422, "invalid_account")
			return
		}
	}
	if in.PreferredVendorID > 0 {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE org_id=$1 AND id=$2 AND kind IN ('supplier','both'))`, access.ID, in.PreferredVendorID).Scan(&valid); err != nil {
			serverError(w, err)
			return
		}
		if !valid {
			writeError(w, 422, "invalid_supplier")
			return
		}
	}
	var id int64
	status := 201
	values := []any{access.ID, in.Name, in.ItemType, in.Unit, in.SKU, in.SellingPrice, in.CostPrice, in.SalesAccountID, in.PurchaseAccountID, in.SalesDescription, in.PurchaseDescription, in.PreferredVendorID, u.ID, in.TrackInventory, in.InventoryAssetID, in.COGSAccountID, in.Archived}
	query := `INSERT INTO items(org_id,name,item_type,unit,sku,selling_price_cents,cost_price_cents,sales_account_id,purchase_account_id,sales_description,purchase_description,preferred_vendor_id,created_by,track_inventory,inventory_asset_account_id,cogs_account_id,archived) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9::bigint,0),$10,$11,NULLIF($12::bigint,0),$13,$14,NULLIF($15::bigint,0),NULLIF($16::bigint,0),$17) RETURNING id`
	if r.Method == http.MethodPut {
		var ok bool
		id, ok = pathID(w, r)
		if !ok {
			return
		}
		values[12] = id
		status = 200
		query = `UPDATE items SET name=$2,item_type=$3,unit=$4,sku=$5,selling_price_cents=$6,cost_price_cents=$7,sales_account_id=$8,purchase_account_id=NULLIF($9::bigint,0),sales_description=$10,purchase_description=$11,preferred_vendor_id=NULLIF($12::bigint,0),track_inventory=$14,inventory_asset_account_id=NULLIF($15::bigint,0),cogs_account_id=NULLIF($16::bigint,0),archived=$17,updated_at=now() WHERE org_id=$1 AND id=$13 RETURNING id`
	}
	if err := tx.QueryRow(ctx, query, values...).Scan(&id); err != nil {
		var pe *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
		} else if errors.As(err, &pe) && pe.Code == "23505" {
			writeError(w, 409, "sku_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'item.saved')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, itemOutput{ID: id, itemInput: in})
}
