"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { kind:string; rate_bp:number; lines:number; taxable_cents:number; tax_cents:number };
type Data = { rows:Row[]; taxable_cents:number; tax_cents:number };
export default function SalesTax() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState("");
 const [data,setData]=useState<Data|null>(null), [busy,setBusy]=useState(false), [error,setError]=useState("");
 const [direction,setDirection]=useState("all");
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null);setError("");setBusy(true); try { setData(await api<Data>(`/v1/reports/sales-tax?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="st-from">From</label><DateInput id="st-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="st-to">To</label><DateInput id="st-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><div><label htmlFor="st-direction">Breakdown by</label><select id="st-direction" value={direction} onChange={e=>setDirection(e.target.value)}><option value="all">Sales and purchases</option><option value="sale">Sales only</option><option value="purchase">Purchases only</option></select></div><button disabled={busy || !orgId}>{busy?"Running…":"Update"}</button></form>;
 return <ReportFrame title="Sales tax summary" description="Posted sales and purchase lines totalled by direction and GST rate, up to 366 days." filters={filters}>
  <p>Sales and purchases, by rate, with a direction breakdown. Tax components are not shown because Tidal supports Singapore&apos;s single-rate GST rather than composite rates. Account-type grouping and filing periods are not included; use the GST worksheet for return figures.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Sales tax by rate</caption><thead><tr><th>Type</th><th>Rate</th><th className="num">Lines</th><th className="num">Taxable</th><th className="num">Tax</th></tr></thead>
   <tbody>{data.rows.filter(r=>direction==="all"||r.kind===direction).map((r,i)=><tr key={i}><td>{r.kind === "purchase" ? "Purchase" : "Sale"}</td><td>{r.rate_bp/100}%</td><td className="num">{r.lines}</td><td className="num">{formatCents(r.taxable_cents)}</td><td className="num">{formatCents(r.tax_cents)}</td></tr>)}
   <tr><th scope="row">Total</th><td/><td className="num">{formatCents(data.taxable_cents)}</td><td className="num">{formatCents(data.tax_cents)}</td></tr></tbody></table></div>)}
 </ReportFrame>;
}
