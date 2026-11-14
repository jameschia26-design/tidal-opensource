"use client";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { ProjectIdSelect, SalespersonSelect, TermsSelect } from "@/components/ListSelects";
import BulkLines from "@/components/BulkLines";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import PdfButton from "@/components/PdfButton";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, parseSignedCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { creditGstRate, creditGstCents } from "@/lib/credit-gst";
import DateInput from "@/components/DateInput";

type Line = { line_no: number; description: string; account_id: number; quantity: number; unit_price_cents: number; amount_cents: number; item_id?: number; project_id?: number; gst_code?: string; gst_rate_bp?: number; gst_cents?: number };
type Note = { amounts_are?: string; currency?: string; fx_rate_micro?: number; commercial?: { salesperson?: string; subject?: string; adjustment_cents?: number; adjustment_description?: string; discount_type?: string; discount_cents?: number; discount_basis_points?: number; shipping_cents?: number }; id: number; contact_id: number; customer_name: string; credit_number: string; reference: string; credit_date: string; lines: Line[]; total_cents: number; balance_cents: number; notes: string; terms: string; status: string };
type Account = { id: number; code: string; name: string; type: string };
type Contact = { id: number; name: string; role: string };
const blankLine = () => ({ description: "", account_id: "", quantity: "1", rate: "", gst_code: "NONE", selected: false, item_id: 0, project_id: "" });
const blank = () => ({ amounts_are: "exclusive", currency: "SGD", fx_rate: "", contact_id: "", credit_number: "", reference: "", credit_date: new Date().toISOString().slice(0, 10), discount_type: "flat", discount: "", shipping: "", adjustment_label: "", adjustment: "", salesperson: "", subject: "", notes: "", terms: "" });

