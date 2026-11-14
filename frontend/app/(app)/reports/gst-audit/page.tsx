"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { kind:string; number:string; date:string; contact:string; description:string; quantity:number; unit_price_cents:number; amount_cents:number; gst_code:string; gst_rate_bp:number; gst_cents:number };
type Data = { rows:Row[]; truncated:boolean };
export default function GstAudit() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState("");
 const [data,setData]=useState<Data|null>(null), [busy,setBusy]=useState(false), [error,setError]=useState("");
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null);setError("");setBusy(true); try { setData(await api<Data>(`/v1/reports/gst-audit?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="ga-from">From</label><DateInput id="ga-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="ga-to">To</label><DateInput id="ga-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><button disabled={busy || !orgId}>{busy?"Running…":"Update"}</button></form>;
 return <ReportFrame title="GST audit" description="Posted sales and purchase lines with their GST treatment, up to 366 days." filters={filters}>
  <p>Sales and purchases together, shown net, GST and gross. Standard-rated GST uses the rate for the document date. Amounts are Singapore dollars; foreign-currency source totals are translated at the document rate before they reach this report. GST on discounts, shipping and adjustments is not split out, and changes to a posted document are not listed as separate audit events.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>GST audit lines</caption><thead><tr><th>Type</th><th>Document</th><th>Date</th><th>Contact</th><th>Description</th><th className="num">Qty</th><th className="num">Net</th><th className="num">GST</th><th className="num">Gross</th><th>Code</th><th className="num">Rate</th></tr></thead>
   <tbody>{data.rows.map((r,i)=><tr key={i}><td>{r.kind === "purchase" ? "Purchase" : "Sale"}</td><td>{r.number}</td><td>{formatDate(r.date)}</td><td>{r.contact}</td><td>{r.description}</td><td className="num">{r.quantity}</td><td className="num">{formatCents(r.amount_cents)}</td><td className="num">{formatCents(r.gst_cents)}</td><td className="num">{formatCents(r.amount_cents + r.gst_cents)}</td><td>{r.gst_code}</td><td className="num">{r.gst_rate_bp/100}%</td></tr>)}</tbody></table></div>)}
  {data?.truncated && <p role="status">Showing first 5,000 lines. Narrow the date range.</p>}
 </ReportFrame>;
}
