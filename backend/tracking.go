package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Tracking categories and options, Xero's shape: two active categories per company, each with named
// options, chosen per document LINE. Values on a line are {"<category_id>": <option_id>}; the reports
// read posted journal lines, so a line split across two options counts by amount.
//
// ponytail: the option id inside the jsonb is checked on every write in Go, not by a foreign key,
// because the same column holds values for whichever category the company has today. Add a
// line_tracking table if a database-level guarantee is ever needed.

// rowQuerier is the part of pgx.Tx and pgxpool.Pool the tracking checks need.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type trackingOptionOut struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}
type trackingCategoryOut struct {
	ID      int64               `json:"id"`
	Name    string              `json:"name"`
	Active  bool                `json:"active"`
	Options []trackingOptionOut `json:"options"`
}

// listTrackingCategories returns every category with its options, archived ones included so history
// stays readable. Forms pick the active categories and non-archived options from the same response.
func (a *app) listTrackingCategories(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	cats, err := a.trackingCategories(r.Context(), access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"categories": cats})
}

func (a *app) trackingCategories(ctx context.Context, orgID int64) ([]trackingCategoryOut, error) {
	rows, err := a.db.Query(ctx, `SELECT c.id,c.name,c.active,COALESCE(o.id,0),COALESCE(o.name,''),COALESCE(o.archived,false)
		FROM tracking_categories c
		LEFT JOIN tracking_options o ON o.org_id=c.org_id AND o.category_id=c.id
		WHERE c.org_id=$1 ORDER BY c.id, o.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []trackingCategoryOut{}
	for rows.Next() {
		var c trackingCategoryOut
		var oid int64
		var oname string
		var archived bool
		if err := rows.Scan(&c.ID, &c.Name, &c.Active, &oid, &oname, &archived); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != c.ID {
			c.Options = []trackingOptionOut{}
			out = append(out, c)
		}
		if oid != 0 {
			i := len(out) - 1
			out[i].Options = append(out[i].Options, trackingOptionOut{ID: oid, Name: oname, Archived: archived})
		}
	}
	return out, rows.Err()
}

// saveTrackingCategory creates a category with its options (POST) or renames and archives one (PUT).
func (a *app) saveTrackingCategory(w http.ResponseWriter, r *http.Request, u currentUser) {
	update := r.Method == http.MethodPut
	var in struct {
		OrgID   int64    `json:"org_id"`
		Name    string   `json:"name"`
		Options []string `json:"options"`
		Active  *bool    `json:"active"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 100) {
		writeError(w, 400, "invalid_input")
		return
	}
	seen := map[string]bool{}
	for _, o := range in.Options {
		o = strings.TrimSpace(o)
		k := strings.ToLower(o)
		if o == "" || !validTaxText(o, 100) || seen[k] {
			writeError(w, 400, "invalid_input")
			return
		}
		seen[k] = true
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
	if update {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		active := true
		if in.Active != nil {
			active = *in.Active
		}
		tag, err := tx.Exec(ctx, `UPDATE tracking_categories SET name=$3, active=$4 WHERE org_id=$1 AND id=$2`, access.ID, id, in.Name, active)
		if err != nil {
			writeTrackingError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, 404, "not_found")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tracking_categories(org_id,name) VALUES($1,$2) RETURNING id`, access.ID, in.Name).Scan(&id)
	if err != nil {
		writeTrackingError(w, err)
		return
	}
	for _, o := range in.Options {
		if _, err := tx.Exec(ctx, `INSERT INTO tracking_options(org_id,category_id,name) VALUES($1,$2,$3)`, access.ID, id, strings.TrimSpace(o)); err != nil {
			writeTrackingError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// saveTrackingOption adds an option (POST) or renames and archives one (PUT).
func (a *app) saveTrackingOption(w http.ResponseWriter, r *http.Request, u currentUser) {
	update := r.Method == http.MethodPut
	var in struct {
		OrgID      int64  `json:"org_id"`
		CategoryID int64  `json:"category_id"`
		Name       string `json:"name"`
		Archived   *bool  `json:"archived"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 100) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	if update {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		archived := false
		if in.Archived != nil {
			archived = *in.Archived
		}
		tag, err := a.db.Exec(ctx, `UPDATE tracking_options SET name=$3, archived=$4 WHERE org_id=$1 AND id=$2`, access.ID, id, in.Name, archived)
		if err != nil {
			writeTrackingError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	if in.CategoryID <= 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	var id int64
	if err := a.db.QueryRow(ctx, `INSERT INTO tracking_options(org_id,category_id,name)
		SELECT $1,c.id,$3 FROM tracking_categories c WHERE c.org_id=$1 AND c.id=$2 RETURNING id`, access.ID, in.CategoryID, in.Name).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 422, "invalid_tracking")
			return
		}
		writeTrackingError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// writeTrackingError maps the two-category rule and duplicate names to the contract codes.
func writeTrackingError(w http.ResponseWriter, err error) {
	if isUniqueViolation(err) {
		writeError(w, 409, "tracking_exists")
		return
	}
	if strings.Contains(err.Error(), "at most two active tracking categories") {
		writeError(w, 422, "tracking_limit")
		return
	}
	serverError(w, err)
}

// validTracking checks one line's {"<category_id>": <option_id>} map: every option must belong to the
// company, sit in the category it is filed under, and not be archived. An archived option stays on
// the lines that already carry it but cannot be chosen on a new or edited line.
func validTracking(ctx context.Context, q rowQuerier, orgID int64, tracking map[string]int64) (bool, error) {
	for k, optionID := range tracking {
		categoryID, err := strconv.ParseInt(k, 10, 64)
		if err != nil || categoryID <= 0 || optionID <= 0 {
			return false, nil
		}
		var good bool
		err = q.QueryRow(ctx, `SELECT NOT o.archived AND c.active FROM tracking_options o
			JOIN tracking_categories c ON c.org_id=o.org_id AND c.id=o.category_id
			WHERE o.org_id=$1 AND o.id=$2 AND c.id=$3`, orgID, optionID, categoryID).Scan(&good)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !good {
			return false, nil
		}
	}
	return true, nil
}

// trackingJSON is the stored form of a line's values; an empty map stores as {} so no NULL checks leak
// into the reports.
func trackingJSON(tracking map[string]int64) []byte {
	if len(tracking) == 0 {
		return []byte(`{}`)
	}
	b, err := json.Marshal(tracking)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}
