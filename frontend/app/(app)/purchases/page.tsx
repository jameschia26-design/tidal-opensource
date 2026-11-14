"use client";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import Link from "next/link";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { TermsSelect, cashBankAccounts } from "@/components/ListSelects";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import BillCorrections from "@/components/BillCorrections";
import Extras from "@/components/Extras";
import PdfButton from "@/components/PdfButton";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import BulkLines from "@/components/BulkLines";
import { messageFor } from "@/lib/errors";
import { parseCents, formatCents, formatMoneyInput } from "@/lib/money";
import { keyFor, clearKey } from "@/lib/payment-key";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";
import { TrackingFields, trackingPayload, type TrackingValue } from "@/components/TrackingSelect";

type Bill = { id: number; document_number: string; lifecycle_status: string; contact_id: number; supplier_name: string; supplier_reference: string; bill_date: string; due_date: string; planned_date: string; permit_number: string; paid: boolean; notes: string; order_number: string; subject: string; payment_terms_days: number; discount_account_id: number | null; status: string; total_cents: number; balance_cents: number };
type Counts = { all: number; draft: number; awaiting_approval: number; awaiting_payment: number; paid: number; voided: number };
const BILL_TABS: { id: keyof Counts; label: string }[] = [
  { id: "all", label: "All" },
  { id: "draft", label: "Draft" },
  { id: "awaiting_approval", label: "Awaiting approval" },
  { id: "awaiting_payment", label: "Awaiting payment" },
  { id: "paid", label: "Paid" },
  { id: "voided", label: "Voided" },
];
type Line = { description: string; quantity: number; unit_price_cents: number; account_id: number; account_code: string; account_name: string; amount_cents: number; line_no: number; gross_cents: number; discount_cents: number; discount_basis_points: number; customer_id: number | null; discount_account_id: number | null; item_id?: number; gst_code?: string; gst_rate_bp?: number; gst_cents?: number; tracking?: Record<string, number> };
type Payment = { id: number; amount_cents: number; payment_date: string; reference: string };
type Detail = { bill: Bill; lines: Line[]; payments: Payment[] };
type Contact = { id: number; name: string; payment_terms_days: number; bill_payment_terms_days: number | null };
type Account = { id: number; code: string; name: string; type: string; tax_rate_code?: string };
type FormLine = { description: string; quantity: string; price: string; account: string; discount: string; discountType: "flat" | "percent"; customer: string; discountAccount: string; item_id: number; gst: string; tracking: TrackingValue };
const addDays = (day: string, n: number) => { if (!day || !Number.isInteger(n)) return day; const d = new Date(`${day}T00:00:00Z`); d.setUTCDate(d.getUTCDate() + n); return d.toISOString().slice(0, 10); };
const blankLine = (): FormLine => ({ description: "", quantity: "1", price: "", account: "", discount: "", discountType: "flat", customer: "", discountAccount: "", item_id: 0, gst: "NONE", tracking: {} });

export default function PurchasesPage() {
  const { selected } = useOrg();
  return <><nav className="form-actions" aria-label="Purchases"><Link href="/purchases" aria-current="page">Bills</Link><Link href="/purchases/supplier-credits">Supplier credits</Link><Link href="/purchases/suppliers">Suppliers</Link></nav>{!selected ? <><PageHeader title="Bills" description="Draft, post and pay supplier bills; per-line items and accounts." /><p className="notice">Choose a company first.</p></> : <BillsWorkspace key={`${selected.id}-${selected.role}`} orgId={selected.id} canWrite={selected.role !== "viewer"} />}</>;
}

