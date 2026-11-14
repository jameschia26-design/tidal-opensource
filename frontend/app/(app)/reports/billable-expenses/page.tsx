"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { formatDate } from "@/lib/dates";

type Row = { date: string; payee: string; description: string; account_code: string; account: string; amount_cents: number; customer: string | null };
type Data = { rows: Row[]; total_cents: number; truncated: boolean };
export default function BillableExpenses() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run() { if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Data>(`/v1/reports/billable-expenses-outstanding?org_id=${orgId}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 function exportCSV() { if (!data) return; const csv = [["Date","Payee","Description","Account","Customer","Amount"], ...data.rows.map(r => [r.date,r.payee,r.description,`${r.account_code} ${r.account}`,r.customer ?? "Not set",(r.amount_cents/100).toFixed(2)])].map(r => r.map(v => `"${String(v).replaceAll('"','""')}"`).join(",")).join("\n"); const url = URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a = document.createElement("a"); a.href=url; a.download="billable-expenses.csv"; a.click(); URL.revokeObjectURL(url); }
 const filters = <form onSubmit={e => { e.preventDefault(); run(); }} className="inline-form"><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Billable expenses — outstanding" description="Billable spend not yet charged to a customer on an invoice." filters={filters}>
  <p>Billable spend that is linked to an invoice leaves this list as soon as the draft invoice is created, before it is posted. Export copies the visible columns.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing outstanding.</p> : <div className="table-scroll"><table className="doc-table"><caption>Outstanding billable expenses</caption><thead><tr><th>Date</th><th>Payee</th><th>Description</th><th>Account</th><th>Customer</th><th className="num">Amount</th></tr></thead>
   <tbody>{data.rows.map((r, i) => <tr key={i}><td>{formatDate(r.date)}</td><td>{r.payee}</td><td>{r.description}</td><td>{r.account_code} {r.account}</td><td>{r.customer ?? "Not set"}</td><td className="num">{formatCents(r.amount_cents)}</td></tr>)}
   <tr><th colSpan={5} scope="row">Total</th><td className="num">{formatCents(data.total_cents)}</td></tr></tbody></table></div>)}
  {data?.truncated && <p role="status">Showing first 500 rows.</p>}
 </ReportFrame>;
}
