"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Side = { opening_cost_cents:number; closing_cost_cents:number; opening_accum_cents:number; closing_accum_cents:number };
type Asset = { id:number; name:string; acquired_on:string; disposed_on:string; cost_cents:number; opening_accum_cents:number; closing_accum_cents:number; opening_nbv_cents:number; closing_nbv_cents:number };
type Data = { cost_account:string; accum_account:string; register:Side; ledger:Side; difference:Side; assets:Asset[] };
function priorRange(from:string,to:string){ const start=new Date(from+"T00:00:00Z"), end=new Date(to+"T00:00:00Z"); const days=Math.round((end.getTime()-start.getTime())/86400000)+1; const prevEnd=new Date(start.getTime()-86400000), prevStart=new Date(prevEnd.getTime()-(days-1)*86400000); return {from:prevStart.toISOString().slice(0,10),to:prevEnd.toISOString().slice(0,10)}; }
export default function FixedAssetReconciliation() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState("");
 const [data,setData]=useState<Data|null>(null), [previous,setPrevious]=useState<Data|null>(null);
 const [compare,setCompare]=useState(false), [busy,setBusy]=useState(false), [error,setError]=useState("");
 const [showCost,setShowCost]=useState(true), [showAccum,setShowAccum]=useState(true), [showNBV,setShowNBV]=useState(true);
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null);setPrevious(null);setError("");setBusy(true); try { setData(await api<Data>(`/v1/reports/fixed-asset-reconciliation?org_id=${orgId}&from=${from}&to=${to}`)); if (compare) { const p=priorRange(from,to); setPrevious(await api<Data>(`/v1/reports/fixed-asset-reconciliation?org_id=${orgId}&from=${p.from}&to=${p.to}`)); } } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 function exportCSV() {
  if (!data) return;
  const head=["Asset","Acquired","Disposed",...(showCost?["Cost"]:[]),...(showAccum?["Opening accumulated","Closing accumulated"]:[]),...(showNBV?["Opening NBV","Closing NBV"]:[])];
  const rows=data.assets.map(a=>[a.name,a.acquired_on,a.disposed_on,...(showCost?[a.cost_cents/100]:[]),...(showAccum?[a.opening_accum_cents/100,a.closing_accum_cents/100]:[]),...(showNBV?[a.opening_nbv_cents/100,a.closing_nbv_cents/100]:[])]);
  const csv=[head,...rows].map(r=>r.map(v=>`"${v}"`).join(",")).join("\n");
  const url=URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a=document.createElement("a"); a.href=url; a.download=`fixed-assets-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
 }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="far-from">From</label><DateInput id="far-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="far-to">To</label><DateInput id="far-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><label className="check-label"><input type="checkbox" checked={compare} onChange={e=>setCompare(e.target.checked)} /> Compare previous period</label><details><summary>Columns</summary><label className="check-label"><input type="checkbox" checked={showCost} onChange={e=>setShowCost(e.target.checked)} /> Cost</label><label className="check-label"><input type="checkbox" checked={showAccum} onChange={e=>setShowAccum(e.target.checked)} /> Accumulated depreciation</label><label className="check-label"><input type="checkbox" checked={showNBV} onChange={e=>setShowNBV(e.target.checked)} /> Net book value</label></details><button disabled={busy || !orgId}>{busy?"Running…":"Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Fixed asset reconciliation" description="Register against the ledger accounts the register posts to. Compare shows each asset at the equal-length period before it." filters={filters}>
  <p>Each asset that existed during the period is listed with its cost and accumulated depreciation. The register has no per-asset cost account, so the ledger comparison uses account {data?.cost_account ?? "1500"} for cost and {data?.accum_account ?? "1590"} for accumulated depreciation, shown as the summary below the asset list.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <>
   <div className="table-scroll"><table className="doc-table"><caption>Assets</caption><thead><tr><th>Asset</th><th>Acquired</th><th>Disposed</th>{showCost&&<th className="num">Cost</th>}{previous&&compare&&<th className="num">Previous closing NBV</th>}{showAccum&&<><th className="num">Opening accumulated</th><th className="num">Closing accumulated</th></>}{showNBV&&<><th className="num">Opening NBV</th><th className="num">Closing NBV</th></>}</tr></thead><tbody>
    {data.assets.map(a=><tr key={a.id}><th scope="row">{a.name}</th><td>{a.acquired_on}</td><td>{a.disposed_on||"—"}</td>{showCost&&<td className="num">{formatCents(a.cost_cents)}</td>}{showAccum&&<><td className="num">{formatCents(a.opening_accum_cents)}</td><td className="num">{formatCents(a.closing_accum_cents)}</td></>}{showNBV&&<><td className="num">{formatCents(a.opening_nbv_cents)}</td><td className="num">{formatCents(a.closing_nbv_cents)}</td></>}{previous&&compare&&<td className="num">{formatCents(previous.assets.find(p=>p.id===a.id)?.closing_nbv_cents ?? 0)}</td>}</tr>)}
   </tbody></table></div>
   <div className="table-scroll"><table className="doc-table"><caption>Register and ledger summary</caption><thead><tr><th>Measure</th><th className="num">Register</th><th className="num">Ledger</th><th className="num">Difference</th></tr></thead>
    <tbody>
     <tr><td>Opening cost</td><td className="num">{formatCents(data.register.opening_cost_cents)}</td><td className="num">{formatCents(data.ledger.opening_cost_cents)}</td><td className="num">{formatCents(data.difference.opening_cost_cents)}</td></tr>
     <tr><td>Opening accumulated depreciation</td><td className="num">{formatCents(data.register.opening_accum_cents)}</td><td className="num">{formatCents(data.ledger.opening_accum_cents)}</td><td className="num">{formatCents(data.difference.opening_accum_cents)}</td></tr>
     <tr><td>Closing cost</td><td className="num">{formatCents(data.register.closing_cost_cents)}</td><td className="num">{formatCents(data.ledger.closing_cost_cents)}</td><td className="num">{formatCents(data.difference.closing_cost_cents)}</td></tr>
     <tr><td>Closing accumulated depreciation</td><td className="num">{formatCents(data.register.closing_accum_cents)}</td><td className="num">{formatCents(data.ledger.closing_accum_cents)}</td><td className="num">{formatCents(data.difference.closing_accum_cents)}</td></tr>
    </tbody></table></div>
   </>}
 </ReportFrame>;
}
