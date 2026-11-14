"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { date: string; payee: string; description: string; account_code: string; account: string; amount_cents: number; invoiced: boolean; billable: boolean };
type Data = { rows: Row[]; total_cents: number; truncated: boolean };
export default function ExpenseClaimDetail() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Data>(`/v1/reports/expense-claim-detail?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 function exportCSV() { if (!data) return; const csv=[["Date","Payee","Description","Account","Billable","Invoiced","Amount"],...data.rows.map(r=>[r.date,r.payee,r.description,`${r.account_code} ${r.account}`,r.billable?"Yes":"No",r.invoiced?"Yes":"No",(r.amount_cents/100).toFixed(2)]),["Total","","","","","",(data.total_cents/100).toFixed(2)]].map(r=>r.map(v=>`"${String(v).replaceAll('"','""')}"`).join(",")).join("\n"); const url=URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a=document.createElement("a"); a.href=url; a.download="expense-claims.csv"; a.click(); URL.revokeObjectURL(url); }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="ec-from">From</label><DateInput id="ec-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><div><label htmlFor="ec-to">To</label><DateInput id="ec-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Expense claim detail" description="Recorded spend by date, up to 366 days." filters={filters}>
  <p>Spend money entries are recorded as already paid, so there is no claim, approval or reimbursement state. Billable means it can be charged to a customer. Export copies the listed spend.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Expense claims</caption><thead><tr><th>Date</th><th>Payee</th><th>Description</th><th>Account</th><th>Billable</th><th>Invoiced</th><th className="num">Amount</th></tr></thead>
   <tbody>{data.rows.map((r, i) => <tr key={i}><td>{formatDate(r.date)}</td><td>{r.payee}</td><td>{r.description}</td><td>{r.account_code} {r.account}</td><td>{r.billable ? "Yes" : "No"}</td><td>{r.invoiced ? "Yes" : "No"}</td><td className="num">{formatCents(r.amount_cents)}</td></tr>)}
   <tr><th colSpan={6} scope="row">Total</th><td className="num">{formatCents(data.total_cents)}</td></tr></tbody></table></div>)}
  {data?.truncated && <p role="status">Showing first 500 rows. Narrow the date range.</p>}
 </ReportFrame>;
}
