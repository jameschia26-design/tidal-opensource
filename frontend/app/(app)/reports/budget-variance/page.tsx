"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { account_id: number; code: string; name: string; type: string; actual_cents: number; budget_cents: number; variance_cents: number; variance_pct: number | null };
type P = { rows: Row[]; totals: Totals };
type Totals = { actual_revenue_cents: number; budget_revenue_cents: number; actual_expense_cents: number; budget_expense_cents: number; net_actual_cents: number; net_budget_cents: number; net_variance_cents: number; net_variance_pct: number | null };
export default function BudgetVariance() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<P | null>(null), [previous, setPrevious] = useState<P | null>(null);
 const [compare, setCompare] = useState(false);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true);
  try { setData(await api<P>(`/v1/reports/budget-variance?org_id=${orgId}&from=${from}&to=${to}`)); if (compare) { const days = Math.round((new Date(to+"T00:00:00Z").getTime() - new Date(from+"T00:00:00Z").getTime())/86400000)+1; const pe = new Date(new Date(from+"T00:00:00Z").getTime()-86400000), ps = new Date(pe.getTime()-(days-1)*86400000); setPrevious(await api<P>(`/v1/reports/budget-variance?org_id=${orgId}&from=${ps.toISOString().slice(0,10)}&to=${pe.toISOString().slice(0,10)}`)); } }
  catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 const filters = <form onSubmit={run} className="inline-form">
  <div><label htmlFor="bv-from">From</label><DateInput id="bv-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div>
  <div><label htmlFor="bv-to">To</label><DateInput id="bv-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div>
  <label className="check-label"><input type="checkbox" checked={compare} onChange={e => setCompare(e.target.checked)} /> Compare previous period</label><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={() => { if (!data) return; const head = ["Code","Account","Actual","Budget","Variance","Variance %",...(previous && compare ? ["Previous actual"] : [])]; const body = data.rows.map(r => [r.code, r.name, (r.actual_cents/100).toFixed(2), (r.budget_cents/100).toFixed(2), (r.variance_cents/100).toFixed(2), r.variance_pct == null ? "" : (r.variance_pct/100).toFixed(1)+"%", ...((previous && compare) ? [((previous.rows.find(q => q.account_id === r.account_id)?.actual_cents ?? 0) / 100).toFixed(2)] : [])]); const csv = [head, ...body].map(r => r.map(v => `"${String(v).replaceAll('"','""')}"`).join(",")).join("\n"); const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" })); const a = document.createElement("a"); a.href = url; a.download = `budget-variance-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url); }}>Export CSV</button></form>;
 const pct = (v: number | null) => v == null ? "—" : `${(v / 100).toFixed(1)}%`;
 return <ReportFrame title="Budget variance" description="Posted actual against budget per account. Revenue is credits less debits; expenses are debits less credits." filters={filters}>
  <p>Tick Compare previous period to fetch the equal-length period before it. Net profit is the one computed line; budgets never carry it.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Budget variance</caption><thead><tr><th>Code</th><th>Account</th><th>Actual</th><th>Budget</th><th>Variance</th><th>Variance %</th>{previous && compare && <th className="num">Previous actual</th>}</tr></thead><tbody>
   {data.rows.map(r => <tr key={r.account_id}><td>{r.code}</td><th scope="row">{r.name}</th><td>{formatCents(r.actual_cents)}</td><td>{formatCents(r.budget_cents)}</td><td>{formatCents(r.variance_cents)}</td><td>{pct(r.variance_pct)}</td>{previous && compare && <td className="num">{formatCents(previous.rows.find(q => q.account_id === r.account_id)?.actual_cents ?? 0)}</td>}</tr>)}
  </tbody><tfoot><tr><th scope="row" colSpan={2}>Net profit</th><td>{formatCents(data.totals.net_actual_cents)}</td><td>{formatCents(data.totals.net_budget_cents)}</td><td>{formatCents(data.totals.net_variance_cents)}</td><td>{pct(data.totals.net_variance_pct)}</td></tr></tfoot></table>
   {data.rows.length === 0 && <p className="notice">No actual or budget for this period.</p>}</div>}
 </ReportFrame>;
}
