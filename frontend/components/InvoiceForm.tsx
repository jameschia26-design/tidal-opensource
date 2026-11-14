"use client";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import { ProjectIdSelect, ProjectSelect, SalespersonSelect, TermsSelect } from "@/components/ListSelects";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";
import CustomerPicker from "@/components/CustomerPicker";
import { api, type ApiError } from "@/lib/api";
import { parseCents, parseSignedCents, formatCents, formatMoneyInput } from "@/lib/money";
import BulkLines from "@/components/BulkLines";
import { messageFor } from "@/lib/errors";
import { scanItem } from "@/lib/item-scan";
import DateInput from "@/components/DateInput";
import { TrackingFields, trackingPayload, type TrackingValue } from "@/components/TrackingSelect";

type Line = { description: string; quantity: string; price: string; discount: string; gst_code: string; selected: boolean; item_id: number; account_id: number; project_id: number; tracking: TrackingValue };
export type Commercial = { subject: string; salesperson: string; project?: string; order_number: string; payment_terms_days: number; discount_type: string; discount_cents: number; discount_basis_points: number; shipping_cents: number; adjustment_cents: number; adjustment_description: string };
const blankCommercial = (): Commercial => ({ subject: "", salesperson: "", order_number: "", payment_terms_days: 0, discount_type: "flat", discount_cents: 0, discount_basis_points: 0, shipping_cents: 0, adjustment_cents: 0, adjustment_description: "" });
export type InvoiceValues = { commercial?: Commercial; amounts_are?: string; currency?: string; fx_rate_micro?: number; document_number?: string; quote_number?: string; contact_id: number; invoice_date: string; due_date: string; reference: string; notes: string; terms: string; lines: { description: string; quantity: number; unit_price_cents: number; gst_code?: string; item_id?: number; revenue_account_id?: number; project_id?: number; discount_basis_points?: number; tracking?: Record<string, number> }[] };
const emptyLine = (): Line => ({ description: "", quantity: "1", price: "", discount: "0", gst_code: "NONE", selected: false, item_id: 0, account_id: 0, project_id: 0, tracking: {} });
export default function InvoiceForm({ orgId, invoiceId, initial, onSaved, quote = false }: { orgId: number; invoiceId?: number; initial?: InvoiceValues; onSaved?: (id: number) => Promise<void>; quote?: boolean }) {
  const router = useRouter();
  const active = useRef(true);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const [contactId, setContactId] = useState(String(initial?.contact_id ?? ""));
  const [invoiceDate, setInvoiceDate] = useState(initial?.invoice_date ?? "");
  const [dueDate, setDueDate] = useState(initial?.due_date ?? "");
  const [reference, setReference] = useState(initial?.reference ?? "");
  const [notes, setNotes] = useState(initial?.notes ?? "Thanks for your business.");
  const [commercial, setCommercial] = useState(() => ({ ...blankCommercial(), ...initial?.commercial }));
  const [amountsAre, setAmountsAre] = useState(initial?.amounts_are === "inclusive" ? "inclusive" : "exclusive");
  const [currency, setCurrency] = useState(initial?.currency ?? "SGD");
  const [fxRate, setFxRate] = useState(initial?.fx_rate_micro && initial.fx_rate_micro !== 1000000 ? String(initial.fx_rate_micro / 1000000) : "");
  const [discount, setDiscount] = useState(() => ((initial?.commercial?.discount_type === "percent" ? initial?.commercial?.discount_basis_points : initial?.commercial?.discount_cents) ?? 0) / 100 + "");
  const [shipping, setShipping] = useState(() => ((initial?.commercial?.shipping_cents ?? 0) / 100).toFixed(2));
  const [adjustment, setAdjustment] = useState(() => ((initial?.commercial?.adjustment_cents ?? 0) / 100).toFixed(2));
  const [rounding, setRounding] = useState("none");
  const [roundingChoice, setRoundingChoice] = useState("none");
  const [preferencesOpen, setPreferencesOpen] = useState(false);
  const [preferencesError, setPreferencesError] = useState("");
  const [preferencesBusy, setPreferencesBusy] = useState(false);
  useEffect(() => { let live = true; api<{ rounding: string }>(`/v1/sales-rounding?org_id=${orgId}`).then(r => { if (live) { setRounding(r.rounding); setRoundingChoice(r.rounding); } }).catch((e: ApiError) => { if (live) setPreferencesError(messageFor(e.error, e.status)); }); return () => { live = false; }; }, [orgId]);
  async function savePreferences() {
    if (preferencesBusy) return;
    setPreferencesBusy(true); setPreferencesError("");
    try {
      const result = await api<{ rounding: string }>("/v1/sales-rounding", { method: "PUT", body: JSON.stringify({ org_id: orgId, rounding: roundingChoice }) });
      if (active.current) { setRounding(result.rounding); setPreferencesOpen(false); }
    } catch (e) { const err = e as ApiError; if (active.current) setPreferencesError(messageFor(err.error, err.status)); }
    finally { if (active.current) setPreferencesBusy(false); }
  }
  const [terms, setTerms] = useState(initial?.terms ?? "");
  const [lines, setLines] = useState<Line[]>(initial?.lines.map(l => ({ description: l.description, quantity: String(l.quantity), price: (l.unit_price_cents / 100).toFixed(2), discount: String((l.discount_basis_points ?? 0) / 100), gst_code: l.gst_code ?? "NONE", selected: false, item_id: l.item_id ?? 0, account_id: l.revenue_account_id ?? 0, project_id: l.project_id ?? 0, tracking: l.tracking ?? {} })) ?? [emptyLine()]);
  const [bulkQuantity, setBulkQuantity] = useState("");
  const [bulkPrice, setBulkPrice] = useState("");
  const [bulkError, setBulkError] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  // Files chosen before the document exists are held here and sent to the saved record, one request each.
  const [files, setFiles] = useState<File[]>([]);
  const [fileError, setFileError] = useState("");
  const madeId = useRef<number | null>(null);
  const [items, setItems] = useState<{ id: number; name: string; sku: string; sales_description: string; selling_price_cents: number }[]>([]);
  const [cols, setCols] = useState({ discount: true, tax: true, project: true, account: true });
  const [revenueAccounts, setRevenueAccounts] = useState<{ id: number; code: string; name: string; tax_rate_code?: string }[]>([]);
  const [taxRates, setTaxRates] = useState<{ code: string; name: string; rate_bp: number; kind: string }[]>([]);
  const [documentNumber, setDocumentNumber] = useState(initial?.document_number ?? initial?.quote_number ?? "");
  // Branding theme picked on the invoice; the company default applies when none is chosen.
  const [themes, setThemes] = useState<{ id: number; name: string; is_default: boolean }[]>([]);
  const [themeId, setThemeId] = useState("0");
  useEffect(() => {
    if (quote) return;
    let live = true;
    api<{ themes: { id: number; name: string; is_default: boolean }[] }>(`/v1/branding-themes?org_id=${orgId}`)
      .then(r => { if (live) setThemes(r.themes); })
      .catch(() => { /* the company default theme still prints */ });
    return () => { live = false; };
  }, [orgId, quote]);
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState("");
  function addScannedItem() {
    const item = scanItem(items, scan);
    if (!item) { setScanError("No unique item matches this SKU. Check the code or select an item manually."); return; }
    if (lines.length >= 200) { setScanError("A document can have at most 200 lines."); return; }
    const line: Line = { description: (item.sales_description || item.name).slice(0, 200), quantity: "1", price: (item.selling_price_cents / 100).toFixed(2), discount: "0", gst_code: "NONE", selected: false, item_id: item.id, account_id: 0, project_id: 0, tracking: {} };
    setLines(current => current.length === 1 && !current[0].description && !current[0].price ? [line] : [...current, line]);
    setScan(""); setScanError("");
  }
  useEffect(() => {
    let live = true;
    api<{ items: typeof items }>(`/v1/items?org_id=${orgId}`).then(r => { if (live) setItems(r.items); }).catch(() => { /* Free-text lines remain available if catalog cannot load. */ });
    return () => { live = false; };
  }, [orgId]);
  useEffect(() => {
    if (quote) return;
    let live = true;
    api<{ accounts: { id: number; code: string; name: string; type: string }[] }>(`/v1/accounts?org_id=${orgId}`)
      .then(r => { if (live) setRevenueAccounts(r.accounts.filter(a => a.type === "revenue")); })
      .catch(() => { /* Posting falls back to the Sales account. */ });
    return () => { live = false; };
  }, [orgId, quote]);
  useEffect(() => {
    if (invoiceId) return;
    let live = true;
    if (quote) {
      api<{ quote_prefix: string; quote_next: number }>(`/v1/document-numbering?org_id=${orgId}`)
        .then(r => { if (live) setDocumentNumber(`${r.quote_prefix}${String(r.quote_next).padStart(5, "0")}`); })
        .catch(() => { /* The server still numbers the quote when it is saved. */ });
    } else {
      api<{ document_number: string }>(`/v1/invoices/next-number?org_id=${orgId}`)
        .then(r => { if (live) setDocumentNumber(r.document_number); })
        .catch(() => { /* The server still numbers the draft when it is saved. */ });
    }
    return () => { live = false; };
  }, [orgId, quote, invoiceId]);
  // The catalogue is dated, so the rate follows the document date rather than a hard-coded table.
  useEffect(() => {
    let live = true;
    api<{ rates: { code: string; name: string; rate_bp: number; kind: string }[] }>(`/v1/tax/rates?org_id=${orgId}&on=${invoiceDate || new Date().toISOString().slice(0, 10)}`)
      .then(r => { if (live) setTaxRates(r.rates.filter(t => t.kind === "standard" || t.kind === "zero" || t.kind === "exempt" || t.kind === "out_of_scope" || t.kind === "no_tax")); })
      .catch(() => { if (live) setTaxRates([]); });
    return () => { live = false; };
  }, [orgId, invoiceDate]);
  const gstRate = (taxRates.find(t => t.code === "SR")?.rate_bp ?? (!invoiceDate ? 0 : invoiceDate >= "2024-01-01" ? 900 : invoiceDate >= "2023-01-01" ? 800 : 700)) / 100;
  // Tax-inclusive prices hold their GST, so the net is backed out of the discounted amount. Quotes are always tax exclusive.
  const inclusive = !quote && amountsAre === "inclusive";
  // Each line's discount is a percentage of that line, rounded half up to the cent like the server.
  const lineDiscountBP = (v: string) => { const n = Number(v); return v.trim() !== "" && Number.isInteger(n) && n >= 0 && n <= 100 ? n * 100 : null; };
  const parsed = lines.map(l => {
    const gross = (parseCents(l.price) ?? 0) * Number(l.quantity);
    const rawBP = lineDiscountBP(l.discount);
    const bp = rawBP ?? -1;
    const amount = rawBP === null ? gross : Math.floor((gross * (10000 - rawBP) + 5000) / 10000);
    const net = inclusive && l.gst_code === "SR" ? Math.floor((amount * 10000 + (10000 + gstRate * 100) / 2) / (10000 + gstRate * 100)) : amount;
    return { description: l.description.trim(), quantity: Number(l.quantity), unit: parseCents(l.price), gst_code: l.gst_code, discount_bp: bp, valid: rawBP !== null, net, tax: inclusive && l.gst_code === "SR" ? amount - net : 0 };
  });
  const subtotal = parsed.reduce((sum, l) => sum + l.net, 0);
  const discountValue = parseCents(discount), shippingValue = parseCents(shipping), adjustmentValue = parseSignedCents(adjustment);
  const discounted = commercial.discount_type === "percent" ? Number((BigInt(Number.isSafeInteger(subtotal) ? subtotal : 0) * BigInt(discountValue ?? 0) + 5000n) / 10000n) : (discountValue ?? 0);
  const total = subtotal - discounted + (shippingValue ?? 0) + (adjustmentValue ?? 0);
  const gst = parsed.reduce((sum, line) => sum + (line.gst_code === "SR" ? (inclusive ? line.tax : Math.round(line.net * gstRate / 100)) : 0), 0);
  const gstCommercialConflict = gst > 0 && (discountValue !== 0 || shippingValue !== 0 || adjustmentValue !== 0);
  const amountsOk = discountValue !== null && shippingValue !== null && adjustmentValue !== null && discounted <= subtotal && (commercial.discount_type !== "percent" || discountValue <= 10000);
  const formOk = amountsOk && !gstCommercialConflict && contactId !== "" && invoiceDate !== "" && dueDate >= invoiceDate && parsed.every(l => l.description && l.valid && Number.isInteger(l.quantity) && l.quantity >= 1 && l.quantity <= 1000000 && l.unit != null) && total > 0 && total + gst <= 100000000000000;
  const set = (i: number, k: keyof Line, v: string | number) => setLines(lines.map((l, index) => index === i ? { ...l, [k]: k === "account_id" || k === "project_id" ? Number(v) : v } : l));
  // Choosing an account applies that account's default tax rate; the Tax select still overrides it.
  function chooseAccount(i: number, accountId: string) {
    const code = revenueAccounts.find(a => String(a.id) === accountId)?.tax_rate_code ?? "";
    setLines(lines.map((line, index) => index === i ? { ...line, account_id: Number(accountId), gst_code: code || line.gst_code } : line));
  }
  function applyBulkEdit() {
    const chosen = lines.filter(l => l.selected).length;
    const quantity = bulkQuantity === "" ? null : Number(bulkQuantity);
    const price = bulkPrice === "" ? null : parseCents(bulkPrice);
    if (!chosen || (quantity == null && bulkPrice === "") || (quantity != null && (!/^\d{1,7}$/.test(bulkQuantity) || quantity < 1 || quantity > 1000000)) || (bulkPrice !== "" && (price == null || price < 0))) {
      setBulkError("Select lines, then enter a valid new quantity or unit price."); return;
    }
    setLines(current => current.map(line => line.selected ? { ...line, ...(quantity == null ? {} : { quantity: String(quantity) }), ...(price == null ? {} : { price: (price / 100).toFixed(2) }) } : line));
    setBulkQuantity(""); setBulkPrice(""); setBulkError("");
  }
  async function submit(e: FormEvent) {
    e.preventDefault(); if (busy || !formOk) return;
    setBusy(true); setError("");
    try {
      const resource = quote ? "quotes" : "invoices";
      const dates = quote ? { quote_date: invoiceDate, expiry_date: dueDate } : { invoice_date: invoiceDate, due_date: dueDate };
      const existingId = invoiceId ?? madeId.current;
      const created = await api<{ id: number }>(existingId ? `/v1/${resource}/${existingId}` : `/v1/${resource}`, { method: existingId ? "PUT" : "POST", body: JSON.stringify({ org_id: orgId, contact_id: Number(contactId), ...dates, reference, notes, terms, ...(quote ? { quote_number: documentNumber.trim() } : { document_number: documentNumber.trim(), branding_theme_id: Number(themeId) }), ...(quote ? {} : { amounts_are: amountsAre }), ...(currency === "SGD" ? {} : { currency, fx_rate_micro: Math.round(Number(fxRate) * 1000000) }), commercial: { ...commercial, discount_cents: commercial.discount_type === "percent" ? 0 : discountValue, discount_basis_points: commercial.discount_type === "percent" ? discountValue : 0, shipping_cents: shippingValue, adjustment_cents: adjustmentValue }, lines: parsed.map((l, i) => ({ description: l.description, quantity: l.quantity, unit_price_cents: l.unit, gst_code: l.gst_code, discount_basis_points: l.discount_bp, ...(lines[i]?.item_id ? { item_id: lines[i].item_id } : {}), ...(quote ? {} : { revenue_account_id: lines[i]?.account_id ?? 0, project_id: lines[i]?.project_id ?? 0 }), ...(trackingPayload(lines[i]?.tracking) ? { tracking: trackingPayload(lines[i]?.tracking) } : {}) })) }) });
      if (!invoiceId) { madeId.current = created.id; const bad = await saveExtras(quote ? "quotes" : "invoices", created.id, orgId, extras); if (bad) { setError(`${bad} Press Save again to retry the custom fields; the document itself is already saved.`); return; } }
      if (files.length) {
        const failed: string[] = [];
        for (const file of files) {
          const body = new FormData(); body.append("file", file);
          const response = await fetch(`/api/v1/entities/${resource}/${created.id}/attachments?org_id=${orgId}`, { method: "POST", credentials: "same-origin", body });
          if (!response.ok) failed.push(file.name);
        }
        setFiles([]);
        if (failed.length) { setError(`Saved. These files were not attached: ${failed.join(", ")}. Add them from the invoice page.`); return; }
      }
      if (!active.current) return;
      if (onSaved) await onSaved(created.id); else router.push(`/sales/invoices/${created.id}`);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
  }
  return <form className="record-form" onSubmit={submit}>
    <details open={preferencesOpen} onToggle={e => setPreferencesOpen(e.currentTarget.open)}><summary>Sales transaction preferences</summary>
      <p className="section-help">These settings apply to this company. Discount is available per line and at transaction level; shipping, adjustment and salesperson fields are always available. Hiding these fields is not available yet.</p>
      <label htmlFor="invoice-rounding-preference">Sales rounding</label><select id="invoice-rounding-preference" disabled={preferencesBusy} value={roundingChoice} onChange={e => setRoundingChoice(e.target.value)}><option value="none">No rounding</option><option value="whole">Nearest whole dollar</option><option value="5c">Nearest 5 cents</option><option value="10c">Nearest 10 cents</option></select>
      <p className="section-help">After saving this preference, use Round total to apply it to the current document. You can also change it in Settings.</p>
      {preferencesError && <p role="alert" className="form-error">{preferencesError}</p>}
      <button type="button" disabled={preferencesBusy} onClick={() => void savePreferences()}>{preferencesBusy ? "Saving…" : "Save and continue"}</button>
    </details>
    <fieldset disabled={busy}><legend>Customer and reference</legend><div className="form-grid"><div><CustomerPicker orgId={orgId} value={contactId} onChange={setContactId} /></div><div><label htmlFor="invoice-reference">Reference / purchase order <span className="field-optional">Optional</span></label><input id="invoice-reference" maxLength={100} value={reference} onChange={e => setReference(e.target.value)} /></div></div></fieldset>
    <fieldset disabled={busy}><legend>Document details</legend><div className="form-grid">{([ ["order_number", "Order number"], ["subject", "Subject"] ] as const).map(([key, label]) => <div key={key}><label htmlFor={`commercial-${key}`}>{label}</label><input id={`commercial-${key}`} maxLength={key === "order_number" ? 100 : 200} value={commercial[key]} onChange={e => setCommercial({ ...commercial, [key]: e.target.value })} /></div>)}<div><label htmlFor="commercial-salesperson">Salesperson</label><SalespersonSelect id="commercial-salesperson" orgId={orgId} value={commercial.salesperson} onChange={v => setCommercial({ ...commercial, salesperson: v })} /></div><div><label htmlFor="commercial-project">Project name</label><ProjectSelect id="commercial-project" orgId={orgId} contactId={contactId} value={commercial.project ?? ""} onChange={v => setCommercial({ ...commercial, project: v })} /></div><div><label htmlFor="commercial-terms">Payment terms</label><TermsSelect id="commercial-terms" orgId={orgId} days={commercial.payment_terms_days} onChange={days => { setCommercial({ ...commercial, payment_terms_days: days }); if (invoiceDate) { const date = new Date(`${invoiceDate}T00:00:00Z`); date.setUTCDate(date.getUTCDate() + days); setDueDate(date.toISOString().slice(0, 10)); } }} /></div><div><label htmlFor="document-number">{quote ? "Quote number" : "Invoice number"}</label><input id="document-number" maxLength={50} value={documentNumber} onChange={e => setDocumentNumber(e.target.value)} /><p className="section-help">Taken from your numbering settings. You can type another number; it must be unique and is fixed once the {quote ? "quote" : "invoice"} is saved.</p></div>{!quote && <div><label htmlFor="invoice-theme">Branding theme</label><select id="invoice-theme" value={themeId} onChange={e => setThemeId(e.target.value)}><option value="0">Company default</option>{themes.map(t => <option key={t.id} value={t.id}>{t.name}{t.is_default ? " (default)" : ""}</option>)}</select><p className="section-help">The chosen theme&apos;s footer prints on this invoice.</p></div>}</div></fieldset>
    <fieldset disabled={busy}><legend>{quote ? "Quote dates" : "Invoice dates"}</legend><div className="form-grid"><div><label htmlFor="inv-date">{quote ? "Quote date" : "Invoice date"}</label><DateInput id="inv-date" required value={invoiceDate} onChange={e => { setInvoiceDate(e.target.value); if (e.target.value) { const date = new Date(`${e.target.value}T00:00:00Z`); date.setUTCDate(date.getUTCDate() + commercial.payment_terms_days); setDueDate(date.toISOString().slice(0, 10)); } }}/></div><div><label htmlFor="due-date">{quote ? "Valid until" : "Due date"}</label><DateInput id="due-date" required min={invoiceDate} value={dueDate} onChange={e => setDueDate(e.target.value)}/></div></div></fieldset>
    {!quote && <fieldset disabled={busy}><legend>Currency</legend><div className="form-grid"><div><label htmlFor="invoice-currency">Currency</label><select id="invoice-currency" value={currency} onChange={e => { setCurrency(e.target.value); if (e.target.value === "SGD") setFxRate(""); }}><option value="SGD">SGD — Singapore Dollar</option><option value="USD">USD — US Dollar</option><option value="EUR">EUR — Euro</option><option value="GBP">GBP — Pound Sterling</option><option value="AUD">AUD — Australian Dollar</option><option value="JPY">JPY — Japanese Yen</option><option value="MYR">MYR — Malaysian Ringgit</option><option value="HKD">HKD — Hong Kong Dollar</option><option value="CNY">CNY — Chinese Yuan</option></select></div><div><label htmlFor="invoice-fx-rate">Rate to SGD <span className="field-optional">Required for a foreign currency</span></label><input id="invoice-fx-rate" inputMode="decimal" disabled={currency === "SGD"} placeholder={currency === "SGD" ? "1" : "e.g. 1.35"} value={fxRate} onChange={e => setFxRate(e.target.value)} /></div></div><p className="section-help">Amounts you type are {currency} and are recorded in the books at this rate. The rate is fixed once the invoice is posted.</p></fieldset>}
    <fieldset disabled={busy}><legend>Items and services</legend><details><summary>Columns</summary><p className="section-help">Untick a column to hide it on every line. Hidden values are kept and still save.</p><label className="check-label"><input type="checkbox" checked={cols.discount} onChange={e => setCols({ ...cols, discount: e.target.checked })} /> Disc. %</label><label className="check-label"><input type="checkbox" checked={cols.account} onChange={e => setCols({ ...cols, account: e.target.checked })} /> Account</label><label className="check-label"><input type="checkbox" checked={cols.project} onChange={e => setCols({ ...cols, project: e.target.checked })} /> Project</label><label className="check-label"><input type="checkbox" checked={cols.tax} onChange={e => setCols({ ...cols, tax: e.target.checked })} /> Tax</label></details>{!quote && <div className="form-grid"><div><label htmlFor="amounts-are">Amounts are</label><select id="amounts-are" value={amountsAre} onChange={e => setAmountsAre(e.target.value)}><option value="exclusive">Tax exclusive</option><option value="inclusive">Tax inclusive</option></select><p className="section-help">Tax exclusive: the typed price is before GST. Tax inclusive: the typed price already contains GST, so the net is worked back out of it.</p></div></div>}{lines.map((l, i) => <fieldset key={i}><legend>Line {i + 1}</legend><label className="check-label"><input type="checkbox" checked={l.selected} onChange={e => set(i, "selected", String(e.target.checked))} /> Select line {i + 1} for bulk actions</label><div className="form-grid"><div><label htmlFor={`invoice-item-${i}`}>Saved item</label><select id={`invoice-item-${i}`} value="" onChange={e => { const item = items.find(v => v.id === Number(e.target.value)); if (item) setLines(lines.map((line, index) => index === i ? { ...line, item_id: item.id, description: (item.sales_description || item.name).slice(0, 200), price: (item.selling_price_cents / 100).toFixed(2) } : line)); }}><option value="">Select item or enter description</option>{items.map(item => <option key={item.id} value={item.id}>{item.name}{item.sku ? ` (${item.sku})` : ""}</option>)}</select></div>{!quote && cols.account && <div><label htmlFor={`invoice-account-${i}`}>Account</label><select id={`invoice-account-${i}`} value={l.account_id} onChange={e => chooseAccount(i, e.target.value)}><option value="0">Sales (default)</option>{revenueAccounts.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>}<div><label htmlFor={`invoice-description-${i}`}>Description</label><input id={`invoice-description-${i}`} required maxLength={200} value={l.description} onChange={e => set(i, "description", e.target.value)} /></div><div><label htmlFor={`invoice-quantity-${i}`}>Quantity</label><input id={`invoice-quantity-${i}`} required type="number" min="1" max="1000000" step="1" value={l.quantity} onChange={e => set(i, "quantity", e.target.value)} /></div>{cols.discount && <div><label htmlFor={`invoice-discount-${i}`}>Disc. % <span className="field-optional">Optional</span></label><input id={`invoice-discount-${i}`} inputMode="decimal" placeholder="0" value={l.discount} onChange={e => set(i, "discount", e.target.value)} /></div>}<div><label htmlFor={`invoice-price-${i}`}>Unit price (SGD)</label><input id={`invoice-price-${i}`} required inputMode="decimal" pattern="[0-9,]+(\.[0-9]{1,2})?" value={l.price} onChange={e => set(i, "price", formatMoneyInput(e.target.value))} /></div>{!quote && cols.project && <div><label htmlFor={`invoice-project-${i}`}>Project <span className="field-optional">Optional</span></label><ProjectIdSelect id={`invoice-project-${i}`} orgId={orgId} contactId={contactId} value={l.project_id ? String(l.project_id) : ""} onChange={v => set(i, "project_id", v)} /></div>}{cols.tax && <div><label htmlFor={`invoice-gst-${i}`}>Tax</label><select id={`invoice-gst-${i}`} value={l.gst_code} onChange={e => set(i, "gst_code", e.target.value)}><option value="NONE">No GST</option>{taxRates.filter(t => t.code !== "NONE").map(t => <option key={t.code} value={t.code}>{t.name} ({t.rate_bp / 100}%)</option>)}</select></div>}<div><p className="section-help" id={`invoice-amount-${i}`}>Line amount</p><output htmlFor={`invoice-quantity-${i} invoice-price-${i}`} aria-labelledby={`invoice-amount-${i}`}>{formatCents(parsed[i].net + parsed[i].tax)}</output></div>{cols.tax && <div><p className="section-help" id={`invoice-tax-amount-${i}`}>Tax amount</p><output aria-labelledby={`invoice-tax-amount-${i}`}>{l.gst_code === "SR" ? formatCents(inclusive ? parsed[i].tax : Math.round(parsed[i].net * gstRate / 100)) : formatCents(0)}</output></div>}</div>{!quote && <TrackingFields orgId={orgId} idPrefix="invoice" lineNumber={i} value={l.tracking} disabled={busy} onChange={v => setLines(lines.map((x, index) => index === i ? { ...x, tracking: v } : x))} />}{lines.length > 1 && <button className="button-secondary" type="button" onClick={() => setLines(lines.filter((_, index) => index !== i))}>Remove line {i + 1}</button>}</fieldset>)}<div className="form-grid"><div><label htmlFor="bulk-quantity">Set selected quantity</label><input id="bulk-quantity" inputMode="numeric" value={bulkQuantity} onChange={e => setBulkQuantity(e.target.value)} /></div><div><label htmlFor="bulk-price">Set selected unit price (SGD)</label><input id="bulk-price" inputMode="decimal" value={bulkPrice} onChange={e => setBulkPrice(formatMoneyInput(e.target.value))} /></div><div><button type="button" className="button-secondary" disabled={!lines.some(l => l.selected)} onClick={applyBulkEdit}>Apply to selected lines</button><button type="button" className="button-secondary" disabled={!lines.some(l => l.selected) || lines.every(l => l.selected)} onClick={() => setLines(lines.filter(l => !l.selected))}>Remove selected lines</button></div></div>{bulkError && <p role="alert" className="form-error">{bulkError}</p>}<button className="button-secondary" type="button" disabled={lines.length >= 200} onClick={() => setLines([...lines, emptyLine()])}>Add line</button><BulkLines id="bulk" room={200 - lines.length} {...(!quote ? { accounts: revenueAccounts, onAccount: (v: string) => setLines(lines.map(l => ({ ...l, account_id: Number(v) }))) } : {})} onAdd={rows => setLines([...lines.filter(l => l.description || l.price), ...rows.map(r => ({ ...emptyLine(), description: r.description, quantity: r.quantity, price: r.rate }))])} /><p aria-live="polite">Subtotal{inclusive ? " (incl. GST)" : ""}: <strong>{formatCents(subtotal + (inclusive ? gst : 0))}</strong> · GST: <strong>{formatCents(gst)}</strong> · Total: <strong>{formatCents(total + gst)}</strong></p>{gstCommercialConflict && <p role="alert" className="form-error">GST {quote ? "quotes" : "invoices"} cannot include discounts, shipping or adjustments yet.</p>}{rounding !== "none" && gst === 0 && (() => { const step = rounding === "whole" ? 100 : rounding === "5c" ? 5 : 10; const diff = Math.round(total / step) * step - total; return <p><button type="button" className="button-secondary" disabled={diff === 0 || adjustmentValue === null} onClick={() => { setAdjustment((((adjustmentValue ?? 0) + diff) / 100).toFixed(2)); if (!commercial.adjustment_description) setCommercial({ ...commercial, adjustment_description: "Rounding" }); }}>Round total{diff !== 0 ? ` (${diff > 0 ? "+" : ""}${(diff / 100).toFixed(2)})` : ""}</button> <span className="section-help">Company rule: {rounding === "whole" ? "nearest whole dollar" : rounding === "5c" ? "nearest 5 cents" : "nearest 10 cents"}. Adds the difference to the adjustment.</span></p>; })()}</fieldset>
    <fieldset disabled={busy}><legend>Scan item</legend><label htmlFor="invoice-scan">Item SKU</label><input id="invoice-scan" value={scan} maxLength={100} onChange={e => setScan(e.target.value)} onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); addScannedItem(); } }} /><button type="button" className="button-secondary" onClick={addScannedItem}>Add scanned item</button><p className="section-help">Use a barcode scanner that types the saved item SKU, or type the SKU and press Enter.</p>{scanError && <p role="alert" className="form-error">{scanError}</p>}</fieldset>
    <fieldset disabled={busy}><legend>Totals and adjustments</legend><p>Subtotal: {formatCents(subtotal)}</p><div className="form-grid"><div><label htmlFor="discount-type">Discount type</label><select id="discount-type" value={commercial.discount_type} onChange={e => { setCommercial({ ...commercial, discount_type: e.target.value }); setDiscount("0"); }}><option value="flat">Amount (SGD)</option><option value="percent">Percentage</option></select></div><div><label htmlFor="discount">Discount</label><input id="discount" inputMode="decimal" value={discount} onChange={e => setDiscount(e.target.value)} /></div><div><label htmlFor="shipping">Shipping charges (SGD)</label><input id="shipping" inputMode="decimal" value={shipping} onChange={e => setShipping(formatMoneyInput(e.target.value))} /></div><div><label htmlFor="adjustment-label">Adjustment description</label><input id="adjustment-label" maxLength={200} value={commercial.adjustment_description} onChange={e => setCommercial({ ...commercial, adjustment_description: e.target.value })} /></div><div><label htmlFor="adjustment">Adjustment (SGD)</label><input id="adjustment" inputMode="decimal" value={adjustment} onChange={e => setAdjustment(formatMoneyInput(e.target.value))} /></div></div><p aria-live="polite">Total: <strong>{formatCents(total + gst)}</strong></p><p className="section-help">Current posting uses Accounts Receivable, Sales and GST Output. Separate shipping and adjustment accounts are not yet available.</p></fieldset>
    <fieldset disabled={busy}><legend>Customer notes and terms</legend><label htmlFor="invoice-notes">Notes <span className="field-optional">Optional</span></label><textarea id="invoice-notes" maxLength={4000} rows={3} value={notes} onChange={e => setNotes(e.target.value)} /><label htmlFor="invoice-terms">Payment terms / instructions <span className="field-optional">Optional</span></label><textarea id="invoice-terms" maxLength={4000} rows={3} value={terms} onChange={e => setTerms(e.target.value)} /></fieldset>
    {!invoiceId && <ExtrasForm kind={quote ? "quotes" : "invoices"} orgId={orgId} value={extras} onChange={setExtras} tags={quote} />}
    <fieldset disabled={busy}><legend>Files <span className="field-optional">Optional</span></legend><label htmlFor="invoice-files">Attach documents</label><input id="invoice-files" type="file" multiple onChange={e => { const chosen = Array.from(e.target.files ?? []); const tooBig = chosen.filter(f => f.size === 0 || f.size > 10 * 1024 * 1024); setFiles(chosen.filter(f => !tooBig.includes(f)).slice(0, 10)); setFileError(tooBig.length ? "Each file must be nonempty and no larger than 10 MB." : chosen.length > 10 ? "Up to 10 files per document." : ""); }} /><p className="section-help">Files are attached to the saved {quote ? "quote" : "invoice"}. Up to 10 files, 10 MB each, visible to company members.</p>{files.length > 0 && <p>{files.map(f => f.name).join(", ")}</p>}{fileError && <p role="alert" className="form-error">{fileError}</p>}</fieldset>
    <details><summary>Preview</summary><p className="section-help">Live preview of this {quote ? "quote" : "invoice"} before saving.</p><p><strong>{quote ? "Quote" : "Invoice"} {documentNumber}</strong> · {invoiceDate || "No date"} → {dueDate || "No due date"}</p><div className="table-scroll"><table className="doc-table"><caption className="sr-only">Preview lines</caption><thead><tr><th scope="col">Description</th><th scope="col" className="num">Qty</th><th scope="col" className="num">Unit price</th><th scope="col" className="num">Amount</th><th scope="col" className="num">Tax</th></tr></thead><tbody>{parsed.map((l, i) => <tr key={i}><td>{l.description || "—"}</td><td className="num">{l.quantity}</td><td className="num">{l.unit == null ? "—" : formatCents(l.unit)}</td><td className="num">{l.unit == null ? "—" : formatCents(l.net + l.tax)}</td><td className="num">{l.gst_code === "SR" ? formatCents(inclusive ? l.tax : Math.round(l.net * gstRate / 100)) : "—"}</td></tr>)}</tbody></table></div><p aria-live="polite">Subtotal{inclusive ? " (incl. GST)" : ""}: <strong>{formatCents(subtotal + (inclusive ? gst : 0))}</strong> · GST: <strong>{formatCents(gst)}</strong> · Total: <strong>{formatCents(total + gst)}</strong></p></details>
    <p className="section-help">Online payments are not connected. <a href="/settings">Connect a payment service in Settings</a> to let customers pay online.</p>
    {error && <p className="form-error" role="alert">{error}</p>}<button disabled={busy || !formOk}>{busy ? "Saving…" : "Save draft"}</button>
  </form>;
}
