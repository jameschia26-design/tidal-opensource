"use client";
import { useCallback, useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Receipt = { id: number; filename: string; size: number; added_on: string };
type Expense = { id: number; description: string; payee: string; expense_date: string; amount_cents: number; billable: boolean; customer_id: number; invoice_id: number };
type Contact = { id: number; name: string };
export default function ReceiptsInboxPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const [receipts, setReceipts] = useState<Receipt[] | null>(null);
  const [expenses, setExpenses] = useState<Expense[]>([]);
  const [customers, setCustomers] = useState<Contact[]>([]);
  const [chosen, setChosen] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const load = useCallback(async () => {
    if (orgId == null) return;
    const [r, e, c] = await Promise.all([
      api<{ receipts: Receipt[] }>(`/v1/receipts/unlinked?org_id=${orgId}`),
      api<{ expenses: Expense[] }>(`/v1/expenses?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    ]);
    setReceipts(r.receipts);
    setExpenses(e.expenses);
    setCustomers(c.contacts);
  }, [orgId]);
  useEffect(() => {
    let live = true;
    void load().catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [load]);
  async function link(receipt: Receipt) {
    const expenseId = Number(chosen[receipt.id]);
    if (!orgId || !expenseId || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/documents/${receipt.id}/receipt-link`, { method: "POST", body: JSON.stringify({ org_id: orgId, expense_id: expenseId }) });
      setNotice(`${receipt.filename} linked to expense.`);
      await load();
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  const eligible = expenses.filter(e => e.invoice_id === 0);
  const customerName = (id: number) => customers.find(c => c.id === id)?.name ?? "Customer not found";
  return <>
    <PageHeader title="Receipts Inbox" description="Link uploaded receipt files to recorded expenses." />
    <p className="notice">Upload files in Documents first. Linking a receipt does not change or post the expense. Auto-reading receipt details is not available.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {receipts?.length === 0 && <p>No unlinked receipts. Upload receipt files in Documents.</p>}
    {!!receipts?.length && <div className="table-scroll"><table className="doc-table"><caption>Unlinked receipts (latest 200)</caption><thead><tr><th>File</th><th>Size</th><th>Added</th><th>Match expense</th>{canWrite && <th>Action</th>}</tr></thead><tbody>{receipts.map(r => <tr key={r.id}>
      <td><a href={`/api/v1/documents/${r.id}?org_id=${orgId}`}>{r.filename}</a></td>
      <td>{Math.ceil(r.size / 1024)} KB</td><td>{r.added_on}</td>
      <td>{canWrite ? <select aria-label={`Expense for ${r.filename}`} value={chosen[r.id] ?? ""} disabled={busy} onChange={e => setChosen(prev => ({ ...prev, [r.id]: e.target.value }))}><option value="">Choose expense</option>{eligible.map(e => <option key={e.id} value={e.id}>{e.expense_date} — {e.payee}: {e.description} — {customerName(e.customer_id)}</option>)}</select> : <span>Ask a company editor to link this receipt.</span>}</td>
      {canWrite && <td><button disabled={busy || !chosen[r.id]} onClick={() => void link(r)}>Link receipt</button></td>}
    </tr>)}</tbody></table></div>}
    {receipts?.length && eligible.length === 0 && <p>No unlinked expenses available. Record an expense before matching a receipt.</p>}
  </>;
}
