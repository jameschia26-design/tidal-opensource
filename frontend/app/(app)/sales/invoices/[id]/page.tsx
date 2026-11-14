"use client";
import { useEffect, useRef, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import PageHeader from "@/components/PageHeader";
import PdfButton from "@/components/PdfButton";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import CreditNotes from "@/components/CreditNotes";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import InvoiceForm, { type Commercial } from "@/components/InvoiceForm";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { keyFor, clearKey } from "@/lib/payment-key";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Line = { line_no: number; description: string; quantity: number; unit_price_cents: number; amount_cents: number; revenue_account_id?: number; project_id?: number; discount_basis_points?: number; gst_code?: string; gst_cents?: number };
type Payment = { id: number; amount_cents: number; payment_date: string; journal_id: number };
type Address = { attention?: string; line1?: string; line2?: string; city?: string; region?: string; postal_code?: string; country?: string };
type Party = { name?: string; legal_name?: string; company_name?: string; uen?: string; contact_email?: string; email?: string; phone?: string; address?: Address; billing_address?: Address };
type Invoice = { amounts_are?: string; commercial: Commercial; id: number; number: number | null; document_number: string; lifecycle_status: string; status: string; contact_id: number; invoice_date: string; due_date: string; total_cents: number; balance_cents: number; reference: string; notes: string; terms: string; seller_snapshot: Party; customer_snapshot: Party };

function PartyDetails({ title, party }: { title: string; party: Party }) {
  const address = party.address ?? party.billing_address;
  return <section><h2>{title}</h2><p><strong>{party.legal_name || party.company_name || party.name || "Not recorded"}</strong></p>{party.company_name && party.name !== party.company_name && <p>{party.name}</p>}{party.uen && <p>UEN: {party.uen}</p>}{address && <p className="long-text">{[address.attention, address.line1, address.line2, address.city, address.region, address.postal_code, address.country].filter(Boolean).join("\n")}</p>}{(party.contact_email || party.email) && <p className="long-text">{party.contact_email || party.email}</p>}{party.phone && <p>{party.phone}</p>}</section>;
}
type Detail = { invoice: Invoice; lines: Line[]; payments: Payment[] };

const DATE = /^\d{4}-\d{2}-\d{2}$/;

export default function InvoiceDetail() {
  const { id } = useParams<{ id: string }>();
  const { selected } = useOrg();
  if (!selected) return <p className="notice">Choose a company first.</p>;
  return <InvoiceWorkspace key={`${selected.id}-${id}-${selected.role}`} id={id} orgId={selected.id} canWrite={selected.role !== "viewer"} />;
}

function InvoiceWorkspace({ id, orgId, canWrite }: { id: string; orgId: number; canWrite: boolean }) {
  const active = useRef(true);
  const [editing, setEditing] = useState(false);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [projects, setProjects] = useState<Record<number, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [amount, setAmount] = useState("");
  const [emailTo, setEmailTo] = useState("");
  const [payDate, setPayDate] = useState("");
  const [voidDate, setVoidDate] = useState(() => new Date().toISOString().slice(0, 10));

  async function load() {
    if (orgId == null) return;
    try {
      const result = await api<Detail>(`/v1/invoices/${encodeURIComponent(id)}?org_id=${orgId}`);
      if (!active.current) return;
      setDetail(result);
      setError(null);
      // Line projects are stored by id; the names come from the customer's project list.
      api<{ projects: { id: number; name: string }[] }>(`/v1/projects?org_id=${orgId}&contact_id=${result.invoice.contact_id}`)
        .then(r => { if (active.current) setProjects(Object.fromEntries(r.projects.map(p => [p.id, p.name]))); })
        .catch(() => { /* A missing name falls back to the id. */ });
    } catch (e) {
      setError(messageFor((e as ApiError).error, (e as ApiError).status));
    }
  }

  useEffect(() => {
    active.current = true;
    void load();
    return () => { active.current = false; };
    /* eslint-disable-next-line react-hooks/exhaustive-deps */
  }, [orgId, id]);

  async function submitForApproval() {
    if (orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try { await api(`/v1/invoices/${encodeURIComponent(id)}/submit`, { method: "POST", body: JSON.stringify({ org_id: orgId }) }); setNotice("Invoice sent for approval. Posting it is the approval."); await load(); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function approveSend() {
    if (orgId == null) return;
    parseCents(amount);
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ number: number; emailed: boolean; to?: string; email_error?: string }>(`/v1/invoices/${encodeURIComponent(id)}/approve-and-email`, { method: "POST", body: JSON.stringify({ org_id: orgId, to: emailTo.trim() }) });
      setNotice(r.emailed ? `Invoice ${r.number} posted and emailed to ${r.to}.` : `Invoice ${r.number} posted, but the email could not be sent.`);
      await load();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function post() {
    if (orgId == null) return;
    setBusy(true);
    setNotice(null);
    try {
      await api(`/v1/invoices/${encodeURIComponent(id)}/post?org_id=${orgId}`, { method: "POST" });
      setNotice("Invoice posted. Its number is now fixed.");
      await load();
    } catch (e) {
      setError(messageFor((e as ApiError).error, (e as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  async function pay(e: React.FormEvent) {
    e.preventDefault();
    if (orgId == null) return;
    const cents = parseCents(amount);
    if (cents === null || cents <= 0 || !DATE.test(payDate)) {
      setError("Enter an amount above zero and a payment date.");
      return;
    }
    const payload = { org_id: orgId, amount_cents: cents, payment_date: payDate };
    const slot = `${id}`;
    const key = keyFor(slot, JSON.stringify(payload));
    setBusy(true);
    setNotice(null);
    setError(null);
    try {
      await api(`/v1/invoices/${encodeURIComponent(id)}/payments`, {
        method: "POST",
        body: JSON.stringify({ ...payload, idempotency_key: key }),
      });
      clearKey(slot);
      setAmount("");
      setPayDate("");
      setNotice("Payment recorded.");
      await load();
    } catch (err) {
      // Keep the key on any error: the server may have saved the payment before the reply was lost.
      // Only a confirmed save clears it. A 4xx such as overpayment is final, so the key is cleared there too.
      const e2 = err as ApiError;
      if (e2.status >= 400 && e2.status < 500 && e2.status !== 409 && e2.status !== 408 && e2.status !== 429) clearKey(slot);
      setError(messageFor(e2.error, e2.status));
    } finally {
      setBusy(false);
    }
  }

  async function voidInvoice() {
    if (orgId == null) return;
    setBusy(true);
    setNotice(null);
    setError(null);
    try {
      await api(`/v1/invoices/${encodeURIComponent(id)}/void`, { method: "POST", body: JSON.stringify({ org_id: orgId, reversal_date: voidDate }) });
      setNotice("Invoice voided. A reversing journal was posted and the invoice stays in the history.");
      await load();
    } catch (e) {
      setError(messageFor((e as ApiError).error, (e as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  const inv = detail?.invoice;
  const isDraft = inv?.status === "draft";
  const isPosted = inv?.status === "posted";

  return (
    <>
      <p className="small"><Link href="/sales/invoices">Back to invoices</Link></p>
      <PageHeader title={inv ? (inv.status === "draft" ? `Draft ${inv.document_number}` : `Invoice ${inv.document_number}`) : "Invoice"} description={inv ? `Status: ${inv.lifecycle_status || inv.status}` : undefined} actions={inv && orgId != null ? <><PdfButton kind="invoices" id={inv.id} orgId={orgId} />{canWrite && detail?.lines[0] && <Link className="button-secondary" href={`/sales/recurring?${new URLSearchParams({ customer: String(inv.contact_id), description: detail.lines[0].description, quantity: String(detail.lines[0].quantity), price: (detail.lines[0].unit_price_cents / 100).toFixed(2), gst_code: detail.lines[0].gst_code ?? "NONE", amounts_are: inv.amounts_are ?? "exclusive" })}`}>Make recurring</Link>}</> : undefined} />
      <PreviewNotice>Amounts come from the server. Payments reduce the balance; there is no reversal in this release.</PreviewNotice>
      {notice && <p role="status" className="muted">{notice}</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {!detail && !error && <p className="muted">Loading…</p>}

      {detail && inv && (
        <>
          {isPosted && <><p className="muted">Business and customer details below were saved when this invoice was posted.</p><div className="detail-grid"><PartyDetails title="From" party={inv.seller_snapshot} /><PartyDetails title="Bill to" party={inv.customer_snapshot} /></div></>}
          {isDraft && canWrite && <div className="action-block"><button className="button-secondary" disabled={busy} type="button" onClick={() => setEditing(!editing)}>{editing ? "Cancel editing" : "Edit draft"}</button>{editing && <InvoiceForm orgId={orgId} invoiceId={inv.id} initial={{ commercial: inv.commercial, amounts_are: inv.amounts_are, document_number: inv.document_number, contact_id: inv.contact_id, invoice_date: inv.invoice_date, due_date: inv.due_date, reference: inv.reference, notes: inv.notes, terms: inv.terms, lines: detail.lines }} onSaved={async () => { setEditing(false); setNotice("Draft saved."); await load(); }} />}</div>}
          <dl className="facts">
            <div><dt>Number</dt><dd>{inv.document_number || inv.number || "—"}</dd></div>
            <div><dt>Status</dt><dd>{inv.lifecycle_status || inv.status}</dd></div>
            {inv.reference && <div><dt>Reference / purchase order</dt><dd>{inv.reference}</dd></div>}
            <div><dt>Customer ID</dt><dd>{inv.contact_id}</dd></div>
            <div><dt>Invoice date</dt><dd>{formatDate(inv.invoice_date)}</dd></div>
            <div><dt>Due date</dt><dd>{formatDate(inv.due_date)}</dd></div>
            <div><dt>Total</dt><dd>{formatCents(inv.total_cents)}</dd></div>
            <div><dt>Balance</dt><dd>{formatCents(inv.balance_cents)}</dd></div>
          </dl>

          <div className="table-scroll"><table className="doc-table">
            <caption className="sr-only">Invoice lines</caption>
            <thead><tr><th scope="col">No.</th><th scope="col">Description</th><th scope="col" className="num">Qty</th><th scope="col" className="num">Unit price</th>{detail.lines.some(l => (l.discount_basis_points ?? 0) > 0) && <th scope="col" className="num">Disc. %</th>}{detail.lines.some(l => (l.project_id ?? 0) > 0) && <th scope="col">Project</th>}<th scope="col" className="num">Amount</th><th scope="col" className="num">Tax</th></tr></thead>
            <tbody>
              {detail.lines.map((l) => (
                <tr key={l.line_no}>
                  <td>{l.line_no}</td>
                  <td>{l.description}</td>
                  <td className="num">{l.quantity}</td>
                  <td className="num">{formatCents(l.unit_price_cents)}</td>
                  {detail.lines.some(x => (x.discount_basis_points ?? 0) > 0) && <td className="num">{(l.discount_basis_points ?? 0) > 0 ? `${(l.discount_basis_points ?? 0) / 100}%` : "—"}</td>}
                  {detail.lines.some(x => (x.project_id ?? 0) > 0) && <td>{(l.project_id ?? 0) > 0 ? projects[l.project_id ?? 0] ?? `Project ${l.project_id}` : "—"}</td>}
                  <td className="num">{formatCents(l.amount_cents)}</td>
                  <td className="num">{(l.gst_cents ?? 0) > 0 ? formatCents(l.gst_cents ?? 0) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table></div>

          <Attachments kind="invoices" id={inv.id} orgId={orgId} readOnly={!canWrite} /><Extras kind="invoices" id={inv.id} orgId={orgId} readOnly={!canWrite} />
          {inv.notes && <section><h2>Notes</h2><p className="long-text">{inv.notes}</p></section>}
          {inv.terms && <section><h2>Payment terms / instructions</h2><p className="long-text">{inv.terms}</p></section>}
          {isDraft && canWrite && !editing && (
            <div className="action-block">
              <h2>Post invoice</h2>
              <p className="muted">Posting fixes the number and records it in the ledger. A posted invoice cannot be edited. Approve and email posts the invoice and sends it to the customer.</p>
              <div className="form-grid"><div><label htmlFor="send-to">Email to <span className="field-optional">Optional</span></label><input id="send-to" type="email" value={emailTo} onChange={e => setEmailTo(e.target.value)} placeholder="Customer email" /></div></div>
              <button type="button" onClick={post} disabled={busy || orgId == null}>{busy ? "Posting…" : "Post invoice"}</button>{" "}
              <button type="button" className="button-secondary" onClick={approveSend} disabled={busy || orgId == null}>Approve &amp; email</button>{" "}
              <button type="button" className="button-secondary" onClick={submitForApproval} disabled={busy || orgId == null}>Submit for approval</button>
            </div>
          )}

          {isPosted && canWrite && (
            <div className="action-block">
              <h2>Email this invoice</h2>
              <div className="form-grid"><div><label htmlFor="send-to-posted">Email to <span className="field-optional">Optional</span></label><input id="send-to-posted" type="email" value={emailTo} onChange={e => setEmailTo(e.target.value)} placeholder="Customer email" /></div></div>
              <button type="button" className="button-secondary" onClick={approveSend} disabled={busy || orgId == null}>Send invoice email</button>
            </div>
          )}

          {isPosted && canWrite && inv.balance_cents > 0 && (
            <div className="action-block">
              <h2>Record payment</h2>
              <p className="notice">This records money already received. Tidal does not collect or send money.</p>
              <form onSubmit={pay} className="inline-form" noValidate>
                <div><label htmlFor="pay-amt">Amount (SGD)</label><input id="pay-amt" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} /></div>
                <div><label htmlFor="pay-date">Payment date</label><DateInput id="pay-date" value={payDate} onChange={(e) => setPayDate(e.target.value)}/></div>
                <button type="submit" disabled={busy}>{busy ? "Recording…" : "Record payment"}</button>
              </form>
            </div>
          )}

          {isPosted && canWrite && inv.lifecycle_status !== "paid" && (
            <div className="action-block">
              <h2>Void invoice</h2>
              <p className="notice">Voiding posts a dated reversing journal and keeps this invoice in the lists and audit history. It cannot be undone. An invoice with payments or credits must have those reversed first.</p>
              <div className="form-grid"><div><label htmlFor="void-date">Void date</label><DateInput id="void-date" value={voidDate} onChange={(e) => setVoidDate(e.target.value)} /></div></div>
              <button type="button" className="button-secondary" disabled={busy || !voidDate} onClick={voidInvoice}>{busy ? "Voiding…" : "Void invoice"}</button>
            </div>
          )}

          <h2>Payments</h2>
          {detail.payments.length === 0 ? <p className="muted">No payments recorded.</p> : (
            <div className="table-scroll"><table className="doc-table">
              <caption className="sr-only">Payments</caption>
              <thead><tr><th scope="col">Date</th><th scope="col" className="num">Amount</th></tr></thead>
              <tbody>{detail.payments.map((p) => <tr key={p.id}><td>{formatDate(p.payment_date)}</td><td className="num">{formatCents(p.amount_cents)}</td></tr>)}</tbody>
            </table></div>
          )}
          {detail.invoice.status === "posted" && orgId != null && (
            <CreditNotes orgId={orgId} invoiceId={detail.invoice.id} balanceCents={detail.invoice.balance_cents} canWrite={canWrite} onChanged={load} />
          )}
        </>
      )}
    </>
  );
}
