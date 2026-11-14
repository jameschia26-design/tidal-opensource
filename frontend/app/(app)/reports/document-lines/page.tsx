"use client";
import { useEffect, useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { document_id: number; number: string; contact: string; date: string; due_date: string; line_no: number; description: string; item_code: string; quantity: number; unit_price_cents: number; discount_cents: number; amount_cents: number; gst_cents: number; gross_cents: number; invoice_total_cents: number; status: string; source: string };
export default function DocumentLines() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [kind,setKind]=useState("receivable"), [from,setFrom]=useState(""), [to,setTo]=useState(""), [contact,setContact]=useState("");
 const [contacts,setContacts]=useState<{id:number;name:string}[]>([]), [contactsError,setContactsError]=useState("");
 const [rows,setRows]=useState<Row[]|null>(null), [truncated,setTruncated]=useState(false), [busy,setBusy]=useState(false), [error,setError]=useState("");
 useEffect(()=>{if(!orgId)return;let live=true;setContacts([]);setContactsError("");api<{contacts:{id:number;name:string}[]}>(`/v1/contacts?org_id=${orgId}&role=${kind === "receivable" ? "customer" : "supplier"}`).then(r=>{if(live)setContacts(r.contacts);}).catch((e:ApiError)=>{if(live)setContactsError(messageFor(e.error,e.status));});return()=>{live=false;};},[orgId,kind]);
 async function run(e:React.FormEvent){e.preventDefault();if(!orgId||busy)return;setRows(null);setError("");setBusy(true);try{const data=await api<{rows:Row[];truncated:boolean}>(`/v1/reports/document-lines?org_id=${orgId}&kind=${kind}&from=${from}&to=${to}${contact ? `&contact_id=${encodeURIComponent(contact)}` : ""}`);setRows(data.rows);setTruncated(data.truncated);}catch(e){const err=e as ApiError;setError(messageFor(err.error,err.status));}finally{setBusy(false);}}
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="dl-kind">Report</label><select id="dl-kind" disabled={busy} value={kind} onChange={e=>{setKind(e.target.value);setContact("");setRows(null);}}><option value="receivable">Receivable invoice detail</option><option value="payable">Payable invoice detail</option></select></div><div><label htmlFor="dl-contact">Contact (optional)</label><select id="dl-contact" disabled={busy} value={contact} onChange={e=>{setContact(e.target.value);setRows(null);}}><option value="">All contacts</option>{contacts.map(c=><option key={c.id} value={c.id}>{c.name}</option>)}</select></div><div><label htmlFor="dl-from">From</label><DateInput id="dl-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setRows(null);}}/></div><div><label htmlFor="dl-to">To</label><DateInput id="dl-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setRows(null);}}/></div><button disabled={busy||!orgId}>{busy?"Running…":"Update"}</button></form>;
 return <ReportFrame title="Invoice and bill detail" description="Posted document lines by invoice or bill date, up to 366 days." filters={filters}>
 <p>Line amounts exclude document-level adjustments and may not sum to the document total. Tax is the line GST; gross is the line amount plus tax. Invoice total repeats the document total on every line. Source names the stored document origin. Credits, payments, grouping, column selection and export are not shown.</p>
 {!orgId&&<p>Choose a company first.</p>}{contactsError&&<p role="alert">Could not load contacts: {contactsError}</p>}{error&&<p role="alert">{error}</p>}
 {rows&&(rows.length===0?<p>Nothing to show here.</p>:<div className="table-scroll"><table className="doc-table"><caption>Posted document lines</caption><thead><tr><th>Number or reference</th><th>Contact</th><th>Date</th><th>Due</th><th>Line</th><th>Description</th><th>Item code</th><th>Quantity</th>{kind==="receivable"&&<th>Discount</th>}<th>Unit price</th><th>Amount</th><th>Tax</th><th>Gross</th><th>Invoice total</th><th>Status</th><th>Source</th></tr></thead><tbody>{rows.map(r=><tr key={`${r.document_id}-${r.line_no}`}><td>{r.number}</td><td>{r.contact}</td><td>{formatDate(r.date)}</td><td>{formatDate(r.due_date)}</td><td>{r.line_no}</td><td>{r.description}</td><td>{r.item_code || "—"}</td><td>{r.quantity}</td>{kind==="receivable"&&<td>{formatCents(r.discount_cents)}</td>}<td>{formatCents(r.unit_price_cents)}</td><td>{formatCents(r.amount_cents)}</td><td>{formatCents(r.gst_cents)}</td><td>{formatCents(r.gross_cents)}</td><td>{formatCents(r.invoice_total_cents)}</td><td>{r.status}</td><td>{r.source}</td></tr>)}</tbody></table></div>)}
 {rows&&truncated&&<p role="status">Showing first 5,000 lines. Narrow the date range.</p>}
 </ReportFrame>;
}
