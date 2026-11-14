"use client";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { SalespersonSelect, TermsSelect, cashBankAccounts } from "@/components/ListSelects";
import BulkLines from "@/components/BulkLines";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import PdfButton from "@/components/PdfButton";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { creditGstRate, creditGstCents } from "@/lib/credit-gst";
import DateInput from "@/components/DateInput";
import { formatDate, todayISO } from "@/lib/dates";

type Line = { line_no: number; description: string; account_id: number; quantity: number; unit_price_cents: number; amount_cents: number; item_id: number; gst_code?: string; gst_rate_bp?: number; gst_cents?: number };
type PO = { commercial?: { payment_terms_days?: number; discount_type?: string; discount_cents?: number; discount_basis_points?: number; shipping_cents?: number; attention?: string; telephone?: string; delivery_instructions?: string; currency?: string; fx_rate_micro?: number }; id: number; contact_id: number; vendor_name: string; po_number: string; reference: string; po_date: string; delivery_date: string; delivery_address: string; shipment_preference: string; lines: Line[]; total_cents: number; notes: string; terms: string; status: string; lifecycle_status: string; sent_at: string; billed_cents: number; billed_percent: number; files: number; received: Record<string, number>; in_transit: Record<string, number> };
const PO_TABS = [{ id: "", label: "All" }, { id: "draft", label: "Draft" }, { id: "awaiting_approval", label: "Awaiting approval" }, { id: "approved", label: "Approved" }, { id: "billed", label: "Billed" }];
type Account = { id: number; code: string; name: string; type: string };
type Contact = { id: number; name: string; role: string };
const today = todayISO;
const blankLine = () => ({ description: "", account_id: "", quantity: "1", rate: "", item_id: 0, gst_code: "NONE" });
const blank = () => ({ contact_id: "", po_number: "", reference: "", po_date: today(), delivery_date: new Date(Date.now() + 7 * 864e5).toISOString().slice(0, 10), delivery_address: "", shipment_preference: "", attention: "", telephone: "", delivery_instructions: "", currency: "SGD", fx_rate: "", discount_type: "flat", discount: "", shipping: "", terms_days: 0, notes: "", terms: "" });