export default function CreditNotesPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Note[] | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [items, setItems] = useState<{ id: number; name: string; sku: string; sales_description: string; selling_price_cents: number }[]>([]);
  const [form, setForm] = useState(blank);
  const [lines, setLines] = useState([blankLine()]);
  const [editing, setEditing] = useState<number | null>(null);
  const [detail, setDetail] = useState<Note | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [bulkQuantity, setBulkQuantity] = useState("");
  const [bulkRate, setBulkRate] = useState("");
  const [bulkError, setBulkError] = useState("");

  // Sets quantity and/or rate on every selected item; blank fields leave that value alone.
  function applyBulkEdit() {
    const quantityOk = bulkQuantity === "" || (/^\d{1,7}$/.test(bulkQuantity) && Number(bulkQuantity) >= 1);
    const rate = bulkRate === "" ? null : parseCents(bulkRate);
    if (!lines.some(l => l.selected) || (bulkQuantity === "" && bulkRate === "") || !quantityOk || (bulkRate !== "" && (rate == null || rate < 0))) {
      setBulkError("Select items and enter a whole quantity of at least 1 and/or a rate of zero or more."); return;
    }
    setBulkError("");
    setLines(current => current.map(l => l.selected ? { ...l, ...(bulkQuantity === "" ? {} : { quantity: bulkQuantity }), ...(rate == null ? {} : { rate: (rate / 100).toFixed(2) }) } : l));
  }

  async function load() {
    if (orgId == null) return;
    const [n, a, c, it] = await Promise.all([
      api<{ credit_notes: Note[] }>(`/v1/credit-notes?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
      api<{ items: { id: number; name: string; sku: string; sales_description: string; selling_price_cents: number }[] }>(`/v1/items?org_id=${orgId}`),
    ]);
    if (!live.current) return;
    setRows(n.credit_notes); setAccounts(a.accounts); setContacts(c.contacts); setItems(it.items);
    setDetail(d => d && (n.credit_notes.find(x => x.id === d.id) ?? null));
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const parsed = lines.map(l => ({ description: l.description.trim(), account_id: Number(l.account_id), quantity: /^\d{1,7}$/.test(l.quantity) ? Number(l.quantity) : 0, unit_price_cents: parseCents(l.rate), gst_code: l.gst_code, ...(l.item_id ? { item_id: l.item_id } : {}), ...(l.project_id ? { project_id: Number(l.project_id) } : {}) }));
  const subtotal = parsed.reduce((n, l) => n + l.quantity * (l.unit_price_cents ?? 0), 0);
  const discountCents = form.discount_type === "percent" ? Math.round(subtotal * Math.round(Number(form.discount || 0) * 100) / 10000) : (parseCents(form.discount || "0") ?? 0);
  const adjustmentCents = parseSignedCents(form.adjustment || "0");
  const gstRate = creditGstRate(form.credit_date);
  const gst = parsed.reduce((sum, l) => sum + creditGstCents(l.quantity * (l.unit_price_cents ?? 0), l.gst_code, gstRate), 0);
  const gstConflict = parsed.some(l => l.gst_code === "SR") && (discountCents !== 0 || (form.discount_type === "percent" && Number(form.discount || 0) !== 0) || (parseCents(form.shipping || "0") ?? 0) !== 0 || (adjustmentCents ?? 0) !== 0);
  const total = gst + subtotal - discountCents + (parseCents(form.shipping || "0") ?? 0) + (adjustmentCents ?? 0);

  function startEdit(n: Note) {
    setEditing(n.id); setOpen(true); setDetail(null); setError(""); setNotice("");
    setForm({ ...blank(), amounts_are: n.amounts_are === "inclusive" ? "inclusive" : "exclusive", currency: n.currency ?? "SGD", fx_rate: n.fx_rate_micro && n.fx_rate_micro !== 1000000 ? String(n.fx_rate_micro / 1000000) : "", contact_id: String(n.contact_id), credit_number: n.credit_number, reference: n.reference, credit_date: n.credit_date, discount_type: n.commercial?.discount_type === "percent" ? "percent" : "flat", discount: ((n.commercial?.discount_type === "percent" ? n.commercial?.discount_basis_points : n.commercial?.discount_cents) ?? 0) / 100 + "", shipping: ((n.commercial?.shipping_cents ?? 0) / 100).toFixed(2), notes: n.notes, terms: n.terms, salesperson: n.commercial?.salesperson ?? "", subject: n.commercial?.subject ?? "", adjustment_label: n.commercial?.adjustment_description ?? "", adjustment: n.commercial?.adjustment_cents ? (n.commercial.adjustment_cents / 100).toFixed(2) : "" });
    setLines(n.lines.map(l => ({ description: l.description, account_id: String(l.account_id), quantity: String(l.quantity), rate: (l.unit_price_cents / 100).toFixed(2), selected: false, gst_code: l.gst_code ?? "NONE", item_id: l.item_id ?? 0, project_id: l.project_id ? String(l.project_id) : "" })));
  }
  async function send(path: string, method: string, body: object, done: string) {
    if (busy || orgId == null) return false;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ id?: number }>(path, { method, body: JSON.stringify({ org_id: orgId, ...body }) });
      const bad = method === "POST" && r?.id && !path.includes("/stage") ? await saveExtras("credit-notes", r.id, orgId, extras) : "";
      if (!live.current) return false;
      setExtras(noExtras()); setNotice(done + (bad ? " " + bad : "")); await load(); return true;
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } return false; }
    finally { if (live.current) setBusy(false); }
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    const shipping = parseCents(form.shipping || "0"), discountRaw = form.discount_type === "percent" ? Math.round(Number(form.discount || 0) * 100) : parseCents(form.discount || "0");
    if (!form.contact_id || !form.credit_date || shipping == null || adjustmentCents == null || discountRaw == null || Number.isNaN(discountRaw) || parsed.some(l => !l.description || !l.account_id || l.quantity < 1 || l.unit_price_cents == null) || total <= 0) {
      setError("Choose a customer and valid lines with a positive total."); return;
    }
    if (gstConflict) { setError("Standard-rate GST cannot be combined with discounts, shipping or adjustments."); return; }
    const commercial = { discount_type: form.discount_type, discount_cents: form.discount_type === "flat" ? discountRaw : 0, discount_basis_points: form.discount_type === "percent" ? discountRaw : 0, shipping_cents: shipping, adjustment_cents: adjustmentCents, adjustment_description: form.adjustment_label.trim(), salesperson: form.salesperson.trim(), subject: form.subject.trim() };
    const body = { contact_id: Number(form.contact_id), credit_number: form.credit_number.trim(), reference: form.reference.trim(), credit_date: form.credit_date, amounts_are: form.amounts_are, ...(form.currency === "SGD" ? {} : { currency: form.currency, fx_rate_micro: Math.round(Number(form.fx_rate) * 1000000) }), lines: parsed, commercial, notes: form.notes, terms: form.terms };
    if (await send(editing ? `/v1/credit-notes/${editing}` : "/v1/credit-notes", editing ? "PUT" : "POST", body, "Credit note saved as draft. Nothing is booked yet.")) { setOpen(false); setEditing(null); setForm(blank()); setLines([blankLine()]); }
  }
  const stage = (n: Note, to: string, msg: string) => void send(`/v1/credit-notes/${n.id}/stage`, "POST", { stage: to }, msg);
  const revenue = accounts.filter(a => a.type === "revenue");

  return <>
    <PageHeader title="Credit notes" description="Credit a customer for returns, refunds or corrections." />
    <p className="notice">SGD. Standard-rate GST is available for GST-registered companies. Opening a credit note books it: income goes down and the customer owes less. An open credit note is locked and stays as a customer credit until applied to an invoice or refunded. For a credit against one invoice, use Credit note on that invoice.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setEditing(null); setDetail(null); setExtras(noExtras()); setForm(blank()); setLines([blankLine()]); }}>New credit note</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Credit note</legend><div className="form-grid">
        <div><label htmlFor="cn-customer">Customer</label><select id="cn-customer" required value={form.contact_id} onChange={e => setForm({ ...form, contact_id: e.target.value })}><option value="">Choose customer</option>{contacts.filter(c => c.role !== "supplier").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="cn-number">Credit note# (blank = automatic)</label><input id="cn-number" maxLength={50} value={form.credit_number} onChange={e => setForm({ ...form, credit_number: e.target.value })} /></div>
        <div><label htmlFor="cn-ref">Reference#</label><input id="cn-ref" maxLength={100} value={form.reference} onChange={e => setForm({ ...form, reference: e.target.value })} /></div>
        <div><label htmlFor="cn-date">Credit note date</label><DateInput id="cn-date" required value={form.credit_date} onChange={e => setForm({ ...form, credit_date: e.target.value })}/></div>
        <div><label htmlFor="cn-sales">Salesperson</label>{orgId != null && <SalespersonSelect id="cn-sales" orgId={orgId} value={form.salesperson} onChange={v => setForm({ ...form, salesperson: v })} />}</div>
        <div><label htmlFor="cn-subject">Subject</label><input id="cn-subject" maxLength={200} value={form.subject} onChange={e => setForm({ ...form, subject: e.target.value })} /></div>
        <div><label htmlFor="cn-amounts">Amounts are</label><select id="cn-amounts" value={form.amounts_are} onChange={e => setForm({ ...form, amounts_are: e.target.value })}><option value="exclusive">Tax exclusive</option><option value="inclusive">Tax inclusive</option></select><p className="section-help">Tax inclusive: the typed rate already contains GST, so the net is worked back out of it.</p></div>
        <div><label htmlFor="cn-currency">Currency</label><select id="cn-currency" value={form.currency} onChange={e => setForm({ ...form, currency: e.target.value, fx_rate: e.target.value === "SGD" ? "" : form.fx_rate })}><option value="SGD">SGD — Singapore Dollar</option><option value="USD">USD — US Dollar</option><option value="EUR">EUR — Euro</option><option value="GBP">GBP — Pound Sterling</option><option value="AUD">AUD — Australian Dollar</option><option value="JPY">JPY — Japanese Yen</option><option value="MYR">MYR — Malaysian Ringgit</option><option value="HKD">HKD — Hong Kong Dollar</option><option value="CNY">CNY — Chinese Yuan</option></select></div>
        <div><label htmlFor="cn-fx">Rate to SGD <span className="field-optional">Required for a foreign currency</span></label><input id="cn-fx" inputMode="decimal" disabled={form.currency === "SGD"} placeholder={form.currency === "SGD" ? "1" : "e.g. 1.35"} value={form.fx_rate} onChange={e => setForm({ ...form, fx_rate: e.target.value })} /><p className="section-help">Amounts are recorded in the books in SGD at this rate; the currency is stored on the credit note.</p></div>
        <div><label htmlFor="cn-ar">Accounts receivable</label><input id="cn-ar" readOnly value="1100 — Accounts Receivable" /><p className="section-help">Shown is the standard account; the company&apos;s chosen receivable account in Settings, Custom fields and lists is used when posting.</p></div>
      </div></fieldset>
      <fieldset disabled={busy}><legend>Items</legend>
        {lines.map((l, i) => <fieldset key={i}><legend>Item {i + 1}</legend><label className="check-label"><input type="checkbox" checked={l.selected} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, selected: e.target.checked } : v))} /> Select item {i + 1} for bulk actions</label><div className="form-grid">
          <div><label htmlFor={`cn-item-${i}`}>Saved item <span className="field-optional">Optional</span></label><select id={`cn-item-${i}`} value="" onChange={e => { const item = items.find(v => v.id === Number(e.target.value)); if (item) setLines(lines.map((v, j) => i === j ? { ...v, item_id: item.id, description: (item.sales_description || item.name).slice(0, 200), rate: (item.selling_price_cents / 100).toFixed(2) } : v)); }}><option value="">Select item or enter details</option>{items.map(item => <option key={item.id} value={item.id}>{item.name}{item.sku ? ` (${item.sku})` : ""}</option>)}</select></div>
          <div><label htmlFor={`cn-d-${i}`}>Item details</label><input id={`cn-d-${i}`} required maxLength={200} value={l.description} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, description: e.target.value } : v))} /></div>
          <div><label htmlFor={`cn-a-${i}`}>Account</label><select id={`cn-a-${i}`} required value={l.account_id} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, account_id: e.target.value } : v))}><option value="">Choose income account</option>{revenue.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
          <div><label htmlFor={`cn-q-${i}`}>Quantity</label><input id={`cn-q-${i}`} required inputMode="numeric" value={l.quantity} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, quantity: e.target.value } : v))} /></div>
          <div><label htmlFor={`cn-r-${i}`}>Rate (SGD)</label><input id={`cn-r-${i}`} required inputMode="decimal" value={l.rate} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, rate: e.target.value } : v))} /></div>
          <div><label htmlFor={`cn-gst-${i}`}>Tax</label><select id={`cn-gst-${i}`} value={l.gst_code} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, gst_code: e.target.value } : v))}><option value="NONE">No GST</option><option value="SR">Standard-rate GST ({gstRate}%)</option></select></div>
          <div><label htmlFor={`cn-project-${i}`}>Project <span className="field-optional">Optional</span></label>{orgId != null && <ProjectIdSelect id={`cn-project-${i}`} orgId={orgId} contactId={form.contact_id} value={l.project_id} onChange={v => setLines(lines.map((x, j) => i === j ? { ...x, project_id: v } : x))} />}</div>
        </div><p>Amount before GST: {formatCents(parsed[i].quantity * (parsed[i].unit_price_cents ?? 0))} · GST: {formatCents(creditGstCents(parsed[i].quantity * (parsed[i].unit_price_cents ?? 0), l.gst_code, gstRate))}</p>{lines.length > 1 && <button type="button" className="button-secondary" onClick={() => setLines(lines.filter((_, j) => j !== i))}>Remove item {i + 1}</button>}</fieldset>)}
        <div className="form-grid"><div><label htmlFor="cn-bulk-qty">Set selected quantity</label><input id="cn-bulk-qty" inputMode="numeric" value={bulkQuantity} onChange={e => setBulkQuantity(e.target.value)} /></div><div><label htmlFor="cn-bulk-rate">Set selected rate (SGD)</label><input id="cn-bulk-rate" inputMode="decimal" value={bulkRate} onChange={e => setBulkRate(e.target.value)} /></div><div><button type="button" className="button-secondary" disabled={!lines.some(l => l.selected)} onClick={applyBulkEdit}>Apply to selected items</button><button type="button" className="button-secondary" disabled={!lines.some(l => l.selected) || lines.every(l => l.selected)} onClick={() => setLines(lines.filter(l => !l.selected))}>Remove selected items</button></div></div>
        {bulkError && <p role="alert" className="form-error">{bulkError}</p>}
        <button type="button" className="button-secondary" disabled={lines.length >= 200} onClick={() => setLines([...lines, blankLine()])}>Add new row</button><BulkLines id="bulk" room={200 - lines.length} accounts={revenue} onAccount={v => setLines(lines.map(l => ({ ...l, account_id: v })))} onAdd={rows => setLines([...lines.filter(l => l.description || l.rate), ...rows.map(r => ({ ...blankLine(), ...r }))])} />
        <p className="section-help">Whole quantities only. Rates exclude GST. Standard-rate GST requires GST registration and cannot be combined with discounts, shipping or adjustments.</p>
      </fieldset>
      <fieldset disabled={busy}><legend>Totals, notes and terms</legend><div className="form-grid">
        <div><label htmlFor="cn-dtype">Discount type</label><select id="cn-dtype" value={form.discount_type} onChange={e => setForm({ ...form, discount_type: e.target.value, discount: "" })}><option value="flat">Amount (SGD)</option><option value="percent">Percent</option></select></div>
        <div><label htmlFor="cn-discount">Discount</label><input id="cn-discount" inputMode="decimal" value={form.discount} onChange={e => setForm({ ...form, discount: e.target.value })} /></div>
        <div><label htmlFor="cn-shipping">Shipping charges (SGD)</label><input id="cn-shipping" inputMode="decimal" value={form.shipping} onChange={e => setForm({ ...form, shipping: e.target.value })} /></div>
        <div><label htmlFor="cn-adj-label">Adjustment description</label><input id="cn-adj-label" maxLength={200} value={form.adjustment_label} onChange={e => setForm({ ...form, adjustment_label: e.target.value })} /></div>
        <div><label htmlFor="cn-adj">Adjustment (SGD, may be negative)</label><input id="cn-adj" inputMode="decimal" value={form.adjustment} onChange={e => setForm({ ...form, adjustment: e.target.value })} /></div>
      </div><p aria-live="polite">Sub total {formatCents(subtotal)} · GST {formatCents(gst)} · Total {formatCents(total)}</p>
        <p className="section-help">Discount, shipping and adjustment are booked against the first item&apos;s income account; a separate shipping account is not available.</p>
        <label htmlFor="cn-notes">Customer notes</label><textarea id="cn-notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} />
        <label htmlFor="cn-terms">Terms &amp; conditions</label><textarea id="cn-terms" rows={3} maxLength={4000} value={form.terms} onChange={e => setForm({ ...form, terms: e.target.value })} />
        <p className="section-help">Save the draft, then attach up to 5 files from its details.</p>
      </fieldset>
      {!editing && orgId != null && <ExtrasForm kind="credit-notes" orgId={orgId} value={extras} onChange={setExtras} />}
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save as draft"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => { setOpen(false); setEditing(null); }}>Cancel</button></div>
    </form>}
    {detail && orgId != null && <section className="record-form"><h2>{detail.credit_number} — {detail.customer_name}</h2>
      <dl className="detail-grid"><div><dt>Status</dt><dd>{detail.status}</dd></div><div><dt>Date</dt><dd>{detail.credit_date}</dd></div><div><dt>Total</dt><dd>{formatCents(detail.total_cents)}</dd></div>{detail.status === "open" && <div><dt>Available credit</dt><dd>{formatCents(detail.balance_cents)}</dd></div>}</dl>
      <div className="table-scroll"><table className="doc-table"><caption>Items</caption><thead><tr><th>Item</th><th>Quantity</th><th>Rate</th><th>Amount</th><th>GST</th></tr></thead><tbody>{detail.lines.map(l => <tr key={l.line_no}><td>{l.description}</td><td>{l.quantity}</td><td>{formatCents(l.unit_price_cents)}</td><td>{formatCents(l.amount_cents)}</td><td>{l.gst_code === "SR" ? `${(l.gst_rate_bp ?? 0) / 100}% — ${formatCents(l.gst_cents ?? 0)}` : "No GST"}</td></tr>)}</tbody></table></div>
      {detail.notes && <p className="long-text">{detail.notes}</p>}
      {canWrite && detail.status === "open" && <ApplyCredit note={detail} orgId={orgId} onDone={load} />}
      {canWrite && detail.status === "open" && <RefundCredit note={detail} orgId={orgId} onDone={load} />}
      <PdfButton kind="customer-credit-notes" id={detail.id} orgId={orgId} /><Attachments key={detail.id} kind="credit-notes" id={detail.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras kind="credit-notes" id={detail.id} orgId={orgId} readOnly={!canWrite} />
      <div className="form-actions">{canWrite && detail.status === "draft" && <><button disabled={busy} onClick={() => startEdit(detail)}>Edit draft</button><button disabled={busy} onClick={() => stage(detail, "open", "Credit note approved and booked. It is now locked.")}>Approve</button><button className="button-secondary" disabled={busy} onClick={() => stage(detail, "void", "Draft voided.")}>Void draft</button></>}<button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></div>
    </section>}
    {rows === null && !error && <p role="status">Loading credit notes…</p>}{rows?.length === 0 && <p>No customer credit notes yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 credit notes</caption><thead><tr><th>Number</th><th>Ref</th><th>To</th><th>Date</th><th>Status</th><th>Amounts are</th><th>Currency</th><th>Total</th><th>Remaining</th><th>Details</th></tr></thead><tbody>{rows.map(n => <tr key={n.id}><td>{n.credit_number}</td><td>{n.reference || "—"}</td><td>{n.customer_name}</td><td>{n.credit_date}</td><td>{n.status}</td><td>{n.amounts_are === "inclusive" ? "Tax inclusive" : "Tax exclusive"}</td><td>{n.currency ?? "SGD"}</td><td>{formatCents(n.total_cents)}</td><td>{formatCents(n.balance_cents)}</td><td><button className="button-secondary" onClick={() => { setDetail(n); setOpen(false); }}>View<span className="sr-only"> {n.credit_number}</span></button></td></tr>)}</tbody></table></div>}
  </>;
}

// Applies this open credit to one posted invoice of the same customer. Allocation only; the server caps amounts.
function ApplyCredit({ note, orgId, onDone }: { note: Note; orgId: number; onDone: () => Promise<void> }) {
  const [invoices, setInvoices] = useState<{ id: number; number: number | null; contact_id: number; balance_cents: number; invoice_date: string }[] | null>(null);
  const [invoiceId, setInvoiceId] = useState("");
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useEffect(() => {
    let live = true;
    void api<{ invoices: { id: number; number: number | null; contact_id: number; balance_cents: number; invoice_date: string }[] }>(`/v1/invoices?org_id=${orgId}&status=posted`)
      .then(r => { if (live) setInvoices(r.invoices.filter(i => i.contact_id === note.contact_id && i.balance_cents > 0)); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, note.contact_id]);
  async function apply(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(amount);
    if (!invoiceId || cents == null || cents < 1) { setError("Choose an invoice and an amount above zero."); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/customer-credit-notes/${note.id}/apply`, { method: "POST", body: JSON.stringify({ org_id: orgId, invoice_id: Number(invoiceId), amount_cents: cents, applied_on: date }) });
      setNotice("Credit applied to the invoice. Nothing new is booked; the invoice balance is lower."); setAmount(""); setInvoiceId("");
      await onDone();
    } catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); }
    finally { setBusy(false); }
  }
  return <form className="record-form" onSubmit={apply}>
    <h3>Apply to an invoice</h3>
    <p className="section-help">Pick a posted invoice of this customer. The amount cannot exceed what the credit note has left or what the invoice still owes.</p>
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {invoices === null && !error && <p role="status">Loading invoices…</p>}
    {invoices?.length === 0 && <p>This customer has no posted invoices with a balance.</p>}
    {!!invoices?.length && <div className="form-grid">
      <div><label htmlFor="ca-inv">Invoice</label><select id="ca-inv" required disabled={busy} value={invoiceId} onChange={e => setInvoiceId(e.target.value)}><option value="">Choose invoice</option>{invoices.map(i => <option key={i.id} value={i.id}>{i.number ?? `#${i.id}`} — {i.invoice_date} — owes {formatCents(i.balance_cents)}</option>)}</select></div>
      <div><label htmlFor="ca-amt">Amount to apply (SGD)</label><input id="ca-amt" required inputMode="decimal" disabled={busy} value={amount} onChange={e => setAmount(e.target.value)} /></div>
      <div><label htmlFor="ca-date">Applied on</label><DateInput id="ca-date" required disabled={busy} value={date} onChange={e => setDate(e.target.value)}/></div>
    </div>}
    {!!invoices?.length && <div className="form-actions"><button disabled={busy}>{busy ? "Applying…" : "Apply credit"}</button></div>}
  </form>;
}

