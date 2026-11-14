package main

import (
	"net/http"
	"strings"
)

// globalSearch: GET /api/v1/search?org_id&q — one short list per record type, so the header search can
// reach a contact, an invoice, a bill or an item without opening each list first.
func (a *app) globalSearch(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(term) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	result := map[string]any{"contacts": []any{}, "invoices": []any{}, "bills": []any{}, "items": []any{}}
	if len(term) < 2 {
		writeJSON(w, http.StatusOK, result)
		return
	}
	like := "%" + term + "%"
	type query struct {
		key, sql string
	}
	queries := []query{
		{"contacts", `SELECT id,name,kind FROM contacts WHERE org_id=$1 AND name ILIKE $2 ORDER BY lower(name) LIMIT 5`},
		{"invoices", `SELECT i.id, i.number::text, c.name, i.total_cents, i.invoice_date::text FROM invoices i JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id WHERE i.org_id=$1 AND (i.number::text ILIKE $2 OR c.name ILIKE $2) ORDER BY i.invoice_date DESC,i.id DESC LIMIT 5`},
		{"bills", `SELECT b.id, b.supplier_reference, c.name, b.total_cents, b.bill_date::text FROM bills b JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id WHERE b.org_id=$1 AND (b.supplier_reference ILIKE $2 OR c.name ILIKE $2) ORDER BY b.bill_date DESC,b.id DESC LIMIT 5`},
		{"items", `SELECT id,name,sku,selling_price_cents FROM items WHERE org_id=$1 AND (name ILIKE $2 OR sku ILIKE $2) ORDER BY lower(name) LIMIT 5`},
	}
	for _, q := range queries {
		rows, err := a.db.Query(r.Context(), q.sql, access.ID, like)
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			switch q.key {
			case "contacts":
				var name, role string
				if err := rows.Scan(&id, &name, &role); err != nil {
					rows.Close()
					serverError(w, err)
					return
				}
				out = append(out, map[string]any{"id": id, "name": name, "role": role})
			case "invoices":
				var number, contact, date string
				var total int64
				if err := rows.Scan(&id, &number, &contact, &total, &date); err != nil {
					rows.Close()
					serverError(w, err)
					return
				}
				out = append(out, map[string]any{"id": id, "number": number, "contact": contact, "total_cents": total, "date": date})
			case "bills":
				var reference, contact, date string
				var total int64
				if err := rows.Scan(&id, &reference, &contact, &total, &date); err != nil {
					rows.Close()
					serverError(w, err)
					return
				}
				out = append(out, map[string]any{"id": id, "reference": reference, "contact": contact, "total_cents": total, "date": date})
			case "items":
				var name, sku string
				var price int64
				if err := rows.Scan(&id, &name, &sku, &price); err != nil {
					rows.Close()
					serverError(w, err)
					return
				}
				out = append(out, map[string]any{"id": id, "name": name, "sku": sku, "selling_price_cents": price})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		result[q.key] = out
	}
	writeJSON(w, http.StatusOK, result)
}
