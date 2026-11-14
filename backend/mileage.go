package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
)

type mileageRate struct {
	StartDate string `json:"start_date"`
	RateCents int64  `json:"rate_cents"`
}
type mileagePreferences struct {
	OrgID            int64         `json:"org_id"`
	ExpenseAccountID int64         `json:"expense_account_id"`
	Unit             string        `json:"unit"`
	DefaultRateCents int64         `json:"default_rate_cents"`
	Rates            []mileageRate `json:"rates"`
}
type mileageInput struct {
	Mode                string `json:"mode"`
	DistanceThousandths int64  `json:"distance_thousandths"`
	OdometerStart       int64  `json:"odometer_start_thousandths"`
	OdometerEnd         int64  `json:"odometer_end_thousandths"`
}
type mileageRecord struct {
	mileageInput
	Unit      string `json:"unit"`
	RateCents int64  `json:"rate_cents"`
}

func mileageAmount(in *mileageInput, rate int64) (int64, bool) {
	if in.Mode == "odometer" {
		if in.OdometerStart < 0 || in.OdometerEnd <= in.OdometerStart || in.OdometerEnd > 1000000000000 {
			return 0, false
		}
		in.DistanceThousandths = in.OdometerEnd - in.OdometerStart
	} else if in.Mode != "distance" || in.OdometerStart != 0 || in.OdometerEnd != 0 {
		return 0, false
	}
	if in.DistanceThousandths <= 0 || in.DistanceThousandths > 1000000000 || rate < 0 || rate > 100000000 {
		return 0, false
	}
	amount := (in.DistanceThousandths*rate + 500) / 1000
	return amount, amount > 0 && amount <= maxMoneyCents
}

func (a *app) mileagePreferences(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in mileagePreferences
	if r.Method == "GET" {
		var ok bool
		in.OrgID, ok = parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
	} else if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, r.Method != "GET")
	if !ok {
		return
	}
	if r.Method == "GET" {
		var raw []byte
		err := a.db.QueryRow(r.Context(), `SELECT expense_account_id,unit,default_rate_cents,rates FROM mileage_preferences WHERE org_id=$1`, access.ID).Scan(&in.ExpenseAccountID, &in.Unit, &in.DefaultRateCents, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, 200, map[string]any{"preferences": nil})
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if err := json.Unmarshal(raw, &in.Rates); err != nil {
			serverError(w, err)
			return
		}
		in.OrgID = access.ID
		writeJSON(w, 200, map[string]any{"preferences": in})
		return
	}
	if !validStage(in.Unit, "km", "mile") || in.ExpenseAccountID <= 0 || in.DefaultRateCents < 0 || in.DefaultRateCents > 100000000 || len(in.Rates) > 100 {
		writeError(w, 400, "invalid_input")
		return
	}
	sort.Slice(in.Rates, func(i, j int) bool { return in.Rates[i].StartDate < in.Rates[j].StartDate })
	for i, rate := range in.Rates {
		_, valid := parseDay(rate.StartDate)
		if !valid || rate.RateCents < 0 || rate.RateCents > 100000000 || (i > 0 && in.Rates[i-1].StartDate == rate.StartDate) {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	if in.Rates == nil {
		in.Rates = []mileageRate{}
	}
	var typ string
	err := a.db.QueryRow(r.Context(), `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.ExpenseAccountID).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "expense") {
		writeError(w, 422, "invalid_expense_account")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	raw, err := json.Marshal(in.Rates)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO mileage_preferences(org_id,expense_account_id,unit,default_rate_cents,rates) VALUES($1,$2,$3,$4,$5) ON CONFLICT(org_id) DO UPDATE SET expense_account_id=excluded.expense_account_id,unit=excluded.unit,default_rate_cents=excluded.default_rate_cents,rates=excluded.rates`, access.ID, in.ExpenseAccountID, in.Unit, in.DefaultRateCents, raw)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"preferences": in})
}
