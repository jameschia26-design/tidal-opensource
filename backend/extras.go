package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"context"

	"github.com/jackc/pgx/v5"
)

// ponytail: values are validated on write against active definitions only; a later deactivation keeps old values.
// Add typed value tables if reports need to filter by custom field.
var numberValue = regexp.MustCompile(`^-?\d{1,12}(\.\d{1,4})?$`)

func (a *app) listCustomFields(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	rows, err := a.db.Query(r.Context(), `SELECT id,entity_kind,label,field_type,active FROM custom_fields WHERE org_id=$1 AND ($2='' OR entity_kind=$2) ORDER BY entity_kind,id LIMIT 500`, access.ID, kind)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var k, label, typ string
		var active bool
		if err := rows.Scan(&id, &k, &label, &typ, &active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "entity_kind": k, "label": label, "field_type": typ, "active": active})
	}
	if rows.Err() != nil {
		serverError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"custom_fields": out})
}

func (a *app) createCustomField(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Kind  string `json:"entity_kind"`
		Label string `json:"label"`
		Type  string `json:"field_type"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Label = strings.TrimSpace(in.Label)
	if t, _ := attachmentTable(in.Kind); t == "" || in.Label == "" || !validTaxText(in.Label, 100) || (in.Type != "text" && in.Type != "number" && in.Type != "date" && in.Type != "yesno") {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var n int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM custom_fields WHERE org_id=$1 AND entity_kind=$2`, access.ID, in.Kind).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	if n >= 30 {
		writeError(w, 422, "extras_limit")
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO custom_fields(org_id,entity_kind,label,field_type) VALUES($1,$2,$3,$4) RETURNING id`, access.ID, in.Kind, in.Label, in.Type).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *app) setDefinitionActive(table string) func(http.ResponseWriter, *http.Request, currentUser) {
	return func(w http.ResponseWriter, r *http.Request, u currentUser) {
		var in struct {
			OrgID  int64 `json:"org_id"`
			Active bool  `json:"active"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
		if !ok {
			return
		}
		// table comes from fixed callers in main.go, never from input.
		tag, err := a.db.Exec(r.Context(), fmt.Sprintf(`UPDATE %s SET active=$3 WHERE org_id=$1 AND id=$2`, table), access.ID, id, in.Active)
		if err != nil {
			serverError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "active": in.Active})
	}
}

func (a *app) listReportingTags(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,options,active FROM reporting_tags WHERE org_id=$1 ORDER BY id LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var opts json.RawMessage
		var active bool
		if err := rows.Scan(&id, &name, &opts, &active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "options": opts, "active": active})
	}
	if rows.Err() != nil {
		serverError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"reporting_tags": out})
}

func (a *app) createReportingTag(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID   int64    `json:"org_id"`
		Name    string   `json:"name"`
		Options []string `json:"options"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	seen := map[string]bool{}
	for i, o := range in.Options {
		in.Options[i] = strings.TrimSpace(o)
		k := strings.ToLower(in.Options[i])
		if in.Options[i] == "" || !validTaxText(in.Options[i], 100) || seen[k] {
			writeError(w, 400, "invalid_input")
			return
		}
		seen[k] = true
	}
	if in.Name == "" || !validTaxText(in.Name, 100) || len(in.Options) < 1 || len(in.Options) > 50 {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var n int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM reporting_tags WHERE org_id=$1`, access.ID).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	if n >= 30 {
		writeError(w, 422, "extras_limit")
		return
	}
	opts, _ := json.Marshal(in.Options)
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO reporting_tags(org_id,name,options) VALUES($1,$2,$3) RETURNING id`, access.ID, in.Name, opts).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// entityExtras reads or replaces the custom field values and tag choices of one record.
func (a *app) entityExtras(w http.ResponseWriter, r *http.Request, u currentUser) {
	write := r.Method == http.MethodPut
	var in struct {
		OrgID  int64             `json:"org_id"`
		Custom map[string]string `json:"custom"`
		Tags   map[string]string `json:"tags"`
	}
	orgID := int64(0)
	if write {
		if !decodeJSON(w, r, &in) {
			return
		}
		orgID = in.OrgID
	} else {
		var ok bool
		if orgID, ok = parseOrgID(w, r.URL.Query().Get("org_id")); !ok {
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, write)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	table, _ := attachmentTable(kind)
	if table == "" {
		writeError(w, 400, "invalid_input")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var found int64
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE org_id=$1 AND id=$2 FOR UPDATE`, table), access.ID, id).Scan(&found); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 404, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if !write {
		var custom, tags json.RawMessage
		err := tx.QueryRow(ctx, `SELECT custom,tags FROM entity_extras WHERE org_id=$1 AND entity_kind=$2 AND entity_id=$3`, access.ID, kind, id).Scan(&custom, &tags)
		if err == pgx.ErrNoRows {
			custom, tags = json.RawMessage(`{}`), json.RawMessage(`{}`)
		} else if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"custom": custom, "tags": tags})
		return
	}
	custom, tags, status, code, validationErr := validateExtras(ctx, tx, access.ID, kind, in.Custom, in.Tags)
	if validationErr != nil {
		serverError(w, validationErr)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	if err := saveExtrasTx(ctx, tx, access.ID, kind, id, custom, tags); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"custom": custom, "tags": tags})
}

