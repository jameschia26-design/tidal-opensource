package main

import (
	"net/http"
	"strings"
)

// pricingWidgets stores the mapped name and product; publishing and checkout are not provided.
func (a *app) pricingWidgets(w http.ResponseWriter, r *http.Request, u currentUser) {
	if r.Method == http.MethodGet {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, false)
		if !ok {
			return
		}
		rows, err := a.db.Query(r.Context(), `SELECT w.id,w.name,w.product_id,p.name FROM pricing_widgets w JOIN billing_products p ON p.org_id=w.org_id AND p.id=w.product_id WHERE w.org_id=$1 ORDER BY w.id DESC LIMIT 200`, access.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, productID int64
			var name, product string
			if err := rows.Scan(&id, &name, &productID, &product); err != nil {
				serverError(w, err)
				return
			}
			out = append(out, map[string]any{"id": id, "name": name, "product_id": productID, "product": product})
		}
		if err := rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"widgets": out})
		return
	}
	var in struct {
		OrgID     int64  `json:"org_id"`
		ProductID int64  `json:"product_id"`
		Name      string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 100) || in.ProductID <= 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO pricing_widgets(org_id,product_id,name) VALUES($1,$2,$3) RETURNING id`, access.ID, in.ProductID, in.Name).Scan(&id); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, 422, "invalid_product")
		} else if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}
