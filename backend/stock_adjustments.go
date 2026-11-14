package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Manual stock changes. An adjustment moves one item up or down with a reason; a stocktake records
// counted quantities and posts one adjustment per item whose count differs from the quantity on hand
// at the count date. Both write the same append-only movement rows the posted documents write, so the
// inventory reports and the invoice posting guard see them without special cases.
// ponytail: one stock pool per company, so there are no location transfers; add a locations table when
// a company needs stock in more than one place.

type stockAdjustmentIn struct {
	OrgID          int64  `json:"org_id"`
	ItemID         int64  `json:"item_id"`
	AdjustmentDate string `json:"adjustment_date"`
	QtyDelta       int64  `json:"qty_delta"`
	UnitCostCents  int64  `json:"unit_cost_cents"`
	Reason         string `json:"reason"`
	Note           string `json:"note"`
}

type stocktakeIn struct {
	OrgID   int64  `json:"org_id"`
	TakenOn string `json:"taken_on"`
	Note    string `json:"note"`
	Lines   []struct {
		ItemID     int64 `json:"item_id"`
		CountedQty int64 `json:"counted_qty"`
	} `json:"lines"`
}

// onHand returns the stock quantity for one item at a date, the same sum the inventory report uses.
func onHand(ctx context.Context, tx pgx.Tx, orgID, itemID int64, on string) (int64, error) {
	var qty int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(qty_delta),0)::bigint FROM stock_movements
		WHERE org_id=$1 AND item_id=$2 AND movement_date<=$3::date`, orgID, itemID, on).Scan(&qty)
	return qty, err
}

// trackedGoods reports whether the item is a tracked goods item in this company, and its cost price.
func trackedGoods(ctx context.Context, tx pgx.Tx, orgID, itemID int64) (cost int64, ok bool, err error) {
	var kind string
	var tracked bool
	err = tx.QueryRow(ctx, `SELECT cost_price_cents,item_type,track_inventory FROM items WHERE org_id=$1 AND id=$2`,
		orgID, itemID).Scan(&cost, &kind, &tracked)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return cost, kind == "goods" && tracked, nil
}

// stockWriteError reports a closed-period refusal as 422 and anything else as a server error.
func stockWriteError(w http.ResponseWriter, err error) {
	if isPeriodClosed(err) {
		writeError(w, http.StatusUnprocessableEntity, "period_closed")
		return
	}
	serverError(w, err)
}

// postStockValueJournal books the money side of a manual stock change: Dr Inventory / Cr Cost of Sales when
// quantity rises, reversed when it falls. The cents match the movement exactly, so the inventory account
// always equals the stock report's value.
func postStockValueJournal(ctx context.Context, tx pgx.Tx, orgID, userID int64, date time.Time, memo, key string, qty, unitCost int64) error {
	value := qty * unitCost
	if value < 0 {
		value = -value
	}
	if value == 0 {
		return nil
	}
	ids, err := accountIDs(ctx, tx, orgID)
	if err != nil {
		return err
	}
	debit, credit := ids[acctInventory], ids[acctStockAdj]
	if qty < 0 {
		debit, credit = credit, debit
	}
	hash := sha256.Sum256([]byte(key))
	_, err = postJournal(ctx, tx, orgID, userID, date, memo, key, hash, debit, credit, value)
	return err
}

// createStockAdjustment: POST /api/v1/stock/adjustments — one manual quantity change with a reason.
func (a *app) createStockAdjustment(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in stockAdjustmentIn
	if !decodeJSON(w, r, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	moveDate, okDay := parseDay(in.AdjustmentDate)
	if in.ItemID <= 0 || !okDay || in.QtyDelta == 0 || in.QtyDelta < -maxQty || in.QtyDelta > maxQty ||
		in.UnitCostCents < 0 || in.UnitCostCents > maxMoneyCents || reason == "" || utf8.RuneCountInString(reason) > 100 ||
		!utf8.ValidString(reason) || !validTaxText(in.Note, 4000) {
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
	cost, tracked, err := trackedGoods(ctx, tx, access.ID, in.ItemID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !tracked {
		writeError(w, 422, "inventory_goods_only")
		return
	}
	on, err := onHand(ctx, tx, access.ID, in.ItemID, day(moveDate))
	if err != nil {
		serverError(w, err)
		return
	}
	if on+in.QtyDelta < 0 {
		writeError(w, 409, "insufficient_stock")
		return
	}
	if in.UnitCostCents == 0 {
		// Default to the cost on hand (D1), so an adjustment at zero stock falls back to the item cost.
		avg, aerr := movingAverageCost(ctx, tx, access.ID, in.ItemID, day(moveDate))
		if aerr != nil {
			serverError(w, aerr)
			return
		}
		in.UnitCostCents = avg
		if in.UnitCostCents == 0 {
			in.UnitCostCents = cost
		}
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO stock_adjustments(org_id,item_id,adjustment_date,qty_delta,unit_cost_cents,reason,note,created_by)
		VALUES($1,$2,$3::date,$4,$5,$6,$7,$8) RETURNING id`,
		access.ID, in.ItemID, day(moveDate), in.QtyDelta, in.UnitCostCents, reason, in.Note, u.ID).Scan(&id); err != nil {
		stockWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stock_movements(org_id,item_id,movement_date,qty_delta,unit_cost_cents,source_kind,source_id)
		VALUES($1,$2,$3::date,$4,$5,'adjustment',$6)`,
		access.ID, in.ItemID, day(moveDate), in.QtyDelta, in.UnitCostCents, id); err != nil {
		stockWriteError(w, err)
		return
	}
	if err = postStockValueJournal(ctx, tx, access.ID, u.ID, moveDate, "Stock adjustment: "+reason, "stock-adjustment:"+strconv.FormatInt(id, 10), in.QtyDelta, in.UnitCostCents); err != nil {
		stockWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'stock.adjusted',jsonb_build_object('adjustment_id',$3::bigint,'item_id',$4::bigint))`,
		access.ID, u.ID, id, in.ItemID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"adjustment_id": id, "qty_delta": in.QtyDelta, "on_hand": on + in.QtyDelta})
}

