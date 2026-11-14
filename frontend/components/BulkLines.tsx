"use client";
import { useState } from "react";
import { parseCents } from "@/lib/money";

export type BulkRow = { description: string; quantity: string; rate: string };
type Account = { id: number; code: string; name: string };

// Add many lines at once ("description, quantity, rate" per row) and set one account on every line.
// ponytail: text paste, not a catalog picker; add a picker when users ask for it.
export default function BulkLines({ id, room, onAdd, accounts, onAccount }: { id: string; room: number; onAdd: (rows: BulkRow[]) => void; accounts?: Account[]; onAccount?: (accountId: string) => void }) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  function add() {
    const rows: BulkRow[] = [];
    for (const raw of text.split("\n")) {
      if (!raw.trim()) continue;
      const [description = "", quantity = "1", rate = "0"] = raw.split(",").map(p => p.trim());
      if (!description || !/^\d{1,7}$/.test(quantity) || Number(quantity) < 1 || parseCents(rate) == null) { setError(`Line "${raw.slice(0, 40)}" needs: description, whole quantity, rate.`); return; }
      rows.push({ description, quantity, rate });
    }
    if (rows.length === 0) { setError("Type at least one line."); return; }
    if (rows.length > room) { setError(`Only ${room} more lines fit.`); return; }
    setError(""); onAdd(rows); setText("");
  }
  return <details><summary>Bulk actions and add items in bulk</summary>
    <label htmlFor={`${id}-bulk`}>Items in bulk (one per row: description, quantity, rate)</label>
    <textarea id={`${id}-bulk`} rows={4} value={text} onChange={e => setText(e.target.value)} placeholder={"Design work, 2, 150.00\nHosting, 1, 40"} />
    {error && <p role="alert" className="form-error">{error}</p>}
    <button type="button" className="button-secondary" onClick={add}>Add these lines</button>
    {accounts && onAccount && <div><label htmlFor={`${id}-all`}>Set account on all lines</label><select id={`${id}-all`} value="" onChange={e => { if (e.target.value) onAccount(e.target.value); }}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>}
  </details>;
}
