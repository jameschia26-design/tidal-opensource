package main

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// controlAccounts reads and sets which accounts hold customer (receivable) and supplier (payable) balances.
// A change is allowed only while the account in use nets to zero; postings and changes share an org lock.
func (a *app) controlAccounts(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID        int64 `json:"org_id"`
		ReceivableID int64 `json:"receivable_account_id"`
		PayableID    int64 `json:"payable_account_id"`
	}
	if r.Method == "GET" {
		var ok bool
		if in.OrgID, ok = parseOrgID(w, r.URL.Query().Get("org_id")); !ok {
			return
		}
	} else if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, r.Method != "GET")
	if !ok {
		return
	}
	ctx := r.Context()
	if r.Method == "GET" {
		var ar, ap int64
		err := a.db.QueryRow(ctx, `SELECT receivable_account_id,payable_account_id FROM control_accounts WHERE org_id=$1`, access.ID).Scan(&ar, &ap)
		if errors.Is(err, pgx.ErrNoRows) { // standard accounts, if they exist yet
			_ = a.db.QueryRow(ctx, `SELECT COALESCE((SELECT id FROM accounts WHERE org_id=$1 AND code=$2),0),COALESCE((SELECT id FROM accounts WHERE org_id=$1 AND code=$3),0)`, access.ID, acctAR, acctAP).Scan(&ar, &ap)
		} else if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"receivable_account_id": ar, "payable_account_id": ap})
		return
	}
	if in.ReceivableID <= 0 || in.PayableID <= 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// Serialize changes with conversion and other ledger writes that resolve control accounts.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	current, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for _, c := range []struct {
		id, now int64
		typ     string
	}{{in.ReceivableID, current[acctAR], "asset"}, {in.PayableID, current[acctAP], "liability"}} {
		var typ string
		if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, c.id).Scan(&typ); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 422, "account_not_allowed")
			} else {
				serverError(w, err)
			}
			return
		}
		if typ != c.typ {
			writeError(w, 422, "account_not_allowed")
			return
		}
		if c.id == c.now {
			continue
		}
		var balance int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, access.ID, c.now).Scan(&balance); err != nil {
			serverError(w, err)
			return
		}
		if balance != 0 {
			writeError(w, 409, "account_in_use")
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO control_accounts(org_id,receivable_account_id,payable_account_id) VALUES($1,$2,$3)
		ON CONFLICT (org_id) DO UPDATE SET receivable_account_id=EXCLUDED.receivable_account_id, payable_account_id=EXCLUDED.payable_account_id`, access.ID, in.ReceivableID, in.PayableID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'control_accounts.updated')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"receivable_account_id": in.ReceivableID, "payable_account_id": in.PayableID})
}
