"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import PageHeader from "@/components/PageHeader";
import InvoiceForm, { type Commercial } from "@/components/InvoiceForm";
import PdfButton from "@/components/PdfButton";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Quote = { commercial: Commercial; id: number; contact_id: number; customer_name: string; quote_date: string; expiry_date: string; reference: string; notes: string; terms: string; total_cents: number; status: string; invoice_id: number | null; seller_snapshot: { name?: string; legal_name?: string; uen?: string }; customer_snapshot: { name?: string; company_name?: string; uen?: string }; lines: { line_no: number; description: string; quantity: number; unit_price_cents: number; amount_cents: number; discount_basis_points?: number; item_id?: number }[] };
export default function QuotesPage() {
  const { selected } = useOrg();
  return <QuotesWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function QuotesWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Quote[] | null>(null);
  const [detail, setDetail] = useState<Quote | null>(null);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Quote | null>(null);
  const [query, setQuery] = useState("");
  const [stage, setStage] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [confirmStage, setConfirmStage] = useState("");
  const [invoiceDate, setInvoiceDate] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    if (orgId == null) return;
    const r = await api<{ quotes: Quote[] }>(`/v1/quotes?org_id=${orgId}`);
    if (!live.current) return;
    setRows(r.quotes); setDetail(prev => prev ? r.quotes.find(q => q.id === prev.id) ?? null : null);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  async function action(work: () => Promise<void>) {
    if (busy || !canWrite || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try { await work(); } catch (e) { if (live.current) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  const filtered = rows?.filter(q => (!stage || (stage === "invoiced" ? q.invoice_id != null : q.status === stage)) && (!from || q.quote_date >= from) && (!to || q.quote_date <= to) && `${q.customer_name} ${q.reference} ${q.id}`.toLowerCase().includes(query.toLowerCase()));
  return <>
    <PageHeader title="Quotes" description="Prepare customer quotes and turn accepted work into one invoice draft." />
    <p className="notice">Quotes can be saved with or without GST per line. Marking sent only records a status. Sending locks its contents.</p>
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && <>
      <div className="record-toolbar"><div><label htmlFor="quote-search">Search quotes</label><input id="quote-search" type="search" value={query} onChange={e => setQuery(e.target.value)} /></div><div><label htmlFor="quote-stage">Status</label><select id="quote-stage" value={stage} onChange={e => setStage(e.target.value)}><option value="">All statuses</option>{["draft", "sent", "accepted", "rejected", "invoiced"].map(s => <option key={s}>{s} ({s === "invoiced" ? rows?.filter(q => q.invoice_id != null).length ?? 0 : rows?.filter(q => q.status === s).length ?? 0})</option>)}</select></div><div><label htmlFor="quote-from">From</label><input id="quote-from" type="date" value={from} onChange={e => setFrom(e.target.value)} /></div><div><label htmlFor="quote-to">To</label><input id="quote-to" type="date" value={to} onChange={e => setTo(e.target.value)} /></div>{canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setEditing(null); setDetail(null); setConfirmStage(""); }}>New quote</button>}</div>
      {open && canWrite && orgId != null && <section><h2>{editing ? `Edit quote ${editing.id}` : "New quote"}</h2><InvoiceForm key={editing?.id ?? "new"} quote orgId={orgId} invoiceId={editing?.id} initial={editing ? { ...editing, invoice_date: editing.quote_date, due_date: editing.expiry_date } : undefined} onSaved={async () => { if (!live.current) return; setOpen(false); setEditing(null); setNotice("Quote draft saved."); await load(); }} /><button className="button-secondary" onClick={() => { setOpen(false); setEditing(null); }}>Close form</button></section>}
      {detail && <section className="record-form"><h2>Quote {detail.id}</h2>{orgId && <><Attachments key={detail.id} kind="quotes" id={detail.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras kind="quotes" id={detail.id} orgId={orgId} readOnly={!canWrite} /></>}{orgId != null && <PdfButton kind="quotes" id={detail.id} orgId={orgId} />}<dl className="detail-grid">{Object.entries({ Customer: detail.customer_name, Seller: detail.seller_snapshot.legal_name || detail.seller_snapshot.name || "Frozen when marked sent", Status: detail.status, Reference: detail.reference, Date: detail.quote_date, "Valid until": detail.expiry_date, Total: formatCents(detail.total_cents), Notes: detail.notes, Terms: detail.terms }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd className="long-text">{value || "Not provided"}</dd></div>)}</dl>
        <div className="table-scroll"><table className="doc-table"><caption>Quoted items</caption><thead><tr><th>Description</th><th>Quantity</th><th className="num">Unit price</th><th className="num">Discount</th><th className="num">Amount</th></tr></thead><tbody>{detail.lines.map(l => <tr key={l.line_no}><td>{l.description}</td><td>{l.quantity}</td><td className="num">{formatCents(l.unit_price_cents)}</td><td className="num">{l.discount_basis_points ? `${l.discount_basis_points / 100}%` : "—"}</td><td className="num">{formatCents(l.amount_cents)}</td></tr>)}</tbody></table></div>
        {canWrite && detail.status === "draft" && <div className="form-actions"><button disabled={busy} onClick={() => { setEditing(detail); setOpen(true); setDetail(null); }}>Edit draft</button><button disabled={busy} onClick={() => setConfirmStage("sent")}>Mark sent</button></div>}
        {canWrite && detail.status === "sent" && <div className="form-actions"><button disabled={busy} onClick={() => setConfirmStage("accepted")}>Record acceptance</button><button disabled={busy} className="button-secondary" onClick={() => setConfirmStage("rejected")}>Record rejection</button></div>}
        {confirmStage && canWrite && <div role="group" aria-label="Confirm quote status"><p>{confirmStage === "sent" ? "Mark sent and lock this quote? Send the document to your customer separately; no email is sent here." : `Record this quote as ${confirmStage}? This decision is final.`}</p><button disabled={busy} onClick={() => action(async () => { await api(`/v1/quotes/${detail.id}/stage`, { method: "POST", body: JSON.stringify({ org_id: orgId, stage: confirmStage }) }); if (!live.current) return; setConfirmStage(""); setNotice("Quote status saved. No email was sent."); await load(); })}>Confirm {confirmStage}</button><button className="button-secondary" disabled={busy} onClick={() => setConfirmStage("")}>Cancel</button></div>}
        {detail.invoice_id ? <p><Link href={`/sales/invoices/${detail.invoice_id}`}>Open linked invoice</Link></p> : canWrite && detail.status === "accepted" && <form onSubmit={e => { e.preventDefault(); action(async () => { const result = await api<{ invoice_id: number }>(`/v1/quotes/${detail.id}/convert`, { method: "POST", body: JSON.stringify({ org_id: orgId, invoice_date: invoiceDate, due_date: dueDate }) }); if (!live.current) return; setNotice(`Invoice draft ${result.invoice_id} created. Review and post it under Invoices; no income recorded yet.`); await load(); }); }}><fieldset disabled={busy}><legend>Create invoice draft</legend><div className="form-grid"><div><label htmlFor="quote-invoice-date">Invoice date</label><DateInput id="quote-invoice-date" required min={detail.quote_date} value={invoiceDate} onChange={e => setInvoiceDate(e.target.value)}/></div><div><label htmlFor="quote-due-date">Payment due date</label><DateInput id="quote-due-date" required min={invoiceDate || detail.quote_date} value={dueDate} onChange={e => setDueDate(e.target.value)}/></div></div><button disabled={busy}>Create invoice draft</button><p className="section-help">Repeated conversion opens the same draft, never a second invoice. Review customer details and dates before posting.</p></fieldset></form>}
        <button className="button-secondary" disabled={busy} onClick={() => { setDetail(null); setConfirmStage(""); }}>Close details</button>
      </section>}
      {rows === null && !error && <p role="status">Loading quotes…</p>}{filtered?.length === 0 && <p>No matching quotes.</p>}{!!filtered?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 quotes</caption><thead><tr><th>Quote</th><th>Customer</th><th>Reference</th><th>Valid until</th><th>Status</th><th className="num">Total</th><th>Actions</th></tr></thead><tbody>{filtered.map(q => <tr key={q.id}><th scope="row">{q.id}</th><td>{q.customer_name}</td><td>{q.reference || "—"}</td><td>{q.expiry_date}</td><td>{q.status}</td><td className="num">{formatCents(q.total_cents)}</td><td><button className="button-secondary" disabled={busy || open} onClick={() => { setDetail(q); setConfirmStage(""); setInvoiceDate(""); setDueDate(""); }}>View</button></td></tr>)}</tbody></table></div>}
    </>}
  </>;
}