function BillsWorkspace({ orgId, canWrite }: { orgId: number; canWrite: boolean }) {
  const active = useRef(true);
  const [rows, setRows] = useState<Bill[] | null>(null);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [customers, setCustomers] = useState<Contact[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [allAccounts, setAllAccounts] = useState<Account[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [filter, setFilter] = useState<keyof Counts>("all");
  const [counts, setCounts] = useState<Counts | null>(null);
  const [documentNumber, setDocumentNumber] = useState("");
  const [numbering, setNumbering] = useState<{ bill_prefix: string; bill_next: number } | null>(null);
  const [search, setSearch] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [dateType, setDateType] = useState<"bill_date" | "due_date">("bill_date");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [supplier, setSupplier] = useState("");
  const [reference, setReference] = useState("");
  const [currency, setCurrency] = useState("SGD");
  const [fxRate, setFxRate] = useState("");
  const [issued, setIssued] = useState("");
  const [due, setDue] = useState("");
  const [notes, setNotes] = useState("");
  const [orderNumber, setOrderNumber] = useState("");
  const [subject, setSubject] = useState("");
  const [terms, setTerms] = useState("0");
  const [discountAccount, setDiscountAccount] = useState("");
  const [repeatEvery, setRepeatEvery] = useState("1");
  const [repeatUnit, setRepeatUnit] = useState("month");
  const [repeatEnds, setRepeatEnds] = useState("");
  const [repeatMode, setRepeatMode] = useState("draft");
  const [repeatAuto, setRepeatAuto] = useState(false);
  const [lines, setLines] = useState<FormLine[]>([blankLine()]);
  const [permitNumber, setPermitNumber] = useState("");
  const [plannedDate, setPlannedDate] = useState("");
  const [forwarding, setForwarding] = useState("");
  const [selected, setSelected] = useState<number[]>([]);
  const [batchDate, setBatchDate] = useState("");
  const [batchReference, setBatchReference] = useState("");
  const [batchAccount, setBatchAccount] = useState("");
  const [overpayment, setOverpayment] = useState(false);
  const [overpaymentDescription, setOverpaymentDescription] = useState("");
  const [payAmount, setPayAmount] = useState("");
  const [payDate, setPayDate] = useState("");
  const [payReference, setPayReference] = useState("");
  const fail = (e: unknown) => { if (active.current) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } };
  async function refresh() {
    const response = await api<{ bills: Bill[]; counts: Counts; forwarding_address: string }>(`/v1/bills?org_id=${orgId}`);
    if (active.current) { setRows(response.bills); setCounts(response.counts); setForwarding(response.forwarding_address); }
  }
  async function show(id: number) {
    setLoading(true); setError("");
    try {
      const response = await api<Detail>(`/v1/bills/${id}?org_id=${orgId}`);
      if (!active.current) return;
      setDetail(response); setOpen(false); setPayAmount(""); setPayDate(""); setPayReference(""); setOverpayment(false); setOverpaymentDescription("");
    } catch (e) { fail(e); } finally { if (active.current) setLoading(false); }
  }
  const [catalog, setCatalog] = useState<{ id: number; name: string; purchase_description: string; cost_price_cents: number; purchase_account_id: number }[]>([]);
  const [taxRates, setTaxRates] = useState<{ code: string; name: string; rate_bp: number; kind: string }[]>([]);
  useEffect(() => { let live = true; api<{ rates: typeof taxRates }>(`/v1/tax/rates?org_id=${orgId}&on=${issued || new Date().toISOString().slice(0, 10)}`).then(r => { if (live) setTaxRates(r.rates); }).catch(() => { if (live) setTaxRates([]); }); return () => { live = false; }; }, [orgId, issued]);
  useEffect(() => { let live = true; api<{ items: typeof catalog }>(`/v1/items?org_id=${orgId}`).then(r => { if (live) setCatalog(r.items); }).catch(() => { /* Free-text lines stay available. */ }); return () => { live = false; }; }, [orgId]);
  useEffect(() => {
    active.current = true;
    Promise.all([api<{ bills: Bill[]; counts: Counts; forwarding_address: string }>(`/v1/bills?org_id=${orgId}`), api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=supplier`), api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`), api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`), api<{ bill_prefix: string; bill_next: number }>(`/v1/document-numbering?org_id=${orgId}`)])
      .then(([b, c, a, cu, n]) => { if (active.current) { setCustomers(cu.contacts); setRows(b.bills); setCounts(b.counts); setForwarding(b.forwarding_address); setContacts(c.contacts); setAllAccounts(a.accounts); setAccounts(a.accounts.filter(account => account.type === "expense")); setNumbering(n); } }).catch(fail);
    return () => { active.current = false; };
  }, [orgId]);
  function start(edit: boolean) {
    const d = edit ? detail : null;
    setExtras(noExtras());
    setEditingId(d?.bill.id ?? null); setDocumentNumber(d?.bill.document_number ?? (numbering ? `${numbering.bill_prefix}${String(numbering.bill_next).padStart(5, "0")}` : "")); setSupplier(d ? String(d.bill.contact_id) : ""); setReference(d?.bill.supplier_reference ?? ""); setIssued(d?.bill.bill_date ?? ""); setDue(d?.bill.due_date ?? ""); setNotes(d?.bill.notes ?? ""); setOrderNumber(d?.bill.order_number ?? ""); setSubject(d?.bill.subject ?? ""); setTerms(String(d?.bill.payment_terms_days ?? 0));
    setDiscountAccount(d?.bill.discount_account_id ? String(d.bill.discount_account_id) : "");
    setPermitNumber(d?.bill.permit_number ?? ""); setPlannedDate(d?.bill.planned_date ?? "");
    setLines(d ? d.lines.map(line => ({ description: line.description, quantity: String(line.quantity), price: (line.unit_price_cents / 100).toFixed(2), account: String(line.account_id), discountType: line.discount_basis_points > 0 ? "percent" : "flat", discount: line.discount_basis_points > 0 ? String(line.discount_basis_points / 100) : line.discount_cents > 0 ? (line.discount_cents / 100).toFixed(2) : "", customer: line.customer_id ? String(line.customer_id) : "", discountAccount: line.discount_account_id ? String(line.discount_account_id) : "", item_id: line.item_id ?? 0, gst: line.gst_code ?? "NONE", tracking: line.tracking ?? {} })) : [blankLine()]);
    setOpen(true); setError(""); setNotice("");
  }
  const parsed = lines.map(line => {
    const price = parseCents(line.price), qty = Number(line.quantity), gross = qty * (price ?? 0);
    const pct = line.discountType === "percent" ? Math.round(Number(line.discount || 0) * 100) : 0;
    const flat = line.discountType === "flat" ? parseCents(line.discount || "0") : 0;
    const discount = pct ? Math.round(gross * pct / 10000) : (flat ?? NaN);
    return { description: line.description.trim(), quantity: qty, unit_price_cents: price, ...(line.item_id ? {} : { account_id: Number(line.account) }), discount_cents: line.discountType === "flat" ? flat : 0, discount_basis_points: pct, customer_id: line.customer ? Number(line.customer) : null, discount_account_id: line.discountAccount ? Number(line.discountAccount) : null, ...(line.item_id ? { item_id: line.item_id } : {}), gst_code: line.gst, ...(trackingPayload(line.tracking) ? { tracking: trackingPayload(line.tracking) } : {}), net: gross - discount, bad: !(discount >= 0 && discount <= gross) || pct > 10000 || Number.isNaN(pct) };
  });
  const subtotal = parsed.reduce((sum, line) => sum + line.quantity * (line.unit_price_cents ?? 0), 0);
  const total = parsed.reduce((sum, line) => sum + line.net, 0);
  const rateFor = (code: string) => taxRates.find(t => t.code === code)?.rate_bp ?? (code === "SR" ? (issued >= "2024-01-01" ? 900 : issued >= "2023-01-01" ? 800 : 700) : 0);
  const kindFor = (code: string) => taxRates.find(t => t.code === code)?.kind ?? (code === "SR" ? "standard" : "no_tax");
  const gst = parsed.reduce((sum, line) => sum + (kindFor(line.gst_code) === "standard" && line.gst_code !== "NONE" ? Math.round(line.net * rateFor(line.gst_code) / 10000) : 0), 0);
  const selfGst = parsed.reduce((sum, line) => sum + (["import", "reverse_charge"].includes(kindFor(line.gst_code)) ? Math.round(line.net * rateFor(line.gst_code) / 10000) : 0), 0);
  const valid = supplier !== "" && /^\d{1,3}$/.test(terms) && Number(terms) <= 365 && reference.trim() !== "" && issued !== "" && due >= issued && parsed.every(line => line.description && (line.item_id ? true : (line.account_id ?? 0) > 0) && Number.isInteger(line.quantity) && line.quantity >= 1 && line.quantity <= 1000000 && line.unit_price_cents !== null && line.discount_cents !== null && !line.bad) && total > 0 && total <= 100000000000000;
  function changeLine(index: number, field: keyof FormLine, value: string) { setLines(lines.map((line, i) => i === index ? { ...line, [field]: value } : line)); }
  async function save(e: FormEvent) {
    e.preventDefault(); if (busy || !valid) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const saved = await api<{ id: number }>(editingId ? `/v1/bills/${editingId}` : "/v1/bills", { method: editingId ? "PUT" : "POST", body: JSON.stringify({ org_id: orgId, contact_id: Number(supplier), document_number: documentNumber.trim(), supplier_reference: reference, bill_date: issued, due_date: due, notes, order_number: orderNumber.trim(), subject: subject.trim(), payment_terms_days: Number(terms), permit_number: permitNumber.trim(), planned_date: plannedDate, discount_account_id: discountAccount ? Number(discountAccount) : null, ...(currency === "SGD" ? {} : { currency, fx_rate_micro: Math.round(Number(fxRate) * 1000000) }), lines: parsed.map(({ net, bad, ...l }) => l) }) });
      const bad = editingId ? "" : await saveExtras("bills", saved.id, orgId, extras); setExtras(noExtras());
      if (!active.current) return;
      setOpen(false); setNotice("Bill draft saved." + (bad ? " " + bad : "")); await refresh(); await show(saved.id);
    } catch (e) { fail(e); } finally { if (active.current) setBusy(false); }
  }
  async function makeRecurring() {
    if (busy || !valid || editingId) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const bill = { contact_id: Number(supplier), supplier_reference: reference, notes, order_number: orderNumber.trim(), subject: subject.trim(), payment_terms_days: Number(terms), discount_account_id: discountAccount ? Number(discountAccount) : null, lines: parsed.map(({ net, bad, ...l }) => l) };
      const r = await api<{ next_run: string }>("/v1/recurring-bills", { method: "POST", body: JSON.stringify({ org_id: orgId, name: `${reference} — ${contacts.find(c => c.id === Number(supplier))?.name ?? "supplier"}`, repeat_every: Number(repeatEvery), repeat_unit: repeatUnit, start_date: issued, ends_on: repeatEnds, mode: repeatMode, auto_generate: repeatAuto, bill }) });
      if (!active.current) return;
      setOpen(false); setNotice(`Recurring bill saved. First draft is due ${r.next_run}. Run it from Recurring bills.`);
    } catch (e) { fail(e); } finally { if (active.current) setBusy(false); }
  }
  async function post() {
    if (!detail || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/bills/${detail.bill.id}/post?org_id=${orgId}`, { method: "POST" });
      if (!active.current) return;
      setNotice("Bill posted. Expenses and amount owed are now recorded."); await refresh(); await show(detail.bill.id);
    } catch (e) { fail(e); } finally { if (active.current) setBusy(false); }
  }
  async function pay(e: FormEvent) {
    e.preventDefault(); if (!detail || busy) return;
    const amount = parseCents(payAmount);
    if (amount === null || amount <= 0 || !payDate || payDate < detail.bill.bill_date) { setError("Enter a payment dated on or after the bill."); return; }
    const over = amount > detail.bill.balance_cents;
    if (over && !overpayment) { setError("This is more than the bill still owes. Tick the overpayment confirmation below if that is intended."); return; }
    if (over && overpaymentDescription.trim() === "") { setError("Say what the extra payment is for."); return; }
    const payload = { org_id: orgId, amount_cents: amount, payment_date: payDate, reference: payReference, ...(over ? { overpayment: true, overpayment_description: overpaymentDescription.trim() } : {}) };
    const slot = `bill-${orgId}-${detail.bill.id}`;
    const key = keyFor(slot, JSON.stringify(payload));
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/bills/${detail.bill.id}/payments`, { method: "POST", body: JSON.stringify({ ...payload, idempotency_key: key }) });
      clearKey(slot); if (!active.current) return;
      setNotice("Payment recorded. No money was transferred by Tidal."); await refresh(); await show(detail.bill.id);
    } catch (e) {
      const err = e as ApiError;
      if (err.status >= 400 && err.status < 500 && ![408, 409, 429].includes(err.status)) clearKey(slot);
      fail(e);
    } finally { if (active.current) setBusy(false); }
  }
  async function bulk(action: "submit" | "approve") {
    if (busy || !selected.length) return;
    setBusy(true); setError(""); setNotice("");
    try {
      for (const id of selected) {
        const bill = rows?.find(r => r.id === id);
        if (!bill) continue;
        if (action === "submit" && bill.status === "draft") {
          await api(`/v1/bills/${id}/submit?org_id=${orgId}`, { method: "POST" });
        } else if (action === "approve" && (bill.status === "draft" || bill.status === "awaiting_approval")) {
          await api(`/v1/bills/${id}/post?org_id=${orgId}`, { method: "POST" });
        }
      }
      if (!active.current) return;
      setSelected([]); setNotice(action === "submit" ? "Selected bills sent for approval." : "Selected bills approved and posted."); await refresh();
    } catch (e) { fail(e); } finally { if (active.current) setBusy(false); }
  }
  const selectedBills = rows?.filter(r => selected.includes(r.id)) ?? [];
  const selectedPayable = selectedBills.filter(r => r.status === "posted" && r.balance_cents > 0);
  async function batchPay(e: FormEvent) {
    e.preventDefault(); if (busy || !selectedPayable.length || !batchDate) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const made = await api<{ total_cents: number }>("/v1/bill-payments/batch", { method: "POST", body: JSON.stringify({ org_id: orgId, payment_date: batchDate, reference: batchReference, idempotency_key: keyFor(`batch-${orgId}`, JSON.stringify({ batchDate, batchReference, ids: selectedPayable.map(b => b.id) })), ...(batchAccount ? { account_id: Number(batchAccount) } : {}), allocations: selectedPayable.map(b => ({ bill_id: b.id, amount_cents: b.balance_cents })) }) });
      if (!active.current) return;
      setSelected([]); setBatchDate(""); setBatchReference(""); setNotice(`One payment of ${formatCents(made.total_cents)} recorded across ${selectedPayable.length} bills.`); await refresh();
    } catch (e) { fail(e); } finally { if (active.current) setBusy(false); }
  }
  const visible = rows?.filter(b => (filter === "all" || (b.lifecycle_status || b.status) === filter) && (!from || b[dateType] >= from) && (!to || b[dateType] <= to) && `${b.supplier_name} ${b.supplier_reference}`.toLowerCase().includes(search.toLowerCase()));
  const b = detail?.bill;
  return <>
    <PageHeader title="Supplier bills" description="Record bills, review drafts, then post expenses and record payments." actions={canWrite ? <button disabled={busy || loading || open || rows === null} onClick={() => start(false)}>New bill</button> : undefined} />
    {rows && rows.length >= 200 && <p className="notice">Showing the latest 200 bills. Narrow the dates or search to reach older bills.</p>}
    <p className="notice">GST-registered companies can select every Singapore purchase tax code per bill line, including imported services, low-value goods and reverse charge, which are self-assessed and do not increase what the supplier is paid. Stock-item lines move into inventory and fixed-asset lines become assets, so the ledger and the stock report agree. Payments record money already paid; they do not send money.</p>
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {open && <form className="record-form" onSubmit={save}>
      <h2>{editingId ? "Edit draft bill" : "New bill"}</h2>
      <fieldset disabled={busy}><legend>Supplier and bill reference</legend><div className="form-grid"><div><label htmlFor="bill-supplier">Supplier</label><select id="bill-supplier" required value={supplier} onChange={e => { setSupplier(e.target.value); const c = contacts.find(x => x.id === Number(e.target.value)); if (c) { const d = c.bill_payment_terms_days ?? c.payment_terms_days; setTerms(String(d)); setDue(addDays(issued, d)); } }}><option value="">Choose supplier</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select><p className="section-help"><Link href="/purchases/suppliers">Manage suppliers</Link></p></div><div><label htmlFor="bill-reference">Supplier invoice number / reference</label><input id="bill-reference" required maxLength={100} value={reference} onChange={e => setReference(e.target.value)} /><p className="section-help">Must be unique for this supplier.</p></div><div><label htmlFor="bill-number">Bill number</label><input id="bill-number" maxLength={50} value={documentNumber} onChange={e => setDocumentNumber(e.target.value)} /><p className="section-help">From your numbering settings. Editable while this is a draft; unique in this company.</p></div><div><label htmlFor="bill-order">Order number <span className="field-optional">Optional</span></label><input id="bill-order" maxLength={100} value={orderNumber} onChange={e => setOrderNumber(e.target.value)} /><p className="section-help">Your purchase order reference.</p></div><div><label htmlFor="bill-subject">Subject <span className="field-optional">Optional</span></label><input id="bill-subject" maxLength={200} value={subject} onChange={e => setSubject(e.target.value)} /></div><div><label htmlFor="bill-permit">Permit number <span className="field-optional">Optional</span></label><input id="bill-permit" maxLength={50} value={permitNumber} onChange={e => setPermitNumber(e.target.value)} /><p className="section-help">Singapore import permit number, if the supplier quoted one.</p></div><div><label htmlFor="bill-planned">Planned date <span className="field-optional">Optional</span></label><DateInput id="bill-planned" value={plannedDate} onChange={e => setPlannedDate(e.target.value)} /><p className="section-help">When you expect to pay, shown on the bills list.</p></div></div><p className="section-help">Save the draft, then use Upload files in the bill details to attach the supplier&apos;s bill or PDF.</p></fieldset>
      <fieldset disabled={busy}><legend>Dates</legend><div className="form-grid"><div><label htmlFor="bill-date">Bill date</label><DateInput id="bill-date" required value={issued} onChange={e => { setIssued(e.target.value); setDue(addDays(e.target.value, Number(terms))); }}/></div><div><label htmlFor="bill-terms">Payment terms</label><TermsSelect id="bill-terms" orgId={orgId} days={Number(terms) || 0} onChange={d => { setTerms(String(d)); setDue(addDays(issued, d)); }} /><p className="section-help">Changing terms recalculates the due date.</p></div><div><label htmlFor="bill-due">Due date</label><DateInput id="bill-due" required min={issued} value={due} onChange={e => setDue(e.target.value)}/></div></div></fieldset>
      <fieldset disabled={busy}><legend>Currency</legend><div className="form-grid"><div><label htmlFor="bill-currency">Currency</label><select id="bill-currency" value={currency} onChange={e => { setCurrency(e.target.value); if (e.target.value === "SGD") setFxRate(""); }}><option value="SGD">SGD — Singapore Dollar</option><option value="USD">USD — US Dollar</option><option value="EUR">EUR — Euro</option><option value="GBP">GBP — Pound Sterling</option><option value="AUD">AUD — Australian Dollar</option><option value="JPY">JPY — Japanese Yen</option><option value="MYR">MYR — Malaysian Ringgit</option><option value="HKD">HKD — Hong Kong Dollar</option><option value="CNY">CNY — Chinese Yuan</option></select></div><div><label htmlFor="bill-fx-rate">Rate to SGD <span className="field-optional">Required for a foreign currency</span></label><input id="bill-fx-rate" inputMode="decimal" disabled={currency === "SGD"} placeholder={currency === "SGD" ? "1" : "e.g. 1.35"} value={fxRate} onChange={e => setFxRate(e.target.value)} /></div></div><p className="section-help">Amounts you type are {currency} and are recorded in the books at this rate. The rate is fixed once the bill is posted.</p></fieldset>
      <fieldset disabled={busy}><legend>Expenses</legend>{accounts.length === 0 && <p className="notice">Create an expense account in <Link href="/accounting">Accounting</Link> before saving a bill.</p>}{lines.map((line, i) => <fieldset key={i}><legend>Line {i + 1}</legend><div className="form-grid"><div><label htmlFor={`bill-item-${i}`}>Saved item</label><select id={`bill-item-${i}`} value={line.item_id || ""} onChange={e => { const item = catalog.find(v => v.id === Number(e.target.value)); setLines(lines.map((l, j) => j === i ? item ? { ...l, item_id: item.id, description: item.purchase_description || item.name, price: (item.cost_price_cents / 100).toFixed(2), account: accounts.some(a => a.id === item.purchase_account_id) ? String(item.purchase_account_id) : l.account } : { ...l, item_id: 0 } : l)); }}><option value="">Choose saved item (optional)</option>{catalog.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></div><div><label htmlFor={`bill-account-${i}`}>Expense account</label><select id={`bill-account-${i}`} required={!line.item_id} disabled={!!line.item_id} value={line.account} onChange={e => { const code = accounts.find(a => String(a.id) === e.target.value)?.tax_rate_code ?? ""; setLines(lines.map((l, j) => j === i ? { ...l, account: e.target.value, gst: code || l.gst } : l)); }}><option value="">Choose account</option>{accounts.map(account => <option key={account.id} value={account.id}>{account.code} — {account.name}</option>)}</select></div><div><label htmlFor={`bill-description-${i}`}>Description</label><input id={`bill-description-${i}`} required maxLength={200} value={line.description} onChange={e => changeLine(i, "description", e.target.value)} /></div><div><label htmlFor={`bill-quantity-${i}`}>Quantity</label><input id={`bill-quantity-${i}`} type="number" required min={1} max={1000000} step={1} value={line.quantity} onChange={e => changeLine(i, "quantity", e.target.value)} /></div><div><label htmlFor={`bill-price-${i}`}>Unit price (SGD)</label><input id={`bill-price-${i}`} inputMode="decimal" required pattern="[0-9,]+(\.[0-9]{1,2})?" value={line.price} onChange={e => changeLine(i, "price", formatMoneyInput(e.target.value))} /></div><div><label htmlFor={`bill-dtype-${i}`}>Discount type</label><select id={`bill-dtype-${i}`} value={line.discountType} onChange={e => setLines(lines.map((l, j) => j === i ? { ...l, discountType: e.target.value as "flat" | "percent", discount: "" } : l))}><option value="flat">Amount (SGD)</option><option value="percent">Percent</option></select></div><div><label htmlFor={`bill-discount-${i}`}>Discount <span className="field-optional">Optional</span></label><input id={`bill-discount-${i}`} inputMode="decimal" value={line.discount} onChange={e => changeLine(i, "discount", e.target.value)} /></div><div><label htmlFor={`bill-daccount-${i}`}>Discount account <span className="field-optional">Optional</span></label><select id={`bill-daccount-${i}`} value={line.discountAccount} onChange={e => changeLine(i, "discountAccount", e.target.value)}><option value="">Use the bill&apos;s setting</option>{accounts.map(account => <option key={account.id} value={account.id}>{account.code} — {account.name}</option>)}</select></div><div><label htmlFor={`bill-gst-${i}`}>GST</label><select id={`bill-gst-${i}`} value={line.gst} onChange={e => changeLine(i, "gst", e.target.value)}><option value="NONE">No GST</option>{taxRates.filter(t => t.code !== "NONE").map(t => <option key={t.code} value={t.code}>{t.name} ({t.rate_bp / 100}%)</option>)}</select></div><div><label htmlFor={`bill-customer-${i}`}>Customer <span className="field-optional">Optional</span></label><select id={`bill-customer-${i}`} value={line.customer} onChange={e => changeLine(i, "customer", e.target.value)}><option value="">No customer</option>{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div></div><p>Amount: {formatCents(parsed[i].net)}</p><TrackingFields orgId={orgId} idPrefix="bill" lineNumber={i} value={line.tracking} disabled={busy} onChange={v => setLines(lines.map((x, index) => index === i ? { ...x, tracking: v } : x))} />{lines.length > 1 && <button type="button" className="button-secondary" onClick={() => setLines(lines.filter((_, index) => index !== i))}>Remove line {i + 1}</button>}</fieldset>)}<button type="button" className="button-secondary" disabled={lines.length >= 200} onClick={() => setLines([...lines, blankLine()])}>Add line</button><BulkLines id="bulk" room={200 - lines.length} accounts={accounts} onAccount={v => setLines(lines.map(l => ({ ...l, account: v })))} onAdd={rows => setLines([...lines.filter(l => l.description || l.price), ...rows.map(r => ({ ...blankLine(), description: r.description, quantity: r.quantity, price: r.rate }))])} /><p aria-live="polite">Sub total {formatCents(subtotal)} · Discount {formatCents(subtotal - total)} · GST {formatCents(gst)} · Total: <strong>{formatCents(total + gst)}</strong>{selfGst > 0 && <> · Self-assessed GST {formatCents(selfGst)} (added to the return, not to the supplier total)</>}</p></fieldset>
      <fieldset disabled={busy}><legend>Purchase discounts</legend><label htmlFor="bill-discount-account">Discount account</label><select id="bill-discount-account" value={discountAccount} onChange={e => setDiscountAccount(e.target.value)}><option value="">Reduce each expense account</option>{accounts.map(account => <option key={account.id} value={account.id}>{account.code} — {account.name}</option>)}</select><p className="section-help">Choose an expense account to show line discounts separately. Each line can override this with its own discount account. With no account on the line or here, the line is recorded after its discount.</p></fieldset>
      <fieldset disabled={busy}><legend>Notes</legend><label htmlFor="bill-notes">Internal notes <span className="field-optional">Optional</span></label><textarea id="bill-notes" rows={3} maxLength={4000} value={notes} onChange={e => setNotes(e.target.value)} /></fieldset>
      {!editingId && <fieldset disabled={busy}><legend>Make recurring</legend><div className="form-grid"><div><label htmlFor="bill-repeat-every">Repeat every</label><input id="bill-repeat-every" type="number" min={1} max={12} value={repeatEvery} onChange={e => setRepeatEvery(e.target.value)} /></div><div><label htmlFor="bill-repeat-unit">Unit</label><select id="bill-repeat-unit" value={repeatUnit} onChange={e => setRepeatUnit(e.target.value)}><option value="week">Week</option><option value="month">Month</option><option value="year">Year</option></select></div><div><label htmlFor="bill-repeat-ends">Ends on <span className="field-optional">Optional</span></label><DateInput id="bill-repeat-ends" min={issued} value={repeatEnds} onChange={e => setRepeatEnds(e.target.value)}/></div><div><label htmlFor="bill-repeat-mode">When a bill is due</label><select id="bill-repeat-mode" value={repeatMode} onChange={e => setRepeatMode(e.target.value)}><option value="draft">Save as draft</option><option value="approve">Approve and post</option></select><p className="section-help">Approve-and-send is not available: Tidal sends no email.</p></div><div><label htmlFor="bill-repeat-auto"><input id="bill-repeat-auto" type="checkbox" checked={repeatAuto} onChange={e => setRepeatAuto(e.target.checked)} /> Generate due bills automatically</label></div></div><p className="section-help">Starts on the bill date. Each run creates a draft bill with the run date added to the reference. Nothing posts by itself.</p><button type="button" className="button-secondary" disabled={busy || !valid || !/^\d{1,2}$/.test(repeatEvery) || Number(repeatEvery) < 1 || Number(repeatEvery) > 12} onClick={() => void makeRecurring()}>Save as recurring bill</button></fieldset>}
      {!editingId && orgId != null && <ExtrasForm kind="bills" orgId={orgId} value={extras} onChange={setExtras} tags={false} />}
      <div className="form-actions"><button disabled={busy || !valid}>{busy ? "Saving…" : "Save draft"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {!open && b && detail && <section aria-label="Bill details">
      <div className="form-actions"><h2>{b.supplier_reference} — {b.supplier_name}</h2><button className="button-secondary" disabled={busy || loading} onClick={() => setDetail(null)}>Close details</button></div>
      <dl className="detail-grid"><div><dt>Number</dt><dd>{b.document_number}</dd></div><div><dt>Status</dt><dd>{b.lifecycle_status || b.status}</dd></div><div><dt>Bill date</dt><dd>{formatDate(b.bill_date)}</dd></div><div><dt>Due date</dt><dd>{formatDate(b.due_date)}</dd></div><div><dt>Payment terms</dt><dd>{b.payment_terms_days === 0 ? "Due on receipt" : `${b.payment_terms_days} days`}</dd></div><div><dt>Permit number</dt><dd>{b.permit_number || "—"}</dd></div><div><dt>Planned date</dt><dd>{b.planned_date ? formatDate(b.planned_date) : "—"}</dd></div><div><dt>Order number</dt><dd>{b.order_number || "—"}</dd></div><div><dt>Subject</dt><dd>{b.subject || "—"}</dd></div><div><dt>Total</dt><dd>{formatCents(b.total_cents)}</dd></div><div><dt>Outstanding</dt><dd>{formatCents(b.balance_cents)}</dd></div></dl>
      <PdfButton kind="bills" id={b.id} orgId={orgId} /><Attachments key={b.id} kind="bills" id={b.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras kind="bills" id={b.id} orgId={orgId} readOnly={!canWrite} />
      {b.notes && <p className="long-text">{b.notes}</p>}
      <div className="table-scroll"><table className="doc-table"><caption>Bill lines</caption><thead><tr><th scope="col">Account</th><th scope="col">Description</th><th scope="col" className="num">Quantity</th><th scope="col" className="num">Unit price</th><th scope="col" className="num">Discount</th><th scope="col" className="num">GST</th><th scope="col" className="num">Amount</th></tr></thead><tbody>{detail.lines.map(line => <tr key={line.line_no}><td>{line.account_code} — {line.account_name}</td><td>{line.description}</td><td className="num">{line.quantity}</td><td className="num">{formatCents(line.unit_price_cents)}</td><td className="num">{line.discount_cents ? formatCents(line.discount_cents) : "—"}</td><td className="num">{line.gst_cents ? formatCents(line.gst_cents) : "—"}</td><td className="num">{formatCents(line.amount_cents)}</td></tr>)}</tbody></table></div>
      {(b.status === "draft" || b.status === "awaiting_approval") && canWrite && <div><p>Posting locks this bill and records expenses and money owed. Review it first; a posted bill can be corrected with a credit note or voided below.</p><div className="form-actions"><button disabled={busy || loading || b.status !== "draft"} onClick={() => start(true)}>Edit draft</button><button className="button-secondary" disabled={busy || loading || b.status !== "draft"} onClick={() => { void (async () => { setBusy(true); setError(""); try { await api(`/v1/bills/${b.id}/submit`, { method: "POST", body: JSON.stringify({ org_id: orgId }) }); setNotice("Bill sent for approval. Approving posts it."); await refresh(); await show(b.id); } catch (e) { fail(e); } finally { setBusy(false); } })(); }}>Send for approval</button><button disabled={busy || loading} onClick={post}>{busy ? "Posting…" : b.status === "awaiting_approval" ? "Approve and post" : "Post bill"}</button></div></div>}
      {b.status === "posted" && b.balance_cents > 0 && canWrite && <form className="record-form" onSubmit={pay}><fieldset disabled={busy || loading}><legend>Record payment already made</legend><div className="form-grid"><div><label htmlFor="bill-payment">Amount (SGD)</label><input id="bill-payment" required inputMode="decimal" pattern="[0-9,]+(\.[0-9]{1,2})?" value={payAmount} onChange={e => setPayAmount(formatMoneyInput(e.target.value))} /></div><div><label htmlFor="bill-paid-date">Payment date</label><DateInput id="bill-paid-date" min={b.bill_date} required value={payDate} onChange={e => setPayDate(e.target.value)}/></div><div><label htmlFor="bill-payment-reference">Bank reference <span className="field-optional">Optional</span></label><input id="bill-payment-reference" maxLength={100} value={payReference} onChange={e => setPayReference(e.target.value)} /></div></div>{(parseCents(payAmount) ?? 0) > b.balance_cents && <fieldset><legend>Overpayment</legend><p role="alert" className="form-error">This is more than the {formatCents(b.balance_cents)} still owed. Nothing is created silently.</p><div><label htmlFor="bill-overpay"><input id="bill-overpay" type="checkbox" checked={overpayment} onChange={e => setOverpayment(e.target.checked)} /> Confirm this overpayment</label></div><div><label htmlFor="bill-overpay-description">What is the extra for?</label><input id="bill-overpay-description" maxLength={500} value={overpaymentDescription} onChange={e => setOverpaymentDescription(e.target.value)} /></div></fieldset>}<button>{busy ? "Recording…" : "Record payment"}</button></fieldset></form>}
      <BillCorrections orgId={orgId} billId={b.id} canWrite={canWrite && b.status === "posted"} onChanged={(m) => { setNotice(m); void show(b.id); void refresh(); }} />
      <h2>Payments</h2>{detail.payments.length === 0 ? <div className="empty-state" role="status"><p>No payments recorded.</p></div> : <div className="table-scroll"><table className="doc-table"><caption className="sr-only">Bill payments</caption><thead><tr><th scope="col">Date</th><th scope="col">Reference</th><th scope="col" className="num">Amount</th></tr></thead><tbody>{detail.payments.map(p => <tr key={p.id}><td>{formatDate(p.payment_date)}</td><td>{p.reference || "—"}</td><td className="num">{formatCents(p.amount_cents)}</td></tr>)}</tbody></table></div>}
    </section>}
    {loading && <p role="status">Loading bill…</p>}
    <div className="record-toolbar"><div><label htmlFor="bill-search">Find supplier / reference</label><input id="bill-search" value={search} onChange={e => setSearch(e.target.value)} /></div><div><label htmlFor="bill-from">From</label><DateInput id="bill-from" value={from} onChange={e => setFrom(e.target.value)} /></div><div><label htmlFor="bill-to">To</label><DateInput id="bill-to" value={to} onChange={e => setTo(e.target.value)} /></div><label>Date type<select id="bill-date-type" value={dateType} onChange={e => setDateType(e.target.value as "bill_date" | "due_date")}><option value="bill_date">Bill date</option><option value="due_date">Due date</option></select></label><div><span className="section-help">Status</span><nav className="tabs" aria-label="Bill status"><ul>{BILL_TABS.map(tab => <li key={tab.id}><button type="button" className="button-secondary" aria-pressed={filter === tab.id} onClick={() => setFilter(tab.id)}>{tab.label}{counts ? ` (${counts[tab.id]})` : ""}</button></li>)}</ul></nav></div></div>
    {selected.length > 0 && <div className="form-actions"><span className="section-help">{selected.length} selected</span><button className="button-secondary" disabled={busy} onClick={() => void bulk("submit")}>Submit for approval</button><button disabled={busy} onClick={() => void bulk("approve")}>Approve</button><button className="button-secondary" disabled={busy} onClick={() => setSelected([])}>Clear selection</button></div>}
    {selectedPayable.length > 0 && <form className="record-form" onSubmit={batchPay}><fieldset disabled={busy}><legend>Batch payment for {selectedPayable.length} bills ({formatCents(selectedPayable.reduce((sum, bill) => sum + bill.balance_cents, 0))})</legend><div className="form-grid"><div><label htmlFor="batch-date">Payment date</label><DateInput id="batch-date" required value={batchDate} onChange={e => setBatchDate(e.target.value)} /></div><div><label htmlFor="batch-reference">Reference</label><input id="batch-reference" maxLength={100} value={batchReference} onChange={e => setBatchReference(e.target.value)} /></div><div><label htmlFor="batch-account">Paid from</label><select id="batch-account" value={batchAccount} onChange={e => setBatchAccount(e.target.value)}><option value="">Default bank account</option>{cashBankAccounts(allAccounts).map(account => <option key={account.id} value={account.id}>{account.code} — {account.name}</option>)}</select></div></div><button disabled={!batchDate}>{busy ? "Recording…" : "Record one payment for all"}</button></fieldset></form>}
    {forwarding && <p className="notice">Email-forwarded bills: {forwarding} (read only). Tidal does not receive mail yet.</p>}
    {rows === null && !error && <p>Loading bills…</p>}{visible?.length === 0 && <div className="empty-state" role="status"><p>{rows?.length ? "No bills match this status." : "No bills yet."}</p></div>}
    {visible && visible.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 bills</caption><thead><tr><th scope="col"><span className="sr-only">Select</span></th><th scope="col">Supplier</th><th scope="col">Reference</th><th scope="col">Status</th><th scope="col">Planned date</th><th scope="col">Due</th><th scope="col">Paid</th><th scope="col" className="num">Total</th><th scope="col" className="num">Outstanding</th></tr></thead><tbody>{visible.map(bill => <tr key={bill.id}><td><input type="checkbox" aria-label={`Select ${bill.supplier_reference}`} checked={selected.includes(bill.id)} onChange={e => setSelected(e.target.checked ? [...selected, bill.id] : selected.filter(id => id !== bill.id))} /></td><td>{bill.supplier_name}</td><td><button className="button-secondary" disabled={busy || loading || open} onClick={() => void show(bill.id)}>{bill.supplier_reference}</button></td><td>{bill.lifecycle_status || bill.status}</td><td>{bill.planned_date ? formatDate(bill.planned_date) : "—"}</td><td>{formatDate(bill.due_date)}</td><td>{bill.paid ? "Yes" : "No"}</td><td className="num">{formatCents(bill.total_cents)}</td><td className="num">{formatCents(bill.balance_cents)}</td></tr>)}</tbody></table></div>}
    {visible && <p className="section-help" role="status">Showing {visible.length} of {rows?.length ?? 0} bills.</p>}
  </>;
}
