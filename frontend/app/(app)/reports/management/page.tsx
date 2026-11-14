"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Bucket = { cash_received_cents:number; cash_spent_cents:number; cash_surplus_cents:number; closing_bank_cents:number; income_cents:number; expenses_cents:number; profit_cents:number };
type Exec = { current: Bucket; previous: Bucket };
type Pnl = { revenue_cents:number; cost_of_sales_cents:number; gross_profit_cents:number; operating_expenses_cents:number; operating_profit_cents:number; other_income_cents:number; other_expense_cents:number; expense_cents:number; profit_cents:number };
type Pack = { executive: Exec; profit: Pnl };
const labels: Record<keyof Bucket,string> = { cash_received_cents:"Cash received", cash_spent_cents:"Cash spent", cash_surplus_cents:"Cash surplus", closing_bank_cents:"Closing bank balance", income_cents:"Income", expenses_cents:"Expenses", profit_cents:"Profit" };
export default function ManagementReport() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState("");
 const [data,setData]=useState<Pack|null>(null), [busy,setBusy]=useState(false), [error,setError]=useState("");
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null);setError("");setBusy(true); try {
   const [executive, profit] = await Promise.all([
     api<Exec>(`/v1/reports/executive-summary?org_id=${orgId}&from=${from}&to=${to}`),
     api<Pnl>(`/v1/reports/pnl?org_id=${orgId}&from=${from}&to=${to}`),
   ]);
   setData({ executive, profit });
 } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 function exportCSV() { if (!data) return; const rows: (string|number)[][] = [["Section","Item","This period","Prior period","Variance"]]; for (const k of Object.keys(labels) as (keyof Bucket)[]) rows.push(["Executive summary",labels[k],data.executive.current[k]/100,data.executive.previous[k]/100,(data.executive.current[k]-data.executive.previous[k])/100]); for (const [k,v] of Object.entries(data.profit)) rows.push(["Profit and loss",k,v/100,"",""]); const csv=rows.map(r=>r.map(v=>`"${String(v)}"`).join(",")).join("\n"); const url=URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a=document.createElement("a"); a.href=url; a.download=`management-report-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url); }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="mg-from">From</label><DateInput id="mg-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="mg-to">To</label><DateInput id="mg-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><button disabled={busy || !orgId}>{busy?"Running…":"Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Management report" description="Executive summary and profit and loss for one period." filters={filters}>
  <p>A fixed two-page pack: the executive summary figures with an equal-length prior period, then profit and loss. Export copies both pages.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <>
   <div className="table-scroll"><table className="doc-table"><caption>Executive summary</caption><thead><tr><th>Measure</th><th className="num">This period</th><th className="num">Prior period</th><th className="num">Variance</th></tr></thead>
    <tbody>{(Object.keys(labels) as (keyof Bucket)[]).map(k=><tr key={k}><td>{labels[k]}</td><td className="num">{formatCents(data.executive.current[k])}</td><td className="num">{formatCents(data.executive.previous[k])}</td><td className="num">{formatCents(data.executive.current[k]-data.executive.previous[k])}</td></tr>)}</tbody></table></div>
   <div className="table-scroll"><table className="doc-table"><caption>Profit and loss</caption><thead><tr><th>Line</th><th className="num">Amount</th></tr></thead>
    <tbody><tr><td>Revenue</td><td className="num">{formatCents(data.profit.revenue_cents)}</td></tr><tr><td>Cost of sales</td><td className="num">{formatCents(data.profit.cost_of_sales_cents)}</td></tr><tr><th scope="row">Gross profit</th><td className="num">{formatCents(data.profit.gross_profit_cents)}</td></tr><tr><td>Operating expenses</td><td className="num">{formatCents(data.profit.operating_expenses_cents)}</td></tr><tr><th scope="row">Operating profit</th><td className="num">{formatCents(data.profit.operating_profit_cents)}</td></tr><tr><td>Other income</td><td className="num">{formatCents(data.profit.other_income_cents)}</td></tr><tr><td>Other expense</td><td className="num">{formatCents(data.profit.other_expense_cents)}</td></tr><tr><th scope="row">Net profit</th><td className="num">{formatCents(data.profit.profit_cents)}</td></tr></tbody></table></div>
  </>}
 </ReportFrame>;
}