// createStocktake: POST /api/v1/stock/stocktakes — counted quantities, posting the differences.
func (a *app) createStocktake(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in stocktakeIn
	if !decodeJSON(w, r, &in) {
		return
	}
	takenOn, okDay := parseDay(in.TakenOn)
	if !okDay || len(in.Lines) == 0 || len(in.Lines) > 500 || !validTaxText(in.Note, 4000) {
		writeError(w, 400, "invalid_input")
		return
	}
	seen := map[int64]bool{}
	for _, l := range in.Lines {
		if l.ItemID <= 0 || l.CountedQty < 0 || l.CountedQty > maxQty || seen[l.ItemID] {
			writeError(w, 400, "invalid_input")
			return
		}
		seen[l.ItemID] = true
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
	// Validate every item before writing, so a bad row leaves no stocktake behind.
	for _, l := range in.Lines {
		if _, tracked, err := trackedGoods(ctx, tx, access.ID, l.ItemID); err != nil {
			serverError(w, err)
			return
		} else if !tracked {
			writeError(w, 422, "inventory_goods_only")
			return
		}
	}
	lines, _ := json.Marshal(in.Lines)
	var stocktakeID int64
	if err = tx.QueryRow(ctx, `INSERT INTO stocktakes(org_id,taken_on,note,lines,created_by) VALUES($1,$2::date,$3,$4,$5) RETURNING id`,
		access.ID, day(takenOn), in.Note, lines, u.ID).Scan(&stocktakeID); err != nil {
		serverError(w, err)
		return
	}
	adjusted := 0
	for _, l := range in.Lines {
		cost, _, err := trackedGoods(ctx, tx, access.ID, l.ItemID)
		if err != nil {
			serverError(w, err)
			return
		}
		on, err := onHand(ctx, tx, access.ID, l.ItemID, day(takenOn))
		if err != nil {
			serverError(w, err)
			return
		}
		delta := l.CountedQty - on
		if delta == 0 {
			continue
		}
		if avg, aerr := movingAverageCost(ctx, tx, access.ID, l.ItemID, day(takenOn)); aerr != nil {
			serverError(w, aerr)
			return
		} else if avg > 0 {
			cost = avg
		}
		var adjID int64
		if err = tx.QueryRow(ctx, `INSERT INTO stock_adjustments(org_id,item_id,adjustment_date,qty_delta,unit_cost_cents,reason,note,stocktake_id,created_by)
			VALUES($1,$2,$3::date,$4,$5,'Stocktake',$6,$7,$8) RETURNING id`,
			access.ID, l.ItemID, day(takenOn), delta, cost, in.Note, stocktakeID, u.ID).Scan(&adjID); err != nil {
			stockWriteError(w, err)
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stock_movements(org_id,item_id,movement_date,qty_delta,unit_cost_cents,source_kind,source_id)
			VALUES($1,$2,$3::date,$4,$5,'stocktake',$6)`,
			access.ID, l.ItemID, day(takenOn), delta, cost, adjID); err != nil {
			stockWriteError(w, err)
			return
		}
		if err = postStockValueJournal(ctx, tx, access.ID, u.ID, takenOn, "Stocktake", "stocktake:"+strconv.FormatInt(adjID, 10), delta, cost); err != nil {
			stockWriteError(w, err)
			return
		}
		adjusted++
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'stock.stocktake_posted',jsonb_build_object('stocktake_id',$3::bigint,'adjusted',$4::int))`,
		access.ID, u.ID, stocktakeID, adjusted); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"stocktake_id": stocktakeID, "items_counted": len(in.Lines), "adjusted": adjusted})
}

// listStockAdjustments: GET /api/v1/stock/adjustments?org_id[&item_id] — the manual changes, newest first.
func (a *app) listStockAdjustments(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	itemID, okItem := parseOrgID(w, r.URL.Query().Get("item_id"))
	if !okItem {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.id,a.item_id,i.name,a.adjustment_date::text,a.qty_delta,a.unit_cost_cents,a.reason,a.note,COALESCE(a.stocktake_id,0)
		FROM stock_adjustments a JOIN items i ON i.org_id=a.org_id AND i.id=a.item_id
		WHERE a.org_id=$1 AND ($2::bigint=0 OR a.item_id=$2) ORDER BY a.adjustment_date DESC,a.id DESC LIMIT 200`,
		access.ID, itemID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, item, qty, cost, stocktake int64
		var name, date, reason, note string
		if err := rows.Scan(&id, &item, &name, &date, &qty, &cost, &reason, &note, &stocktake); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "item_id": item, "item_name": name, "adjustment_date": date,
			"qty_delta": qty, "unit_cost_cents": cost, "reason": reason, "note": note, "stocktake_id": stocktake})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"adjustments": out})
}
