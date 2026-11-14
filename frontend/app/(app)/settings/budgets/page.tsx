"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents, parseCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";

type Account = { id: number; code: string; name: string; type: string };
export default function Budgets() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [month, setMonth] = useState(new Date().toISOString().slice(0, 7) + "-01");
 const [accounts, setAccounts] = useState<Account[]>([]);
 const [amounts, setAmounts] = useState<Record<number, string>>({});
 const [busy, setBusy] = useState(false), [saved, setSaved] = useState(""), [error, setError] = useState("");
 useEffect(() => {
  if (!orgId) return;
  let live = true;
  Promise.all([api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`), api<{ rows: { account_id: number; amount_cents: number }[] }>(`/v1/budgets?org_id=${orgId}&from=${month}&to=${month}`)])
   .then(([ledger, budget]) => { if (!live) return; setAccounts(ledger.accounts.filter(a => a.type === "revenue" || a.type === "expense")); const next: Record<number, string> = {}; for (const r of budget.rows) next[r.account_id] = (r.amount_cents / 100).toFixed(2); setAmounts(next); })
   .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
  return () => { live = false; };
 }, [orgId, month]);
 async function save(e: FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setBusy(true); setError(""); setSaved("");
  const lines = Object.entries(amounts).filter(([, v]) => v.trim() !== "").map(([id, v]) => ({ account_id: Number(id), amount_cents: parseCents(v) ?? NaN }));
  if (lines.some(l => Number.isNaN(l.amount_cents))) { setError("Enter amounts with up to two decimal places."); setBusy(false); return; }
  try { await api("/v1/budgets", { method: "PUT", body: JSON.stringify({ org_id: orgId, period_month: month, lines }) }); setSaved("Budget saved."); }
  catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 return <><PageHeader title="Budgets" description="One amount per account per month. Budget reports read these amounts." />
  {!orgId && <p className="notice">Choose a company first.</p>}
  {orgId && <><form onSubmit={save}><div className="inline-form"><div><label htmlFor="budget-month">Month</label><input id="budget-month" type="month" disabled={busy} value={month.slice(0, 7)} onChange={e => setMonth(`${e.target.value}-01`)} /></div></div>
   {error && <p role="alert" className="form-error">{error}</p>}{saved && <p role="status">{saved}</p>}
   <div className="table-scroll"><table className="doc-table"><caption>Budget for {month.slice(0, 7)}</caption><thead><tr><th>Code</th><th>Account</th><th>Type</th><th>Amount</th></tr></thead><tbody>
    {accounts.map(a => <tr key={a.id}><td>{a.code}</td><th scope="row">{a.name}</th><td>{a.type}</td><td><label className="sr-only" htmlFor={`budget-${a.id}`}>Amount for {a.name}</label><input id={`budget-${a.id}`} inputMode="decimal" placeholder="0.00" disabled={busy} value={amounts[a.id] ?? ""} onChange={e => setAmounts({ ...amounts, [a.id]: e.target.value })} /></td></tr>)}
   </tbody></table></div>
   <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save budget"}</button></div>
   <p className="section-help">Blank amounts are left unchanged; zero clears an account&rsquo;s month. Only revenue and expense accounts take budgets. Current total: {formatCents(Object.values(amounts).reduce((sum, v) => sum + (parseCents(v) ?? 0), 0))}.</p>
  </form></>}
 </>;
}
