"use client";
import ExtrasForm, { noExtras, extrasPayload } from "@/components/ExtrasForm";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { ProjectIdSelect, cashBankAccounts } from "@/components/ListSelects";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import BulkExpenses from "@/components/BulkExpenses";
import MileagePreferences, { type MileageSettings } from "@/components/MileagePreferences";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { keyFor, clearKey } from "@/lib/payment-key";
import DateInput from "@/components/DateInput";

type ExpenseItem = { description: string; account_id: number; amount_cents: number };
type Expense = { id: number; expense_date: string; payee: string; description: string; account_code: string; account_name: string; amount_cents: number; receipt_reference: string; notes: string; paid_through_account_id: number; vendor_id: number; customer_id: number; project_id: number; billable: boolean; invoice_id: number; items: ExpenseItem[] };
type Account = { id: number; code: string; name: string; type: string };
type Contact = { id: number; name: string; role: string };
const blank = () => ({ expense_date: new Date().toISOString().slice(0, 10), payee: "", description: "", expense_account_id: "", paid_through_account_id: "", vendor_id: "", customer_id: "", project_id: "", billable: false, gst: "NONE", amount: "", receipt_reference: "", notes: "" });
const blankItem = () => ({ description: "", account_id: "", amount: "" });

export default function ExpensesPage() {
  const { selected } = useOrg();
  return <ExpensesWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function ExpensesWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const saveAndNew = useRef(false);
  const [rows, setRows] = useState<Expense[] | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [form, setForm] = useState(blank);
  const [itemize, setItemize] = useState(false);
  const [mileage, setMileage] = useState(false);
  const [mileageMode, setMileageMode] = useState("distance");
  const [distance, setDistance] = useState("");
  const [odometerStart, setOdometerStart] = useState("");
  const [odometerEnd, setOdometerEnd] = useState("");
  const [preferences, setPreferences] = useState<MileageSettings | null>(null);
  const [preferencesOpen, setPreferencesOpen] = useState(false);
  const [items, setItems] = useState([blankItem()]);
  const [detail, setDetail] = useState<Expense | null>(null);
  const [invoiceGst, setInvoiceGst] = useState("NONE");
  const [open, setOpen] = useState(false);
  const [bulk, setBulk] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);

  async function load() {
    if (orgId == null) return;
    const [e, a, customers, suppliers, p] = await Promise.all([
      api<{ expenses: Expense[] }>(`/v1/expenses?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=supplier`),
      api<{ preferences: MileageSettings | null }>(`/v1/mileage-preferences?org_id=${orgId}`),
    ]);
    if (!live.current) return;
    setRows(e.expenses); setAccounts(a.accounts); setContacts([...new Map([...customers.contacts, ...suppliers.contacts].map(c => [c.id, c])).values()]); setPreferences(p.preferences);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const parsed = items.map(i => ({ description: i.description.trim(), account_id: Number(i.account_id), amount_cents: parseCents(i.amount) }));
  const amount = itemize ? parsed.reduce((n, i) => n + (i.amount_cents ?? 0), 0) : parseCents(form.amount);
  // Same rule as the server: latest dated rate on or before the expense date, distance x rate rounded to the cent.
  const thousandths = (text: string) => /^\d{1,9}(\.\d{1,3})?$/.test(text.trim()) ? Math.round(Number(text) * 1000) : null;
  const mileagePreview = (() => {
    if (!mileage || !preferences) return null;
    const start = thousandths(odometerStart), end = thousandths(odometerEnd), travelled = thousandths(distance);
    const dist = mileageMode === "distance" ? travelled : start != null && end != null && end > start ? end - start : null;
    if (dist == null || dist <= 0) return null;
    let rate = preferences.default_rate_cents;
    for (const r of preferences.rates) if (r.start_date <= form.expense_date) rate = r.rate_cents;
    return { rate, cents: Number((BigInt(dist) * BigInt(rate) + BigInt(500)) / BigInt(1000)) };
  })();
  async function convertToInvoice(exp: Expense) {
    if (busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const made = await api<{ invoice_id: number }>(`/v1/expenses/${exp.id}/invoice`, { method: "POST", body: JSON.stringify({ org_id: orgId, gst_code: invoiceGst }) });
      setInvoiceGst("NONE");
      setNotice(`Draft invoice ${made.invoice_id} created for the customer. Review and post it from Invoices.`);
      setRows(prev => prev?.map(x => x.id === exp.id ? { ...x, invoice_id: made.invoice_id } : x) ?? null); setDetail(d => d && d.id === exp.id ? { ...d, invoice_id: made.invoice_id } : d);
    } catch (err) { if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy || !canWrite || orgId == null) return;
    if (mileage) {
      const start = thousandths(odometerStart), end = thousandths(odometerEnd), travelled = thousandths(distance);
      if (!preferences || !form.expense_date || !form.payee.trim() || !form.description.trim() || !form.paid_through_account_id || (mileageMode === "distance" ? travelled == null || travelled <= 0 : start == null || end == null || end <= start)) { setError("Save mileage preferences, then enter date, payee, description, payment account and a valid distance."); return; }
      const payload = { org_id: orgId, expense_date: form.expense_date, payee: form.payee.trim(), description: form.description.trim(), paid_through_account_id: Number(form.paid_through_account_id), vendor_id: Number(form.vendor_id), customer_id: Number(form.customer_id), project_id: Number(form.project_id), billable: form.billable, gst_code: form.gst, receipt_reference: form.receipt_reference.trim(), notes: form.notes, mileage: mileageMode === "distance" ? { mode: "distance", distance_thousandths: travelled } : { mode: "odometer", odometer_start_thousandths: start, odometer_end_thousandths: end } };
      const body = JSON.stringify(payload), slot = `mileage-${orgId}`;
      const key = attempt.current?.body === body ? attempt.current.key : keyFor(slot, body);
      attempt.current = { body, key };
      setBusy(true); setError(""); setNotice("");
      try {
        const saved = await api<{ id: number }>("/v1/expenses", { method: "POST", body: JSON.stringify({ ...payload, idempotency_key: key, ...extrasPayload(extras) }) });
        clearKey(slot); attempt.current = null;
        setExtras(noExtras());
        const bad1 = "";
        if (!live.current) return;
        setForm(blank()); setDistance(""); setOdometerStart(""); setOdometerEnd(""); setMileage(false); setOpen(saveAndNew.current);
        setNotice("Mileage expense recorded using the saved mileage rate." + (bad1 ? " " + bad1 : ""));
        const result = await api<{ expenses: Expense[] }>(`/v1/expenses?org_id=${orgId}`);
        if (!live.current) return;
        setRows(result.expenses); setDetail(saveAndNew.current ? null : result.expenses.find(x => x.id === saved.id) ?? null);
      } catch (err) { if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); } }
      finally { if (live.current) setBusy(false); }
      return;
    }
    if (!form.expense_date || !form.payee.trim() || !form.description.trim() || !form.paid_through_account_id || amount == null || amount <= 0 || amount > 100000000000000 || (itemize ? parsed.some(i => !i.description || !i.account_id || i.amount_cents == null || i.amount_cents <= 0) : !form.expense_account_id)) {
      setError("Enter date, payee, description, payment account and valid expense amounts."); return;
    }
    const payload = { org_id: orgId, expense_date: form.expense_date, payee: form.payee.trim(), description: form.description.trim(), expense_account_id: itemize ? parsed[0].account_id : Number(form.expense_account_id), paid_through_account_id: Number(form.paid_through_account_id), vendor_id: Number(form.vendor_id), customer_id: Number(form.customer_id), project_id: Number(form.project_id), billable: form.billable, gst_code: form.gst, amount_cents: amount, receipt_reference: form.receipt_reference.trim(), notes: form.notes, items: itemize ? parsed : [] };
    const body = JSON.stringify(payload), slot = `expense-${orgId}`;
    const key = attempt.current?.body === body ? attempt.current.key : keyFor(slot, body);
    attempt.current = { body, key };
    setBusy(true); setError(""); setNotice("");
    try {
      const saved = await api<{ id: number }>("/v1/expenses", { method: "POST", body: JSON.stringify({ ...payload, idempotency_key: key, ...extrasPayload(extras) }) });
      clearKey(slot); attempt.current = null;
      setExtras(noExtras());
      const bad = "";
      if (!live.current) return;
      setForm(blank()); setItems([blankItem()]); setItemize(false); setOpen(saveAndNew.current);
      setNotice("Expense recorded. No money was sent. Open its details to upload receipts." + (bad ? " " + bad : ""));
      const result = await api<{ expenses: Expense[] }>(`/v1/expenses?org_id=${orgId}`);
      if (!live.current) return;
      setRows(result.expenses); setDetail(saveAndNew.current ? null : result.expenses.find(e => e.id === saved.id) ?? null);
    } catch (err) {
      if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); }
    } finally { if (live.current) setBusy(false); }
  }
  const accountSelect = (id: string, value: string, type: string, change: (v: string) => void) => <select id={id} required value={value} onChange={e => change(e.target.value)}><option value="">Choose account</option>{(type === "asset" ? cashBankAccounts(accounts) : accounts.filter(a => a.type === type)).map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select>;
  return <>
    <PageHeader title="Expenses" description="Record money already spent and keep its receipt evidence." />
    <p className="notice">SGD only. Choose the GST code that matches the receipt; the amount you type is the total paid, so a standard-rated 109.00 records 100.00 expense and 9.00 input GST. Saving records money already paid; it sends no payment. Saved expenses cannot be changed. Recurring profiles are on the Recurring expenses page.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <button className="button-secondary" disabled={busy || open} onClick={() => { setBulk(!bulk); setDetail(null); }}>{bulk ? "Close bulk add" : "Bulk add expenses"}</button>}
    {bulk && canWrite && orgId != null && <BulkExpenses orgId={orgId} accounts={accounts} contacts={contacts} onSaved={() => { void load().catch(() => {}); }} onCancel={() => setBulk(false)} />}
    {selected && canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setDetail(null); setExtras(noExtras()); setForm(blank()); setItems([blankItem()]); setItemize(false); setMileage(false); }}>New expense</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Expense details</legend><div className="form-grid">
        <div><label htmlFor="ex-date">Date</label><DateInput id="ex-date" required value={form.expense_date} onChange={e => setForm({ ...form, expense_date: e.target.value })}/></div>
        <div><label htmlFor="ex-vendor">Vendor (optional)</label><select id="ex-vendor" value={form.vendor_id} onChange={e => setForm({ ...form, vendor_id: e.target.value, payee: contacts.find(c => c.id === Number(e.target.value))?.name ?? form.payee })}><option value="">No vendor</option>{contacts.filter(c => c.role !== "customer").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="ex-payee">Paid to</label><input id="ex-payee" required maxLength={200} value={form.payee} onChange={e => setForm({ ...form, payee: e.target.value })} /></div>
        <div><label htmlFor="ex-desc">Description</label><input id="ex-desc" required maxLength={200} value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} /></div>
        <div><label htmlFor="ex-paid-through">Paid through</label>{accountSelect("ex-paid-through", form.paid_through_account_id, "asset", v => setForm({ ...form, paid_through_account_id: v }))}<p className="section-help">Choose cash or bank account used. No bank connection required.</p></div>
        <div><label htmlFor="ex-customer">Customer (optional)</label><select id="ex-customer" value={form.customer_id} onChange={e => setForm({ ...form, customer_id: e.target.value, project_id: "", billable: e.target.value ? form.billable : false })}><option value="">No customer</option>{contacts.filter(c => c.role !== "supplier").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select><p className="section-help">Records customer association; does not create a customer invoice.</p></div>
        <div><label htmlFor="ex-project">Project</label>{orgId != null && <ProjectIdSelect id="ex-project" orgId={orgId} contactId={form.customer_id} value={form.project_id} onChange={v => setForm({ ...form, project_id: v })} />}</div>
        <div><label className="check-label"><input type="checkbox" disabled={!form.customer_id} checked={form.billable} onChange={e => setForm({ ...form, billable: e.target.checked })} />Billable</label><p className="section-help">Marks the cost to pass on to the customer. It does not add the cost to an invoice.</p></div>
      </div></fieldset>
      <fieldset disabled={busy}><legend>Amounts</legend><p>Currency: SGD. Exchange rate: 1 SGD = 1 SGD. Other currencies unavailable.</p><label className="check-label"><input type="checkbox" checked={mileage} onChange={e => { setMileage(e.target.checked); if (e.target.checked) setItemize(false); }} />Record mileage</label>
        {mileage ? <>{!preferences && <p role="alert" className="form-error">Save mileage preferences first.</p>}<div className="form-grid"><div><label htmlFor="ex-mileage-mode">Entry method</label><select id="ex-mileage-mode" value={mileageMode} onChange={e => setMileageMode(e.target.value)}><option value="distance">Distance</option><option value="odometer">Odometer readings</option></select></div>{mileageMode === "distance" ? <div><label htmlFor="ex-distance">Distance ({preferences?.unit ?? "km"})</label><input id="ex-distance" required inputMode="decimal" value={distance} onChange={e => setDistance(e.target.value)} /></div> : <><div><label htmlFor="ex-odo-start">Start odometer</label><input id="ex-odo-start" required inputMode="decimal" value={odometerStart} onChange={e => setOdometerStart(e.target.value)} /></div><div><label htmlFor="ex-odo-end">End odometer</label><input id="ex-odo-end" required inputMode="decimal" value={odometerEnd} onChange={e => setOdometerEnd(e.target.value)} /></div></>}</div><p aria-live="polite">Amount: {mileagePreview ? `${formatCents(mileagePreview.cents)} at ${formatCents(mileagePreview.rate)} per ${preferences?.unit ?? "km"}` : "enter a valid distance"}</p><p className="section-help">Amount = distance × rate in force on the expense date, rounded to the cent. Preferences are saved per company.</p><button type="button" className="button-secondary" onClick={() => setPreferencesOpen(!preferencesOpen)}>{preferencesOpen ? "Hide mileage preferences" : "Mileage preferences"}</button></> : <label className="check-label"><input type="checkbox" checked={itemize} onChange={e => setItemize(e.target.checked)} />Itemize expense</label>}
        {mileage ? null : !itemize ? <div className="form-grid"><div><label htmlFor="ex-account">Expense account</label>{accountSelect("ex-account", form.expense_account_id, "expense", v => setForm({ ...form, expense_account_id: v }))}</div><div><label htmlFor="ex-amount">Amount (SGD)</label><input id="ex-amount" required inputMode="decimal" value={form.amount} onChange={e => setForm({ ...form, amount: e.target.value })} /></div></div> : <>{items.map((item, i) => <fieldset key={i}><legend>Item {i + 1}</legend><div className="form-grid"><div><label htmlFor={`ex-item-description-${i}`}>Description</label><input id={`ex-item-description-${i}`} required maxLength={200} value={item.description} onChange={e => setItems(items.map((v, j) => i === j ? { ...v, description: e.target.value } : v))} /></div><div><label htmlFor={`ex-item-account-${i}`}>Expense account</label>{accountSelect(`ex-item-account-${i}`, item.account_id, "expense", value => setItems(items.map((v, j) => i === j ? { ...v, account_id: value } : v)))}</div><div><label htmlFor={`ex-item-amount-${i}`}>Amount (SGD)</label><input id={`ex-item-amount-${i}`} required inputMode="decimal" value={item.amount} onChange={e => setItems(items.map((v, j) => i === j ? { ...v, amount: e.target.value } : v))} /></div></div>{items.length > 1 && <button type="button" className="button-secondary" onClick={() => setItems(items.filter((_, j) => j !== i))}>Remove item {i + 1}</button>}</fieldset>)}<button type="button" disabled={items.length >= 200} onClick={() => setItems([...items, blankItem()])}>Add item</button></>}
        {!mileage && !itemize && (
          <div className="form-grid"><div>
            <label htmlFor="ex-gst">GST code</label>
            <select id="ex-gst" value={form.gst} onChange={e => setForm({ ...form, gst: e.target.value })}>
              <option value="NONE">No GST</option>
              <option value="SR">Standard-rated (SR)</option>
              <option value="ZR">Zero-rated (ZR)</option>
              <option value="ES">Exempt (ES)</option>
              <option value="OS">Out of scope (OS)</option>
            </select>
            <p className="muted small">The amount includes GST. Claimable input tax appears in the GST report.</p>
          </div></div>
        )}
        {!mileage && <p aria-live="polite">Total: {formatCents(amount ?? 0)}</p>}
      </fieldset>
      <fieldset disabled={busy}><legend>Receipt and notes</legend><label htmlFor="ex-receipt">Reference (optional)</label><input id="ex-receipt" maxLength={100} value={form.receipt_reference} onChange={e => setForm({ ...form, receipt_reference: e.target.value })} /><label htmlFor="ex-notes">Notes (optional)</label><textarea id="ex-notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /><p className="section-help">Save first, then upload receipts from expense details. Up to 10 MB per file.</p></fieldset>
      {orgId != null && <ExtrasForm kind="expenses" orgId={orgId} value={extras} onChange={setExtras} />}
      <div className="form-actions"><button disabled={busy} onClick={() => { saveAndNew.current = false; }}>{busy ? "Saving…" : "Save expense"}</button><button disabled={busy} onClick={() => { saveAndNew.current = true; }}>Save and new</button><button type="button" disabled={busy} className="button-secondary" onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {preferencesOpen && open && orgId != null && mileage && <MileagePreferences orgId={orgId} initial={preferences} accounts={accounts} onSaved={v => { setPreferences(v); setPreferencesOpen(false); setNotice("Mileage preferences saved."); }} />}
    {detail && orgId != null && <section className="record-form"><h2>{detail.description}</h2><dl className="detail-grid"><div><dt>Paid to</dt><dd>{detail.payee}</dd></div><div><dt>Amount</dt><dd>{formatCents(detail.amount_cents)}</dd></div><div><dt>Paid through</dt><dd>{accounts.find(a => a.id === detail.paid_through_account_id)?.name ?? "Bank"}</dd></div><div><dt>Vendor</dt><dd>{contacts.find(c => c.id === detail.vendor_id)?.name ?? "Not assigned"}</dd></div><div><dt>Customer</dt><dd>{contacts.find(c => c.id === detail.customer_id)?.name ?? "Not assigned"}</dd></div><div><dt>Billable</dt><dd>{detail.billable ? "Yes" : "No"}</dd></div></dl>{detail.items?.map((i, n) => <p key={n}>{i.description} — {accounts.find(a => a.id === i.account_id)?.name} — {formatCents(i.amount_cents)}</p>)}{detail.notes && <p className="long-text">{detail.notes}</p>}{canWrite && <ReceiptUpload expense={detail} orgId={orgId} onLinked={async () => { await load(); setDetail(prev => prev ? { ...prev } : null); }} />}<Attachments key={detail.id} kind="expenses" id={detail.id} orgId={orgId} readOnly={!canWrite} /><Extras kind="expenses" id={detail.id} orgId={orgId} readOnly={!canWrite} />{canWrite && detail.billable && detail.customer_id > 0 && detail.invoice_id === 0 && <div className="form-grid"><div><label htmlFor="expense-invoice-gst">Tax on resulting invoice</label><select id="expense-invoice-gst" value={invoiceGst} onChange={e => setInvoiceGst(e.target.value)}><option value="NONE">No GST</option><option value="SR">Standard-rate GST</option></select><p className="section-help">Standard-rate GST needs a GST-registered company. The draft invoice can still be edited.</p></div><div><button className="button-secondary" disabled={busy} onClick={() => void convertToInvoice(detail)}>Convert to customer invoice (draft)</button></div></div>}<button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></section>}
    {rows === null && !error && <p role="status">Loading expenses…</p>}{rows?.length === 0 && <p>No expenses recorded yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 expenses</caption><thead><tr><th>Date</th><th>Paid to</th><th>Description</th><th>Account</th><th>Receipt</th><th>Amount</th><th>Details</th></tr></thead><tbody>{rows.map(e => <tr key={e.id}><td>{e.expense_date}</td><td>{e.payee}</td><td>{e.description}</td><td>{e.items?.length > 1 ? "Itemized" : e.account_name}</td><td>{e.receipt_reference || "—"}</td><td>{formatCents(e.amount_cents)}</td><td><button className="button-secondary" onClick={() => { setDetail(e); setOpen(false); }}>View<span className="sr-only"> {e.description}</span></button></td></tr>)}</tbody></table></div>}
  </>;
}

function ReceiptUpload({ expense, orgId, onLinked }: { expense: Expense; orgId: number; onLinked: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function upload(file: File) {
    if (file.size === 0 || file.size > 10 * 1024 * 1024) { setError("Choose a nonempty file no larger than 10 MB."); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      const form = new FormData(); form.append("file", file);
      const response = await fetch(`/api/v1/documents?org_id=${orgId}`, { method: "POST", credentials: "same-origin", body: form });
      const result = await response.json();
      if (!response.ok) throw { error: result.error ?? "request_failed", status: response.status };
      await api(`/v1/documents/${result.id}/receipt-link`, { method: "POST", body: JSON.stringify({ org_id: orgId, expense_id: expense.id }) });
      setNotice("Receipt uploaded and linked."); await onLinked();
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  return <section><h3>Receipt</h3><label htmlFor={`expense-receipt-${expense.id}`}>Upload and link receipt (max 10 MB)</label><input id={`expense-receipt-${expense.id}`} type="file" disabled={busy} onChange={e => { const file = e.target.files?.[0]; const input = e.target; if (file) void upload(file).then(() => { input.value = ""; }); }} />{busy && <p role="status">Uploading…</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}</section>;
}