// validateExtras checks custom field and reporting tag values against the company's definitions.
// It returns cleaned maps, or an HTTP status and error code when a value is refused.
func validateExtras(ctx context.Context, tx pgx.Tx, orgID int64, kind string, inCustom, inTags map[string]string) (map[string]string, map[string]string, int, string, error) {
	custom, tags := map[string]string{}, map[string]string{}
	for k, v := range inCustom {
		fid, err := strconv.ParseInt(k, 10, 64)
		v = strings.TrimSpace(v)
		if err != nil || fid <= 0 {
			return nil, nil, http.StatusBadRequest, "invalid_input", nil
		}
		if v == "" {
			continue
		}
		var typ string
		err = tx.QueryRow(ctx, `SELECT field_type FROM custom_fields WHERE org_id=$1 AND id=$2 AND entity_kind=$3 AND active`, orgID, fid, kind).Scan(&typ)
		if err == pgx.ErrNoRows {
			return nil, nil, http.StatusUnprocessableEntity, "invalid_extra", nil
		} else if err != nil {
			return nil, nil, 0, "", err
		}
		okVal := false
		switch typ {
		case "text":
			okVal = validTaxText(v, 500)
		case "number":
			okVal = numberValue.MatchString(v)
		case "date":
			_, okVal = parseDay(v)
		case "yesno":
			okVal = v == "yes" || v == "no"
		}
		if !okVal {
			return nil, nil, http.StatusUnprocessableEntity, "invalid_extra", nil
		}
		custom[k] = v
	}
	for k, v := range inTags {
		tid, err := strconv.ParseInt(k, 10, 64)
		v = strings.TrimSpace(v)
		if err != nil || tid <= 0 {
			return nil, nil, http.StatusBadRequest, "invalid_input", nil
		}
		if v == "" {
			continue
		}
		var good bool
		err = tx.QueryRow(ctx, `SELECT active AND jsonb_exists(options,$3) FROM reporting_tags WHERE org_id=$1 AND id=$2`, orgID, tid, v).Scan(&good)
		if err == pgx.ErrNoRows || (err == nil && !good) {
			return nil, nil, http.StatusUnprocessableEntity, "invalid_extra", nil
		} else if err != nil {
			return nil, nil, 0, "", err
		}
		tags[k] = v
	}
	return custom, tags, 0, "", nil
}

// saveExtrasTx writes one record's custom fields and tags inside the caller's transaction.
func saveExtrasTx(ctx context.Context, tx pgx.Tx, orgID int64, kind string, id int64, custom, tags map[string]string) error {
	cj, _ := json.Marshal(custom)
	tj, _ := json.Marshal(tags)
	_, err := tx.Exec(ctx, `INSERT INTO entity_extras(org_id,entity_kind,entity_id,custom,tags) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (org_id,entity_kind,entity_id) DO UPDATE SET custom=EXCLUDED.custom,tags=EXCLUDED.tags,updated_at=now()`, orgID, kind, id, cj, tj)
	return err
}
