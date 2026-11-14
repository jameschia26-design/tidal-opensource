package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// movingAverageCost is the weighted-average unit cost on hand for an item at a date, in cents (D1).
// Zero stock is worth zero, so a later items.cost_price_cents change cannot rewrite past movements.
func movingAverageCost(ctx context.Context, q rowQueryer, orgID, itemID int64, on string) (int64, error) {
	var cost int64
	err := q.QueryRow(ctx, `SELECT CASE WHEN SUM(qty_delta)>0 THEN SUM(qty_delta*unit_cost_cents)/SUM(qty_delta) ELSE 0 END
		FROM stock_movements WHERE org_id=$1 AND item_id=$2 AND movement_date<=$3::date`, orgID, itemID, on).Scan(&cost)
	return cost, err
}

// postStockMovements records stock for a posted document. Only items marked track_inventory move, and each
// document contributes one row per item: the document's lines for that item are summed.
// Invoices take stock out at the moving-average cost (D1); bills bring stock in at the line's weighted average.
func postStockMovements(ctx context.Context, tx pgx.Tx, orgID int64, kind string, docID int64, movDate time.Time) error {
	d, err := documentStockMoves(ctx, tx, orgID, kind, docID, day(movDate))
	if err != nil {
		return err
	}
	for _, m := range d {
		if m.qty == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO stock_movements(org_id,item_id,movement_date,qty_delta,unit_cost_cents,source_kind,source_id)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (org_id,item_id,source_kind,source_id) DO NOTHING`,
			orgID, m.item, movDate, m.qty, m.cost, kind, docID); err != nil {
			return err
		}
	}
	return nil
}

// stockMove is one tracked item's quantity and unit cost for a document, before the movement is stored.
type stockMove struct{ item, qty, cost, account int64 }

// documentStockMoves computes the movements a document will post, without writing them, so the journal and
// the stock ledger use identical cents.
func documentStockMoves(ctx context.Context, tx pgx.Tx, orgID int64, kind string, docID int64, on string) ([]stockMove, error) {
	var query string
	var sign int64
	if kind == "bill" {
		sign = 1
		query = `SELECT bl.item_id, SUM(bl.quantity)::bigint,
			(SUM(bl.quantity * bl.unit_price_cents) / SUM(bl.quantity))::bigint, MAX(bl.account_id)::bigint
			FROM bill_lines bl JOIN items i ON i.org_id=bl.org_id AND i.id=bl.item_id
			WHERE bl.org_id=$1 AND bl.bill_id=$2 AND bl.item_id IS NOT NULL AND i.track_inventory
			GROUP BY bl.item_id`
	} else {
		sign = -1
		query = `SELECT il.item_id, SUM(il.quantity)::bigint, 0::bigint, 0::bigint
			FROM invoice_lines il JOIN items i ON i.org_id=il.org_id AND i.id=il.item_id
			WHERE il.org_id=$1 AND il.invoice_id=$2 AND il.item_id IS NOT NULL AND i.track_inventory
			GROUP BY il.item_id`
	}
	rows, err := tx.Query(ctx, query, orgID, docID)
	if err != nil {
		return nil, err
	}
	moves := []stockMove{}
	for rows.Next() {
		var m stockMove
		if err := rows.Scan(&m.item, &m.qty, &m.cost, &m.account); err != nil {
			rows.Close()
			return nil, err
		}
		moves = append(moves, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range moves {
		if sign < 0 {
			// Goods leave at the cost on hand before this document.
			cost, err := movingAverageCost(ctx, tx, orgID, moves[i].item, on)
			if err != nil {
				return nil, err
			}
			moves[i].cost = cost
		}
		moves[i].qty = sign * moves[i].qty
	}
	return moves, nil
}
