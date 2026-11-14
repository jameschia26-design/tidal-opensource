"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { journal_id:number; date:string; memo:string; account_code:string; account:string; debit_cents:number; credit_cents:number; rule:string };
type Check = { key:string; label:string; count:number; note:string };
type Data = { rows:Row[]; checks:Check[]; closed_through:string; truncated:boolean };
function priorRange(from:string,to:string){ const start=new Date(from+"T00:00:00Z"), end=new Date(to+"T00:00:00Z"); const days=Math.round((end.getTime()-start.getTime())/86400000)+1; const prevEnd=new Date(start.getTime()-86400000), prevStart=new Date(prevEnd.getTime()-(days-1)*86400000); return {from:prevStart.toISOString().slice(0,10),to:prevEnd.toISOString().slice(0,10)}; }
const rules: Record<string,string> = {
  control_account: "Manual journal on a control account",
  closed_period: "Dated in a closed period",
  control_account_in_closed_period: "Control account, closed period",
};
export default function GlExceptions() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState("");
 const [data,setData]=useState<Data|null>(null), [previous,setPrevious]=useState<Data|null>(null);
 const [compare,setCompare]=useState(false), [busy,setBusy]=useState(false), [error,setError]=useState("");
 const [showDebit,setShowDebit]=useState(true), [showCredit,setShowCredit]=useState(true);
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null);setPrevious(null);setError("");setBusy(true); try { setData(await api<Data>(`/v1/reports/gl-exceptions?org_id=${orgId}&from=${from}&to=${to}`)); if (compare) { const p=priorRange(from,to); setPrevious(await api<Data>(`/v1/reports/gl-exceptions?org_id=${orgId}&from=${p.from}&to=${p.to}`)); } } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 function exportCSV() {
  if (!data) return;
  const head=["Reason","Date","Journal","Memo","Account",...(showDebit?["Debit"]:[]),...(showCredit?["Credit"]:[])];
  const rows=data.rows.map(r=>[rules[r.rule]??r.rule,r.date,String(r.journal_id),r.memo,`${r.account_code} ${r.account}`,...(showDebit?[r.debit_cents/100]:[]),...(showCredit?[r.credit_cents/100]:[])]);
  const checks=data.checks.map(c=>[c.label,String(c.count),c.note,"","","",""]);
  const csv=[["Check","Count","Note","","","",""],...checks,[""],head,...rows].map(r=>r.map(v=>`"${String(v).replaceAll('"','""')}"`).join(",")).join("\n");
  const url=URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a=document.createElement("a"); a.href=url; a.download=`gl-exceptions-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
 }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="ge-from">From</label><DateInput id="ge-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="ge-to">To</label><DateInput id="ge-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><label className="check-label"><input type="checkbox" checked={compare} onChange={e=>setCompare(e.target.checked)} /> Compare previous period</label><details><summary>Columns</summary><label className="check-label"><input type="checkbox" checked={showDebit} onChange={e=>setShowDebit(e.target.checked)} /> Debit</label><label className="check-label"><input type="checkbox" checked={showCredit} onChange={e=>setShowCredit(e.target.checked)} /> Credit</label></details><button disabled={busy || !orgId}>{busy?"Running…":"Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="General ledger exceptions" description="Reconciliation checks plus journal lines that need a person to look at them. Compare runs the same checks for the equal-length period before it." filters={filters}>
  <p>Checks: journals that do not balance, one-line journals, duplicate document numbers, receivable and payable control accounts against their subledgers, manual lines on a control account, and entries dated in a closed period.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <><p>Closed through: {data.closed_through || "no period closed"}.</p>
   <div className="table-scroll"><table className="doc-table"><caption>Reconciliation checks</caption><thead><tr><th>Check</th><th className="num">Count</th>{previous&&compare&&<th className="num">Previous</th>}<th>What it means</th></tr></thead><tbody>
    {data.checks.map(c=><tr key={c.key}><th scope="row">{c.label}</th><td className="num">{c.count}</td>{previous&&compare&&<td className="num">{previous.checks.find(p=>p.key===c.key)?.count ?? 0}</td>}<td>{c.note}</td></tr>)}
   </tbody></table></div></>}
  {data && (data.rows.length === 0 ? <p>No exception lines.</p> : <div className="table-scroll"><table className="doc-table"><caption>Exception lines</caption><thead><tr><th>Reason</th><th>Date</th><th>Journal</th><th>Memo</th><th>Account</th>{showDebit&&<th className="num">Debit</th>}{showCredit&&<th className="num">Credit</th>}</tr></thead>
   <tbody>{data.rows.map((r,i)=><tr key={i}><td>{rules[r.rule] ?? r.rule}</td><td>{formatDate(r.date)}</td><td>{r.journal_id}</td><td>{r.memo}</td><td>{r.account_code} {r.account}</td>{showDebit&&<td className="num">{formatCents(r.debit_cents)}</td>}{showCredit&&<td className="num">{formatCents(r.credit_cents)}</td>}</tr>)}</tbody></table></div>)}
  {data?.truncated && <p role="status">Showing first 5,000 lines. Narrow the date range.</p>}
 </ReportFrame>;
}
