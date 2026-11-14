"use client";
import { useEffect, useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { kind:string; count:number; amount_cents:number };
type Data = { contact_id:number; contact:string; rows:Row[]; net_cents:number };
export default function ContactTransactions() { const orgId=useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({orgId}:{orgId:number|null}) {
 const [from,setFrom]=useState(""), [to,setTo]=useState(""), [contact,setContact]=useState("");
 const [contacts,setContacts]=useState<{id:number;name:string}[]>([]), [contactsError,setContactsError]=useState("");
 const [data,setData]=useState<Data|null>(null), [busy,setBusy]=useState(false), [error,setError]=useState("");
 useEffect(()=>{ if(!orgId) return; let live=true; setContacts([]); setContactsError(""); api<{contacts:{id:number;name:string}[]}>(`/v1/contacts?org_id=${orgId}`).then(r=>{if(live)setContacts(r.contacts);}).catch((e:ApiError)=>{if(live)setContactsError(messageFor(e.error,e.status));}); return ()=>{live=false;}; },[orgId]);
 async function run(e:React.FormEvent) { e.preventDefault(); if (!orgId || busy || !contact) return; setData(null);setError("");setBusy(true); try { setData(await api<Data>(`/v1/reports/contact-transactions?org_id=${orgId}&contact_id=${contact}&from=${from}&to=${to}`)); } catch (e) { const err=e as ApiError;setError(messageFor(err.error,err.status)); } finally {setBusy(false);} }
 function exportCSV() { if (!data) return; const csv=[["Type","Count","Amount"],...data.rows.map(r=>[r.kind,String(r.count),(r.amount_cents/100).toFixed(2)]),["Net","",(data.net_cents/100).toFixed(2)]].map(r=>r.map(v=>`"${String(v).replaceAll('"','""')}"`).join(",")).join("\n"); const url=URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a=document.createElement("a"); a.href=url; a.download="contact-transactions.csv"; a.click(); URL.revokeObjectURL(url); }
 const filters=<form onSubmit={run} className="inline-form"><div><label htmlFor="ctx-contact">Contact</label><select id="ctx-contact" required disabled={busy} value={contact} onChange={e=>{setContact(e.target.value);setData(null);}}><option value="">Choose a contact…</option>{contacts.map(c=><option key={c.id} value={c.id}>{c.name}</option>)}</select></div><div><label htmlFor="ctx-from">From</label><DateInput id="ctx-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setData(null);}}/></div><div><label htmlFor="ctx-to">To</label><DateInput id="ctx-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setData(null);}}/></div><button disabled={busy || !orgId || !contact}>{busy?"Running…":"Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Contact transactions summary" description="One contact's sales and purchase totals for a period." filters={filters}>
  <p>Totals by kind only: invoices, payments, credit notes, bills and bill payments. No per-document rows here — use the customer or supplier activity report for those. Opening balances and unapplied credits are not included. Export copies the summary.</p>
  {!orgId && <p>Choose a company first.</p>}{contactsError && <p role="alert">Could not load contacts: {contactsError}</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing to show for {data.contact} in this period.</p> : <div className="table-scroll"><table className="doc-table"><caption>{data.contact}</caption><thead><tr><th>Type</th><th className="num">Count</th><th className="num">Amount</th></tr></thead>
   <tbody>{data.rows.map(r=><tr key={r.kind}><td>{r.kind}</td><td className="num">{r.count}</td><td className="num">{formatCents(r.amount_cents)}</td></tr>)}<tr><th scope="row">Net</th><td/><td className="num">{formatCents(data.net_cents)}</td></tr></tbody></table></div>)}
 </ReportFrame>;
}
