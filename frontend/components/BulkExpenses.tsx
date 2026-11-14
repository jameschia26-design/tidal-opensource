"use client";
import { useRef, useState, type FormEvent } from "react";
import { api, type ApiError } from "@/lib/api";
import { parseCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { keyFor, clearKey } from "@/lib/payment-key";
import { cashBankAccounts } from "@/components/ListSelects";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";

type Row = { id: number; date: string; account: string; amount: string; customer: string; description: string; error: string };
type Props = { orgId: number; accounts: { id: number; code: string; name: string; type: string }[]; contacts: { id: number; name: string; role: string }[]; onSaved: () => void; onCancel: () => void };

// ponytail: one POST per row, each with its own retry-safe key. A failed row stays on screen with its error; saved rows leave.
// Add a single atomic bulk endpoint when all-or-nothing batches are needed.
export default function BulkExpenses({ orgId, accounts, contacts, onSaved, onCancel }: Props) {
  const today = todayISO();
  // Rows get their ids from a counter held in state, not a ref, so the first ten can be built during render.
  const [rows, setRows] = useState<Row[]>(() => Array.from({ length: 10 }, (_, i) => ({ id: i, date: today, account: "", amount: "", customer: "", description: "", error: "" })));
  const next = useRef(10);
  const blank = (): Row => ({ id: next.current++, date: today, account: "", amount: "", customer: "", description: "", error: "" });
  const [paidThrough, setPaidThrough] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const set = (id: number, patch: Partial<Row>) => setRows(rs => rs.map(r => r.id === id ? { ...r, ...patch } : r));
  const used = (r: Row) => !!(r.account || r.amount.trim() || r.customer || r.description.trim());

  async function save(e: FormEvent) {
    e.preventDefault(); if (busy) return;
    const todo = rows.filter(used);
    if (!paidThrough || todo.length === 0) { setMessage("Choose the paid-through account and fill at least one row."); return; }
    setBusy(true); setMessage("");
    let saved = 0, failed = 0;
    const left: Row[] = [];
    for (const r of todo) {
      const cents = parseCents(r.amount);
      if (!r.date || !r.account || cents == null || cents <= 0 || cents > 100000000000000 || !r.description.trim()) { failed++; left.push({ ...r, error: "Needs date, category, description and a valid amount." }); continue; }
      const payload = { org_id: orgId, expense_date: r.date, payee: "Bulk entry", description: r.description.trim(), expense_account_id: Number(r.account), paid_through_account_id: Number(paidThrough), customer_id: Number(r.customer), amount_cents: cents, receipt_reference: "", notes: "", items: [] };
      const body = JSON.stringify(payload), slot = `bulk-${orgId}-${r.id}`;
      try {
        await api("/v1/expenses", { method: "POST", body: JSON.stringify({ ...payload, idempotency_key: keyFor(slot, body) }) });
        clearKey(slot); saved++;
      } catch (err) { const x = err as ApiError; failed++; left.push({ ...r, error: messageFor(x.error, x.status) }); }
    }
    setRows(left.length ? left : Array.from({ length: 10 }, blank));
    setMessage(`${saved} saved, ${failed} need attention. Saved rows cannot be edited here.`);
    setBusy(false);
    if (saved) onSaved();
  }

  const expense = accounts.filter(a => a.type === "expense");
  return <form className="record-form" onSubmit={save}>
    <fieldset disabled={busy}><legend>Bulk add expenses</legend>
      <p className="section-help">SGD, no GST. Each row becomes its own expense. Projects, billable flag and reporting tags are not available in Tidal.</p>
      <label htmlFor="bulk-paid">Paid through (all rows)</label>
      <select id="bulk-paid" required value={paidThrough} onChange={e => setPaidThrough(e.target.value)}><option value="">Choose account</option>{cashBankAccounts(accounts).map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select>
      <div className="table-scroll"><table className="doc-table"><caption>Expense rows</caption><thead><tr><th>Date</th><th>Category</th><th>Description</th><th>Amount (SGD)</th><th>Customer</th><th>Status</th></tr></thead><tbody>
        {rows.map((r, i) => <tr key={r.id}>
          <td><DateInput aria-label={`Date, row ${i + 1}`} value={r.date} onChange={e => set(r.id, { date: e.target.value })}/></td>
          <td><select aria-label={`Category, row ${i + 1}`} value={r.account} onChange={e => set(r.id, { account: e.target.value })}><option value="">Choose</option>{expense.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></td>
          <td><input aria-label={`Description, row ${i + 1}`} maxLength={200} value={r.description} onChange={e => set(r.id, { description: e.target.value })} /></td>
          <td><input aria-label={`Amount, row ${i + 1}`} inputMode="decimal" value={r.amount} onChange={e => set(r.id, { amount: e.target.value })} /></td>
          <td><select aria-label={`Customer, row ${i + 1}`} value={r.customer} onChange={e => set(r.id, { customer: e.target.value })}><option value="">None</option>{contacts.filter(c => c.role !== "supplier").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></td>
          <td>{r.error && <span role="alert" className="form-error">{r.error}</span>}</td>
        </tr>)}
      </tbody></table></div>
      <button type="button" className="button-secondary" disabled={rows.length >= 100} onClick={() => setRows(rs => [...rs, blank()])}>Add more expenses</button>
    </fieldset>
    {message && <p role="status">{message}</p>}
    <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save expenses"}</button><button type="button" className="button-secondary" disabled={busy} onClick={onCancel}>Cancel</button></div>
  </form>;
}
