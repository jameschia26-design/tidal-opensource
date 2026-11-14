"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { project_id:number; project:string; contact:string; active:boolean; minutes:number; entries:number; labour_cents:number; expense_cents:number; billable_expense_cents:number; revenue_cents:number; gross_profit_cents:number; estimated_cost_cents:number; estimated_charge_cents:number; profit_bp:number };
type Data = { rows:Row[]; truncated:boolean };
export default function ProjectFinancials() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState(""), [status,setStatus]=useState("active");
 const [data,setData]=useState<Data|null>(null), [busy,setBusy]=useState(false), [error,setError]=useState("");
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null);setError("");setBusy(true); try { setData(await api<Data>(`/v1/reports/project-financials?org_id=${orgId}&from=${from}&to=${to}&status=${status}`)); } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="pf-status">Project status</label><select id="pf-status" disabled={busy} value={status} onChange={e=>{setStatus(e.target.value);setData(null);}}><option value="active">Active</option><option value="inactive">Inactive</option><option value="all">All</option></select></div><div><label htmlFor="pf-from">From</label><DateInput id="pf-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="pf-to">To</label><DateInput id="pf-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><button disabled={busy || !orgId}>{busy?"Running…":"Update"}</button></form>;
 return <ReportFrame title="Project financials" description="Time, labour cost and project expenses by project." filters={filters}>
  <p>Revenue comes from posted invoice lines assigned to the project. Labour cost uses each person&apos;s staff cost rate, so entries from people without a rate count as zero cost.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Project financials</caption><thead><tr><th>Project</th><th>Contact</th><th className="num">Entries</th><th className="num">Hours</th><th className="num">Labour cost</th><th className="num">Expenses</th><th className="num">Billable expenses</th><th className="num">Estimated cost</th><th className="num">Estimated charge</th><th className="num">Invoiced</th><th className="num">Uninvoiced</th><th className="num">Gross profit</th><th className="num">Profit %</th></tr></thead>
   <tbody>{data.rows.map(r=><tr key={r.project_id}><td>{r.project}{r.active ? "" : " (inactive)"}</td><td>{r.contact}</td><td className="num">{r.entries}</td><td className="num">{(r.minutes/60).toFixed(2)}</td><td className="num">{formatCents(r.labour_cents)}</td><td className="num">{formatCents(r.expense_cents)}</td><td className="num">{formatCents(r.billable_expense_cents)}</td><td className="num">{formatCents(r.revenue_cents)}</td><td className="num">{formatCents(r.estimated_cost_cents)}</td><td className="num">{formatCents(r.estimated_charge_cents)}</td><td className="num">{formatCents(r.revenue_cents)}</td><td className="num">{formatCents(Math.max(0, r.estimated_charge_cents - r.revenue_cents))}</td><td className="num">{formatCents(r.gross_profit_cents)}</td><td className="num">{r.profit_bp/100}%</td></tr>)}</tbody></table></div>)}
  {data?.truncated && <p role="status">Showing first 500 projects. Narrow the date range.</p>}
 </ReportFrame>;
}
