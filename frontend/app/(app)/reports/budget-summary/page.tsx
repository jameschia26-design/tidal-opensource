"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { account_id: number; code: string; name: string; type: string; months: { month: string; amount_cents: number }[]; total_cents: number };
export default function BudgetSummary() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<{ months: string[]; rows: Row[] } | null>(null);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true);
  try { setData(await api(`/v1/reports/budget-summary?org_id=${orgId}&from=${from}&to=${to}`)); }
  catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 const filters = <form onSubmit={run} className="inline-form">
  <div><label htmlFor="bs-from">From</label><DateInput id="bs-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div>
  <div><label htmlFor="bs-to">To</label><DateInput id="bs-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div>
  <button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Budget summary" description="Budgeted amounts per account per month. Set budgets under Settings → Budgets." filters={filters}>
  <p>Budget only: no actual, group subtotals or export. Use Budget variance for actual against budget.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Budget summary</caption><thead><tr><th>Code</th><th>Account</th>{data.months.map(m => <th key={m}>{m}</th>)}<th>Total</th></tr></thead><tbody>
   {data.rows.map(r => <tr key={r.account_id}><td>{r.code}</td><th scope="row">{r.name}</th>{r.months.map(m => <td key={m.month}>{m.amount_cents ? formatCents(m.amount_cents) : "—"}</td>)}<td>{formatCents(r.total_cents)}</td></tr>)}
  </tbody></table>{data.rows.length === 0 && <p className="notice">No budget amounts for this period.</p>}</div>}
 </ReportFrame>;
}
