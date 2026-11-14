"use client";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import { useEffect, useRef, useState, type FormEvent } from "react";
import BulkLines from "@/components/BulkLines";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import PdfButton from "@/components/PdfButton";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { PaymentModeSelect, SalespersonSelect, cashBankAccounts } from "@/components/ListSelects";
import { parseCents, parseSignedCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { keyFor, clearKey } from "@/lib/payment-key";
import { creditGstRate, creditGstCents } from "@/lib/credit-gst";
import { scanItem } from "@/lib/item-scan";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Line = { line_no: number; description: string; account_id: number; quantity: number; unit_price_cents: number; amount_cents: number; gst_code?: string; gst_cents?: number };
type Receipt = { id: number; contact_id: number; customer_name: string; receipt_number: string; receipt_date: string; lines: Line[]; total_cents: number; payment_mode: string; deposit_account_id: number; reference: string; notes: string; terms: string };
type Account = { id: number; code: string; name: string; type: string };
type Contact = { id: number; name: string; role: string };
const modeLabels: Record<string, string> = { cash: "Cash", bank_transfer: "Bank transfer", cheque: "Cheque", card: "Card", paynow: "PayNow", other: "Other" };
const blankLine = () => ({ description: "", account_id: "", quantity: "1", rate: "", gst_code: "NONE", selected: false });
const blank = () => ({ contact_id: "", receipt_number: "", receipt_date: new Date().toISOString().slice(0, 10), payment_mode: "cash", deposit_account_id: "", reference: "", discount_type: "flat", discount: "", shipping: "", adjustment_label: "", adjustment: "", salesperson: "", subject: "", notes: "", terms: "" });

export default function SalesReceiptsPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const [rows, setRows] = useState<Receipt[] | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [items, setItems] = useState<{ sku: string; name: string; sales_description: string; selling_price_cents: number }[]>([]);
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState("");
  const [form, setForm] = useState(blank);
  const [lines, setLines] = useState([blankLine()]);
  const [detail, setDetail] = useState<Receipt | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const [r, a, c] = await Promise.all([
      api<{ sales_receipts: Receipt[] }>(`/v1/sales-receipts?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    ]);
    if (!live.current) return;
    setRows(r.sales_receipts); setAccounts(a.accounts); setContacts(c.contacts);
    return r.sales_receipts;
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (orgId == null) return;
    let active = true;
    api<{ items: typeof items }>(`/v1/items?org_id=${orgId}`).then(r => { if (active) setItems(r.items); }).catch(() => { if (active) setScanError("Items could not be loaded. Enter receipt lines manually."); });
    return () => { active = false; };
  }, [orgId]);
  function addScannedItem() {
    const item = scanItem(items, scan);
    if (!item) { setScanError("No unique item matches this SKU. Check the code or enter the item manually."); return; }
    if (lines.length >= 200) { setScanError("A receipt can have at most 200 lines."); return; }
    const line = { ...blankLine(), description: (item.sales_description || item.name).slice(0, 200), rate: (item.selling_price_cents / 100).toFixed(2) };
    setLines(current => current.length === 1 && !current[0].description && !current[0].rate ? [{ ...line, account_id: current[0].account_id }] : [...current, line]);
    setScan(""); setScanError("");
  }

  const parsed = lines.map(l => ({ description: l.description.trim(), account_id: Number(l.account_id), quantity: /^\d{1,7}$/.test(l.quantity) ? Number(l.quantity) : 0, unit_price_cents: parseCents(l.rate), gst_code: l.gst_code }));
  const subtotal = parsed.reduce((n, l) => n + l.quantity * (l.unit_price_cents ?? 0), 0);
  const discountCents = form.discount_type === "percent" ? Math.round(subtotal * Math.round(Number(form.discount || 0) * 100) / 10000) : (parseCents(form.discount || "0") ?? 0);
  const adjustmentCents = parseSignedCents(form.adjustment || "0");
  const gstRate = creditGstRate(form.receipt_date);
  const gst = parsed.reduce((n, l) => n + creditGstCents(l.quantity * (l.unit_price_cents ?? 0), l.gst_code, gstRate), 0);
  const total = gst + subtotal - discountCents + (parseCents(form.shipping || "0") ?? 0) + (adjustmentCents ?? 0);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy || !canWrite || orgId == null) return;
    const shipping = parseCents(form.shipping || "0"), discountRaw = form.discount_type === "percent" ? Math.round(Number(form.discount || 0) * 100) : parseCents(form.discount || "0");
    if (!form.contact_id || !form.receipt_date || !form.deposit_account_id || shipping == null || adjustmentCents == null || discountRaw == null || Number.isNaN(discountRaw) || parsed.some(l => !l.description || !l.account_id || l.quantity < 1 || l.unit_price_cents == null) || total <= 0 || total > 100000000000000) {
      setError("Choose a customer, deposit account and valid lines with a positive total."); return;
    }
    if (parsed.some(l => l.gst_code === "SR") && (discountRaw !== 0 || shipping !== 0 || adjustmentCents !== 0)) { setError("Standard-rate GST cannot be combined with discounts, shipping or adjustments."); return; }
    const commercial = { discount_type: form.discount_type, discount_cents: form.discount_type === "flat" ? discountRaw : 0, discount_basis_points: form.discount_type === "percent" ? discountRaw : 0, shipping_cents: shipping, adjustment_cents: adjustmentCents, adjustment_description: form.adjustment_label.trim(), salesperson: form.salesperson.trim(), subject: form.subject.trim() };
    const payload = { org_id: orgId, contact_id: Number(form.contact_id), receipt_number: form.receipt_number.trim(), receipt_date: form.receipt_date, lines: parsed, commercial, payment_mode: form.payment_mode, deposit_account_id: Number(form.deposit_account_id), reference: form.reference.trim(), notes: form.notes, terms: form.terms };
    const body = JSON.stringify(payload), slot = `sales-receipt-${orgId}`;
    const key = attempt.current?.body === body ? attempt.current.key : keyFor(slot, body);
    attempt.current = { body, key };
    setBusy(true); setError(""); setNotice("");
    try {
      const saved = await api<{ id: number }>("/v1/sales-receipts", { method: "POST", body: JSON.stringify({ ...payload, idempotency_key: key }) });
      clearKey(slot); attempt.current = null;
      const bad = await saveExtras("sales-receipts", saved.id, orgId, extras); setExtras(noExtras());
      if (!live.current) return;
      setOpen(false); setForm(blank()); setLines([blankLine()]);
      setNotice("Sales receipt saved and booked. It cannot be changed. No email was sent." + (bad ? " " + bad : ""));
      const list = await load();
      if (live.current) setDetail(list?.find(x => x.id === saved.id) ?? null);
    } catch (err) { if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  const setLine = (i: number, patch: object) => setLines(lines.map((v, j) => i === j ? { ...v, ...patch } : v));
  const revenue = accounts.filter(a => a.type === "revenue");
  const modeLabel = (m: string) => (modeLabels[m] ?? m) ?? m;

  return <>
    <PageHeader title="Sales receipts" description="Record a sale that was paid at once." />
    <p className="notice">SGD. Standard-rate GST requires GST registration. Saving books the sale and the money received straight away; no invoice or balance owed is created. Saved receipts cannot be changed. Save and send is not available: Tidal sends no email.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setDetail(null); setExtras(noExtras()); setForm(blank()); setLines([blankLine()]); }}>New sales receipt</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Sales receipt</legend><div className="form-grid">
        <div><label htmlFor="sr-customer">Customer</label><select id="sr-customer" required value={form.contact_id} onChange={e => setForm({ ...form, contact_id: e.target.value })}><option value="">Choose customer</option>{contacts.filter(c => c.role !== "supplier").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="sr-sales">Salesperson</label>{orgId != null && <SalespersonSelect id="sr-sales" orgId={orgId} value={form.salesperson} onChange={v => setForm({ ...form, salesperson: v })} />}</div>
        <div><label htmlFor="sr-subject">Subject</label><input id="sr-subject" maxLength={200} value={form.subject} onChange={e => setForm({ ...form, subject: e.target.value })} /></div>
        <div><label htmlFor="sr-date">Sales receipt date</label><DateInput id="sr-date" required value={form.receipt_date} onChange={e => setForm({ ...form, receipt_date: e.target.value })}/></div>
        <div><label htmlFor="sr-number">Sales receipt# (blank = automatic)</label><input id="sr-number" maxLength={50} value={form.receipt_number} onChange={e => setForm({ ...form, receipt_number: e.target.value })} /></div>
        <div><label htmlFor="sr-mode">Payment mode</label>{orgId != null && <PaymentModeSelect id="sr-mode" orgId={orgId} value={form.payment_mode} onChange={v => setForm({ ...form, payment_mode: v })} />}</div>
        <div><label htmlFor="sr-deposit">Deposit to</label><select id="sr-deposit" required value={form.deposit_account_id} onChange={e => setForm({ ...form, deposit_account_id: e.target.value })}><option value="">Choose cash or bank account</option>{cashBankAccounts(accounts).map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
        <div><label htmlFor="sr-ref">Reference#</label><input id="sr-ref" maxLength={100} value={form.reference} onChange={e => setForm({ ...form, reference: e.target.value })} /></div>
      </div></fieldset>
      <fieldset disabled={busy}><legend>Items</legend>
        {lines.map((l, i) => <fieldset key={i}><legend>Item {i + 1}</legend><label className="check-label"><input type="checkbox" checked={l.selected} onChange={e => setLine(i, { selected: e.target.checked })} /> Select item {i + 1} for bulk actions</label><div className="form-grid">
          <div><label htmlFor={`sr-d-${i}`}>Item details</label><input id={`sr-d-${i}`} required maxLength={200} value={l.description} onChange={e => setLine(i, { description: e.target.value })} /></div>
          <div><label htmlFor={`sr-a-${i}`}>Account</label><select id={`sr-a-${i}`} required value={l.account_id} onChange={e => setLine(i, { account_id: e.target.value })}><option value="">Choose income account</option>{revenue.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
          <div><label htmlFor={`sr-q-${i}`}>Quantity</label><input id={`sr-q-${i}`} required inputMode="numeric" value={l.quantity} onChange={e => setLine(i, { quantity: e.target.value })} /></div>
          <div><label htmlFor={`sr-r-${i}`}>Rate (SGD)</label><input id={`sr-r-${i}`} required inputMode="decimal" value={l.rate} onChange={e => setLine(i, { rate: e.target.value })} /></div>
          <div><label htmlFor={`sr-gst-${i}`}>Tax</label><select id={`sr-gst-${i}`} value={l.gst_code} onChange={e => setLine(i, { gst_code: e.target.value })}><option value="NONE">No GST</option><option value="SR">Standard-rate GST ({gstRate}%)</option></select></div>
        </div><p>Amount before GST: {formatCents(parsed[i].quantity * (parsed[i].unit_price_cents ?? 0))} · GST: {formatCents(creditGstCents(parsed[i].quantity * (parsed[i].unit_price_cents ?? 0), l.gst_code, gstRate))}</p>{lines.length > 1 && <button type="button" className="button-secondary" onClick={() => setLines(lines.filter((_, j) => j !== i))}>Remove item {i + 1}</button>}</fieldset>)}
        <div><label htmlFor="receipt-selected-account">Set account on selected items</label><select id="receipt-selected-account" value="" disabled={!lines.some(l => l.selected)} onChange={e => { const account = e.target.value; if (account) setLines(lines.map(l => l.selected ? { ...l, account_id: account } : l)); }}><option value="">Choose account</option>{revenue.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select> <button type="button" className="button-secondary" disabled={!lines.some(l => l.selected) || lines.every(l => l.selected)} onClick={() => setLines(lines.filter(l => !l.selected))}>Remove selected items</button></div>
        <button type="button" className="button-secondary" disabled={lines.length >= 200} onClick={() => setLines([...lines, blankLine()])}>Add new row</button><BulkLines id="bulk" room={200 - lines.length} accounts={revenue} onAccount={v => setLines(lines.map(l => ({ ...l, account_id: v })))} onAdd={rows => setLines([...lines.filter(l => l.description || l.rate), ...rows.map(r => ({ ...blankLine(), ...r }))])} />
        <label htmlFor="sr-scan">Scan item SKU</label><input id="sr-scan" maxLength={100} value={scan} onChange={e => setScan(e.target.value)} onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); addScannedItem(); } }} /><button type="button" className="button-secondary" onClick={addScannedItem}>Add scanned item</button><p className="section-help">Scan or type a saved item SKU, then choose an income account for the line.</p>{scanError && <p role="alert" className="form-error">{scanError}</p>}
        <p className="section-help">Whole quantities only. Rates exclude GST. Standard-rate GST cannot be combined with discounts, shipping or adjustments.</p>
      </fieldset>
      <fieldset disabled={busy}><legend>Totals, notes and terms</legend><div className="form-grid">
        <div><label htmlFor="sr-dtype">Discount type</label><select id="sr-dtype" value={form.discount_type} onChange={e => setForm({ ...form, discount_type: e.target.value, discount: "" })}><option value="flat">Amount (SGD)</option><option value="percent">Percent</option></select></div>
        <div><label htmlFor="sr-discount">Discount</label><input id="sr-discount" inputMode="decimal" value={form.discount} onChange={e => setForm({ ...form, discount: e.target.value })} /></div>
        <div><label htmlFor="sr-shipping">Shipping charges (SGD)</label><input id="sr-shipping" inputMode="decimal" value={form.shipping} onChange={e => setForm({ ...form, shipping: e.target.value })} /></div>
        <div><label htmlFor="sr-adj-label">Adjustment description</label><input id="sr-adj-label" maxLength={200} value={form.adjustment_label} onChange={e => setForm({ ...form, adjustment_label: e.target.value })} /></div>
        <div><label htmlFor="sr-adj">Adjustment (SGD, may be negative)</label><input id="sr-adj" inputMode="decimal" value={form.adjustment} onChange={e => setForm({ ...form, adjustment: e.target.value })} /></div>
      </div><p aria-live="polite">Sub total {formatCents(subtotal)} · GST {formatCents(gst)} · Total {formatCents(total)}</p>
        <p className="section-help">Discount and shipping are booked against the first item&apos;s income account; adjustment and a separate shipping account are not available here.</p>
        <label htmlFor="sr-notes">Notes</label><textarea id="sr-notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} />
        <label htmlFor="sr-terms">Terms &amp; conditions</label><textarea id="sr-terms" rows={3} maxLength={4000} value={form.terms} onChange={e => setForm({ ...form, terms: e.target.value })} />
        <p className="section-help">Save first, then attach up to 5 files from the receipt details.</p>
      </fieldset>
      <ExtrasForm kind="sales-receipts" orgId={orgId!} value={extras} onChange={setExtras} />
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {detail && orgId != null && <section className="record-form"><h2>{detail.receipt_number} — {detail.customer_name}</h2>
      <dl className="detail-grid"><div><dt>Date</dt><dd>{formatDate(detail.receipt_date)}</dd></div><div><dt>Total</dt><dd>{formatCents(detail.total_cents)}</dd></div><div><dt>Payment mode</dt><dd>{modeLabel(detail.payment_mode)}</dd></div><div><dt>Deposited to</dt><dd>{accounts.find(a => a.id === detail.deposit_account_id)?.name ?? "Account"}</dd></div><div><dt>Reference#</dt><dd>{detail.reference || "—"}</dd></div></dl>
      <div className="table-scroll"><table className="doc-table"><caption>Items</caption><thead><tr><th>Item</th><th>Quantity</th><th>Rate</th><th>Amount</th><th>GST</th></tr></thead><tbody>{detail.lines.map(l => <tr key={l.line_no}><td>{l.description}</td><td>{l.quantity}</td><td>{formatCents(l.unit_price_cents)}</td><td>{formatCents(l.amount_cents)}</td><td>{formatCents(l.gst_cents ?? 0)}</td></tr>)}</tbody></table></div>
      {detail.notes && <p className="long-text">{detail.notes}</p>}
      <PdfButton kind="sales-receipts" id={detail.id} orgId={orgId} /><Attachments key={detail.id} kind="sales-receipts" id={detail.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras kind="sales-receipts" id={detail.id} orgId={orgId} readOnly={!canWrite} />
      <button className="button-secondary" onClick={() => setDetail(null)}>Close details</button>
    </section>}
    {rows === null && !error && <p role="status">Loading sales receipts…</p>}{rows?.length === 0 && <p>No sales receipts yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 sales receipts</caption><thead><tr><th>Number</th><th>Customer</th><th>Date</th><th>Mode</th><th>Total</th><th>Details</th></tr></thead><tbody>{rows.map(n => <tr key={n.id}><td>{n.receipt_number}</td><td>{n.customer_name}</td><td>{n.receipt_date}</td><td>{modeLabel(n.payment_mode)}</td><td>{formatCents(n.total_cents)}</td><td><button className="button-secondary" onClick={() => { setDetail(n); setOpen(false); }}>View<span className="sr-only"> {n.receipt_number}</span></button></td></tr>)}</tbody></table></div>}
  </>;
}