// Pays money back to the customer against this open credit. Money goes out of the bank; the server caps the amount.
function RefundCredit({ note, orgId, onDone }: { note: Note; orgId: number; onDone: () => Promise<void> }) {
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function refund(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(amount);
    if (cents == null || cents < 1) { setError("Enter an amount above zero."); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/customer-credit-notes/${note.id}/refund`, { method: "POST", body: JSON.stringify({ org_id: orgId, amount_cents: cents, refund_date: date, idempotency_key: `refund-${note.id}-${crypto.randomUUID()}` }) });
      setNotice("Refund recorded: money paid out of the bank to the customer."); setAmount("");
      await onDone();
    } catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); }
    finally { setBusy(false); }
  }
  return <form className="record-form" onSubmit={refund}>
    <h3>Refund the customer</h3>
    <p className="section-help">Pays money out of the bank. Cannot be more than the credit note has left.</p>
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    <div className="form-grid">
      <div><label htmlFor="cr-amt">Amount to refund (SGD)</label><input id="cr-amt" required inputMode="decimal" disabled={busy} value={amount} onChange={e => setAmount(e.target.value)} /></div>
      <div><label htmlFor="cr-date">Refund date</label><DateInput id="cr-date" required disabled={busy} value={date} onChange={e => setDate(e.target.value)}/></div>
    </div>
    <div className="form-actions"><button disabled={busy}>{busy ? "Refunding…" : "Record refund"}</button></div>
  </form>;
}