export default function PurchaseOrdersPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<PO[] | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [items, setItems] = useState<{ id: number; name: string; sku: string; purchase_description: string; cost_price_cents: number }[]>([]);
  const [form, setForm] = useState(blank);
  const [lines, setLines] = useState([blankLine()]);
  const [editing, setEditing] = useState<number | null>(null);
  const [detail, setDetail] = useState<PO | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [filter, setFilter] = useState("");
  const [query, setQuery] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [dateType, setDateType] = useState<"po_date" | "delivery_date">("po_date");
  const [hidden, setHidden] = useState<string[]>([]);

  async function load() {
    if (orgId == null) return;
    const [p, a, c, it] = await Promise.all([
      api<{ purchase_orders: PO[] }>(`/v1/purchase-orders?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=supplier`),
      api<{ items: { id: number; name: string; sku: string; purchase_description: string; cost_price_cents: number }[] }>(`/v1/items?org_id=${orgId}`),
    ]);
    if (!live.current) return;
    setRows(p.purchase_orders); setAccounts(a.accounts); setContacts(c.contacts); setItems(it.items);
    setDetail(d => d && (p.purchase_orders.find(x => x.id === d.id) ?? null));
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const parsed = lines.map(l => ({ description: l.description.trim(), account_id: Number(l.account_id), quantity: /^\d{1,7}$/.test(l.quantity) ? Number(l.quantity) : 0, unit_price_cents: parseCents(l.rate), gst_code: l.gst_code, ...(l.item_id ? { item_id: l.item_id } : {}) }));
  const subtotal = parsed.reduce((n, l) => n + l.quantity * (l.unit_price_cents ?? 0), 0);
  const discountCents = form.discount_type === "percent" ? Math.round(subtotal * Math.round(Number(form.discount || 0) * 100) / 10000) : (parseCents(form.discount || "0") ?? 0);
  const gstRate = creditGstRate(form.po_date);
  const gst = parsed.reduce((sum, l) => sum + creditGstCents(l.quantity * (l.unit_price_cents ?? 0), l.gst_code, gstRate), 0);
  const gstConflict = parsed.some(l => l.gst_code === "SR") && (discountCents !== 0 || (form.discount_type === "percent" && Number(form.discount || 0) !== 0) || (parseCents(form.shipping || "0") ?? 0) !== 0);
  const total = subtotal - discountCents + (parseCents(form.shipping || "0") ?? 0) + gst;

  function startEdit(p: PO) {
    setEditing(p.id); setOpen(true); setDetail(null); setError(""); setNotice("");
    setForm({ ...blank(), contact_id: String(p.contact_id), po_number: p.po_number, reference: p.reference, po_date: p.po_date, delivery_date: p.delivery_date, delivery_address: p.delivery_address, shipment_preference: p.shipment_preference, terms_days: p.commercial?.payment_terms_days ?? 0, discount_type: p.commercial?.discount_type === "percent" ? "percent" : "flat", discount: ((p.commercial?.discount_type === "percent" ? p.commercial?.discount_basis_points : p.commercial?.discount_cents) ?? 0) / 100 + "", shipping: ((p.commercial?.shipping_cents ?? 0) / 100).toFixed(2), attention: p.commercial?.attention ?? "", telephone: p.commercial?.telephone ?? "", delivery_instructions: p.commercial?.delivery_instructions ?? "", currency: p.commercial?.currency || "SGD", fx_rate: p.commercial?.fx_rate_micro && p.commercial.fx_rate_micro !== 1000000 ? String(p.commercial.fx_rate_micro / 1000000) : "", notes: p.notes, terms: p.terms });
    setLines(p.lines.map(l => ({ description: l.description, account_id: String(l.account_id), quantity: String(l.quantity), rate: (l.unit_price_cents / 100).toFixed(2), item_id: l.item_id ?? 0, gst_code: l.gst_code ?? "NONE" })));
  }
  async function send(path: string, method: string, body: object, done: string) {
    if (busy || orgId == null) return false;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ id?: number }>(path, { method, body: JSON.stringify({ org_id: orgId, ...body }) });
      const bad = method === "POST" && r?.id && !path.includes("/stage") ? await saveExtras("purchase-orders", r.id, orgId, extras) : "";
      if (!live.current) return false;
      setExtras(noExtras()); setNotice(done + (bad ? " " + bad : "")); await load(); return true;
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } return false; }
    finally { if (live.current) setBusy(false); }
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    const shipping = parseCents(form.shipping || "0"), discountRaw = form.discount_type === "percent" ? Math.round(Number(form.discount || 0) * 100) : parseCents(form.discount || "0");
    if (!form.contact_id || !form.po_date || form.delivery_date <= form.po_date || shipping == null || discountRaw == null || Number.isNaN(discountRaw) || parsed.some(l => !l.description || !l.account_id || l.quantity < 1 || l.unit_price_cents == null) || total <= 0) {
      setError("Choose a vendor, a delivery date after the PO date, and valid lines with a positive total."); return;
    }
    if (gstConflict) { setError("Standard-rate GST cannot be combined with discounts or shipping."); return; }
    const commercial = { currency: form.currency, fx_rate_micro: form.currency === "SGD" ? 1000000 : Math.round(Number(form.fx_rate) * 1000000), discount_type: form.discount_type, discount_cents: form.discount_type === "flat" ? discountRaw : 0, discount_basis_points: form.discount_type === "percent" ? discountRaw : 0, shipping_cents: shipping, payment_terms_days: form.terms_days, attention: form.attention.trim(), telephone: form.telephone.trim(), delivery_instructions: form.delivery_instructions.trim() };
    const body = { contact_id: Number(form.contact_id), po_number: form.po_number.trim(), reference: form.reference.trim(), po_date: form.po_date, delivery_date: form.delivery_date, delivery_address: form.delivery_address, shipment_preference: form.shipment_preference.trim(), lines: parsed, commercial, notes: form.notes, terms: form.terms };
    if (await send(editing ? `/v1/purchase-orders/${editing}` : "/v1/purchase-orders", editing ? "PUT" : "POST", body, "Purchase order saved as draft. Nothing was sent to the vendor.")) { setOpen(false); setEditing(null); setForm(blank()); setLines([blankLine()]); }
  }
  const stage = (p: PO, to: string, msg: string) => void send(`/v1/purchase-orders/${p.id}/stage`, "POST", { stage: to }, msg);
  async function copyBill(p: PO) {
    if (busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const made = await api<{ bill_id: number; billed_percent: number }>(`/v1/purchase-orders/${p.id}/bill`, { method: "POST", body: JSON.stringify({ org_id: orgId }) });
      if (!live.current) return;
      setNotice(`Bill ${made.bill_id} drafted from ${p.po_number} with accounts, tax and a due date. ${made.billed_percent}% of the order is now billed. Review it on Bills, then approve it.`);
      await load();
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  const accountOptions = [...accounts.filter(a => a.type === "expense"), ...cashBankAccounts(accounts)];

  return <>
    <PageHeader title="Purchase orders" description="Order goods or services from vendors. Orders do not post to the books." />
    <p className="notice">SGD. Standard-rate GST is available for GST-registered companies. A purchase order is a commitment, not a bill: no journal is posted. Issuing locks the order; print or send the PDF yourself. Receive goods on the Purchase receives page, then enter the bill.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setEditing(null); setDetail(null); setExtras(noExtras()); setForm(blank()); setLines([blankLine()]); }}>New purchase order</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Purchase order</legend><div className="form-grid">
        <div><label htmlFor="po-vendor">Vendor</label><select id="po-vendor" required value={form.contact_id} onChange={e => setForm({ ...form, contact_id: e.target.value })}><option value="">Choose vendor</option>{contacts.filter(c => c.role !== "customer").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="po-number">Purchase order# (blank = automatic)</label><input id="po-number" maxLength={50} value={form.po_number} onChange={e => setForm({ ...form, po_number: e.target.value })} /></div>
        <div><label htmlFor="po-ref">Reference#</label><input id="po-ref" maxLength={100} value={form.reference} onChange={e => setForm({ ...form, reference: e.target.value })} /></div>
        <div><label htmlFor="po-date">Date</label><DateInput id="po-date" required value={form.po_date} onChange={e => setForm({ ...form, po_date: e.target.value })}/></div>
        <div><label htmlFor="po-currency">Currency</label><select id="po-currency" value={form.currency} onChange={e => setForm({ ...form, currency: e.target.value })}><option value="SGD">SGD — Singapore Dollar</option><option value="USD">USD — US Dollar</option><option value="EUR">EUR — Euro</option><option value="GBP">GBP — Pound Sterling</option><option value="AUD">AUD — Australian Dollar</option><option value="MYR">MYR — Malaysian Ringgit</option></select></div>
        {form.currency !== "SGD" && <div><label htmlFor="po-fx">Exchange rate to SGD</label><input id="po-fx" required inputMode="decimal" value={form.fx_rate} onChange={e => setForm({ ...form, fx_rate: e.target.value })} /><p className="section-help">One {form.currency} equals this many SGD on the PO date.</p></div>}
        <div><label htmlFor="po-delivery">Delivery date</label><DateInput id="po-delivery" required min={form.po_date} value={form.delivery_date} onChange={e => setForm({ ...form, delivery_date: e.target.value })}/><p className="section-help">Must be after the PO date.</p></div>
        <div><label htmlFor="po-ship">Shipment preference</label><input id="po-ship" maxLength={200} value={form.shipment_preference} onChange={e => setForm({ ...form, shipment_preference: e.target.value })} /></div>
        <div><label htmlFor="po-address">Delivery address</label><textarea id="po-address" rows={3} maxLength={1000} value={form.delivery_address} onChange={e => setForm({ ...form, delivery_address: e.target.value })} /><p className="section-help">Typed address. Choosing organization, customer or admin addresses is not available.</p></div>
        <div><label htmlFor="po-attention">Attention</label><input id="po-attention" maxLength={200} value={form.attention} onChange={e => setForm({ ...form, attention: e.target.value })} /></div>
        <div><label htmlFor="po-phone">Telephone</label><input id="po-phone" type="tel" maxLength={50} value={form.telephone} onChange={e => setForm({ ...form, telephone: e.target.value })} /></div>
        <div><label htmlFor="po-instr">Delivery instructions</label><textarea id="po-instr" rows={3} maxLength={500} value={form.delivery_instructions} onChange={e => setForm({ ...form, delivery_instructions: e.target.value })} /><p className="section-help">{form.delivery_instructions.length}/500</p></div>
      </div></fieldset>
      <fieldset disabled={busy}><legend>Items</legend>
        {lines.map((l, i) => <fieldset key={i}><legend>Item {i + 1}</legend><div className="form-grid">
          <div><label htmlFor={`po-item-${i}`}>Saved item <span className="field-optional">Optional</span></label><select id={`po-item-${i}`} value="" onChange={e => { const item = items.find(v => v.id === Number(e.target.value)); if (item) setLines(lines.map((v, j) => i === j ? { ...v, item_id: item.id, description: (item.purchase_description || item.name).slice(0, 200), rate: (item.cost_price_cents / 100).toFixed(2) } : v)); }}><option value="">Select item or enter details</option>{items.map(item => <option key={item.id} value={item.id}>{item.name}{item.sku ? ` (${item.sku})` : ""}</option>)}</select></div>
          <div><label htmlFor={`po-d-${i}`}>Item details</label><input id={`po-d-${i}`} required maxLength={200} value={l.description} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, description: e.target.value } : v))} /></div>
          <div><label htmlFor={`po-a-${i}`}>Account</label><select id={`po-a-${i}`} required value={l.account_id} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, account_id: e.target.value } : v))}><option value="">Choose account</option>{accountOptions.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
          <div><label htmlFor={`po-q-${i}`}>Quantity</label><input id={`po-q-${i}`} required inputMode="numeric" value={l.quantity} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, quantity: e.target.value } : v))} /></div>
          <div><label htmlFor={`po-r-${i}`}>Rate (SGD)</label><input id={`po-r-${i}`} required inputMode="decimal" value={l.rate} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, rate: e.target.value } : v))} /></div>
          <div><label htmlFor={`po-gst-${i}`}>Tax</label><select id={`po-gst-${i}`} value={l.gst_code} onChange={e => setLines(lines.map((v, j) => i === j ? { ...v, gst_code: e.target.value } : v))}><option value="NONE">No GST</option><option value="SR">Standard-rate GST ({gstRate}%)</option></select></div>
        </div><p>Amount before GST: {formatCents(parsed[i].quantity * (parsed[i].unit_price_cents ?? 0))} · GST: {formatCents(creditGstCents(parsed[i].quantity * (parsed[i].unit_price_cents ?? 0), l.gst_code, gstRate))}</p>{lines.length > 1 && <button type="button" className="button-secondary" onClick={() => setLines(lines.filter((_, j) => j !== i))}>Remove item {i + 1}</button>}</fieldset>)}
        <button type="button" className="button-secondary" disabled={lines.length >= 200} onClick={() => setLines([...lines, blankLine()])}>Add new row</button><BulkLines id="bulk" room={200 - lines.length} accounts={accountOptions} onAccount={v => setLines(lines.map(l => ({ ...l, account_id: v })))} onAdd={rows => setLines([...lines.filter(l => l.description || l.rate), ...rows.map(r => ({ ...blankLine(), ...r }))])} />
        <p className="section-help">Whole quantities only. Rates exclude GST. Standard-rate GST requires GST registration and cannot be combined with discounts or shipping.</p>
      </fieldset>
      <fieldset disabled={busy}><legend>Totals, notes and terms</legend><div className="form-grid">
        <div><label htmlFor="po-payterms">Payment terms</label><TermsSelect id="po-payterms" orgId={orgId ?? 0} days={form.terms_days} onChange={d => setForm({ ...form, terms_days: d })} /></div>
        <div><label htmlFor="po-dtype">Discount type</label><select id="po-dtype" value={form.discount_type} onChange={e => setForm({ ...form, discount_type: e.target.value, discount: "" })}><option value="flat">Amount (SGD)</option><option value="percent">Percent</option></select></div>
        <div><label htmlFor="po-discount">Discount</label><input id="po-discount" inputMode="decimal" value={form.discount} onChange={e => setForm({ ...form, discount: e.target.value })} /></div>
        <div><label htmlFor="po-shipping">Shipping charges (SGD)</label><input id="po-shipping" inputMode="decimal" value={form.shipping} onChange={e => setForm({ ...form, shipping: e.target.value })} /></div>
      </div><p aria-live="polite">Sub total {formatCents(subtotal)} · GST {formatCents(gst)} · Total {formatCents(total)}</p>
        <label htmlFor="po-notes">Notes</label><textarea id="po-notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} />
        <label htmlFor="po-terms">Terms &amp; conditions</label><textarea id="po-terms" rows={3} maxLength={4000} value={form.terms} onChange={e => setForm({ ...form, terms: e.target.value })} />
        <p className="section-help">Save the draft, then attach up to 10 files (10 MB each) from the order details.</p>
      </fieldset>
      {!editing && orgId != null && <ExtrasForm kind="purchase-orders" orgId={orgId} value={extras} onChange={setExtras} />}
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save as draft"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => { setOpen(false); setEditing(null); }}>Cancel</button></div>
    </form>}
    {detail && orgId != null && <section className="record-form"><h2>{detail.po_number} — {detail.vendor_name}</h2>
      <dl className="detail-grid"><div><dt>Status</dt><dd>{detail.lifecycle_status || detail.status}</dd></div><div><dt>Reference</dt><dd>{detail.reference || "—"}</dd></div><div><dt>Date</dt><dd>{formatDate(detail.po_date)}</dd></div><div><dt>Delivery</dt><dd>{formatDate(detail.delivery_date)}</dd></div><div><dt>Sent</dt><dd>{detail.sent_at || "Not sent"}</dd></div><div><dt>Billed</dt><dd>{formatCents(detail.billed_cents)} of {formatCents(detail.total_cents)} ({detail.billed_percent}%)</dd></div><div><dt>Total</dt><dd>{formatCents(detail.total_cents)}</dd></div></dl>
      <div className="table-scroll"><table className="doc-table"><caption>Items and receiving</caption><thead><tr><th scope="col">Item</th><th scope="col" className="num">Ordered</th><th scope="col" className="num">Received</th><th scope="col" className="num">In transit</th><th scope="col" className="num">Amount</th><th scope="col">Tax rate</th><th scope="col" className="num">Tax amount</th></tr></thead><tbody>{detail.lines.map(l => <tr key={l.line_no}><td>{l.description}</td><td className="num">{l.quantity}</td><td className="num">{detail.received?.[l.line_no] ?? 0}</td><td className="num">{detail.in_transit?.[l.line_no] ?? 0}</td><td className="num">{formatCents(l.amount_cents)}</td><td>{l.gst_code === "SR" ? `${(l.gst_rate_bp ?? 0) / 100}%` : "No GST"}</td><td className="num">{formatCents(l.gst_cents ?? 0)}</td></tr>)}</tbody></table></div>
      {detail.notes && <p className="long-text">{detail.notes}</p>}
      <PdfButton kind="purchase-orders" id={detail.id} orgId={orgId} /><Attachments key={detail.id} kind="purchase-orders" id={detail.id} orgId={orgId} limit={10} readOnly={!canWrite} /><Extras kind="purchase-orders" id={detail.id} orgId={orgId} readOnly={!canWrite} />
      <div className="form-actions">{canWrite && detail.status === "draft" && <><button disabled={busy} onClick={() => startEdit(detail)}>Edit draft</button><button className="button-secondary" disabled={busy} onClick={() => stage(detail, "awaiting_approval", "Purchase order sent for approval.")}>Submit for approval</button><button disabled={busy} onClick={() => stage(detail, "issued", "Purchase order approved. It is now locked.")}>Approve</button></>}{canWrite && detail.status === "awaiting_approval" && <button disabled={busy} onClick={() => stage(detail, "issued", "Purchase order approved. It is now locked.")}>Approve</button>}{canWrite && (detail.status === "issued" || detail.status === "received") && detail.billed_percent < 100 && <button disabled={busy} onClick={() => void copyBill(detail)}>Create bill</button>}{canWrite && (detail.status === "draft" || detail.status === "awaiting_approval" || detail.status === "issued") && <button className="button-secondary" disabled={busy} onClick={() => stage(detail, "cancelled", "Purchase order cancelled.")}>Cancel order</button>}<button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></div>
    </section>}
    {rows === null && !error && <p role="status">Loading purchase orders…</p>}{rows?.length === 0 && <p>No purchase orders yet.</p>}
    {!!rows?.length && <div className="record-toolbar" role="search" aria-label="Find purchase orders"><div><label htmlFor="po-search">Search</label><input id="po-search" type="search" value={query} onChange={e => setQuery(e.target.value)} placeholder="Number, vendor or reference" /></div><div><label htmlFor="po-from">From</label><input id="po-from" type="date" value={from} onChange={e => setFrom(e.target.value)} /></div><div><label htmlFor="po-to">To</label><input id="po-to" type="date" value={to} onChange={e => setTo(e.target.value)} /></div><label>Date type<select id="po-date-type" value={dateType} onChange={e => setDateType(e.target.value as "po_date" | "delivery_date")}><option value="po_date">Order date</option><option value="delivery_date">Delivery date</option></select></label><details><summary>Columns</summary>{["vendor", "reference", "sent", "billed", "files"].map(c => <label key={c} className="check-label"><input type="checkbox" checked={!hidden.includes(c)} onChange={e => setHidden(prev => e.target.checked ? prev.filter(x => x !== c) : [...prev, c])} />{c}</label>)}</details></div>}
    {!!rows?.length && <div><nav className="tabs" aria-label="Purchase order status"><ul>{PO_TABS.map(tab => <li key={tab.id}><button type="button" className="button-secondary" aria-pressed={filter === tab.id} onClick={() => setFilter(tab.id)}>{tab.label} ({tab.id ? rows.filter(p => (p.lifecycle_status || p.status) === tab.id).length : rows.length})</button></li>)}</ul></nav>
    {(() => { const shown = rows.filter(p => (!filter || (p.lifecycle_status || p.status) === filter) && (!from || p[dateType] >= from) && (!to || p[dateType] <= to) && `${p.po_number} ${p.vendor_name} ${p.reference}`.toLowerCase().includes(query.toLowerCase())); return <><div className="table-scroll"><table className="doc-table"><caption>Latest 200 purchase orders</caption><thead><tr><th scope="col">Number</th>{!hidden.includes("vendor") && <th scope="col">Vendor</th>}{!hidden.includes("reference") && <th scope="col">Reference</th>}<th scope="col">Date</th><th scope="col">Delivery date</th><th scope="col">Status</th>{!hidden.includes("sent") && <th scope="col">Sent</th>}<th scope="col" className="num">Total</th>{!hidden.includes("billed") && <th scope="col" className="num">Billed</th>}{!hidden.includes("files") && <th scope="col" className="num">Files</th>}<th scope="col">Details</th></tr></thead><tbody>{shown.map(p => <tr key={p.id}><td>{p.po_number}</td>{!hidden.includes("vendor") && <td>{p.vendor_name}</td>}{!hidden.includes("reference") && <td>{p.reference || "—"}</td>}<td>{formatDate(p.po_date)}</td><td>{formatDate(p.delivery_date)}</td><td>{p.lifecycle_status || p.status}</td>{!hidden.includes("sent") && <td>{p.sent_at || "—"}</td>}<td className="num">{formatCents(p.total_cents)}</td>{!hidden.includes("billed") && <td className="num">{p.billed_percent}%</td>}{!hidden.includes("files") && <td className="num">{p.files}</td>}<td><button className="button-secondary" onClick={() => { setDetail(p); setOpen(false); }}>View<span className="sr-only"> {p.po_number}</span></button></td></tr>)}</tbody></table></div><p className="section-help" role="status">Showing {shown.length} of {rows.length} purchase orders.</p></>; })()}</div>}
  </>;
}
