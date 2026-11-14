"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Customer = { id: number; name: string; email: string; role: string };

// Lists the company's real customers (GET /api/v1/contacts?role=customer). Nothing is invented here.
export default function CustomerPicker({ orgId, value, onChange }: { orgId: number; value: string; onChange: (id: string) => void }) {
  const [rows, setRows] = useState<Customer[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    setRows(null);
    api<{ contacts: Customer[] }>(`/v1/contacts?role=customer&org_id=${orgId}`)
      .then((r) => { if (live) setRows(r.contacts); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);

  if (error) return <p role="alert" className="form-error">{error}</p>;
  if (!rows) return <p className="muted">Loading customers…</p>;
  if (rows.length === 0) return <p className="muted">No customers yet. <a href="/sales/customers">Add a customer first</a>.</p>;
  return (
    <>
      <label htmlFor="customer">Customer</label>
      <select id="customer" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">Choose a customer…</option>
        {rows.map((c) => <option key={c.id} value={String(c.id)}>{c.name}{c.email ? ` (${c.email})` : ""}</option>)}
      </select>
    </>
  );
}
