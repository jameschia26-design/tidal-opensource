"use client";
import { PaymentModeSelect, cashBankAccounts } from "@/components/ListSelects";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { keyFor, clearKey } from "@/lib/payment-key";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import DateInput from "@/components/DateInput";
import { formatDate, todayISO } from "@/lib/dates";

// One screen for Payments received (customer) and Payments made (vendor); the map's two forms differ only in labels and bank charges.
// ponytail: received amount must equal the allocated total; excess/prepayment, refunds, custom fields are not built. Drafts are saved forms only; nothing is booked until posted.
type Doc = { id: number; contact_id: number; status: string; balance_cents: number; date: string; label: string; due: string };
type Row = { id: number; contact_name: string; payment_number: string; payment_date: string; payment_mode: string; amount_cents: number; bank_charge_cents: number; reference: string; notes: string; allocations: { id: number; amount_cents: number }[] };
type Account = { id: number; code: string; name: string; type: string };
type Contact = { id: number; name: string; role: string };
const MODE_LABELS: Record<string, string> = { cash: "Cash", bank_transfer: "Bank transfer", cheque: "Cheque", card: "Card", paynow: "PayNow", other: "Other" };
const today = todayISO;
const blank = () => ({ contact_id: "", number: "", date: today(), mode: "bank_transfer", account_id: "", amount: "", charge: "", charge_account: "", reference: "", notes: "" });

export default function AllocatedPayments({ side }: { side: "received" | "made" }) {
  const { selected } = useOrg();
  return <Workspace side={side} key={`${side}-${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace({ side }: { side: "received" | "made" }) {
  const rec = side === "received";
  const T = rec
    ? { title: "Payments received", path: "payments-received", who: "Customer name", amountLabel: "Amount received", accountLabel: "Deposit to", docs: "Unpaid invoices", noDocs: "This customer has no unpaid sent invoices.", used: "Amount used for payments", kind: "payments-received", none: "No payments received yet." }
    : { title: "Payments made", path: "payments-made", who: "Vendor name", amountLabel: "Payment made", accountLabel: "Paid through", docs: "Unpaid bills", noDocs: "This vendor has no unpaid posted bills.", used: "Amount used for payments", kind: "payments-made", none: "No payments made yet." };
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const [rows, setRows] = useState<Row[] | null>(null);
  const [drafts, setDrafts] = useState<{ id: number; saved_on: string; data: { form: ReturnType<typeof blank>; apply: Record<number, string>; from: string; to: string; extras?: ReturnType<typeof noExtras> } }[]>([]);
  const [draftId, setDraftId] = useState<number | null>(null);
  const [docs, setDocs] = useState<Doc[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [form, setForm] = useState(blank);
  const [apply, setApply] = useState<Record<number, string>>({});
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [detail, setDetail] = useState<Row | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const [p, d, a, c, dr] = await Promise.all([
      api<{ payments: Row[] }>(`/v1/${T.path}?org_id=${orgId}`),
      rec ? api<{ invoices: { id: number; number: number | null; contact_id: number; status: string; invoice_date: string; due_date: string; balance_cents: number }[] }>(`/v1/invoices?org_id=${orgId}&status=posted`) : api<{ bills: { id: number; contact_id: number; status: string; bill_date: string; due_date: string; balance_cents: number; supplier_reference: string }[] }>(`/v1/bills?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=${rec ? "customer" : "supplier"}`),
      api<{ drafts: typeof drafts }>(`/v1/payment-drafts?org_id=${orgId}&side=${side}`),
    ]);
    if (!live.current) return;
    const list: Doc[] = "invoices" in d ? d.invoices.map(i => ({ id: i.id, contact_id: i.contact_id, status: i.status, balance_cents: i.balance_cents, date: i.invoice_date, due: i.due_date, label: i.number != null ? `INV-${i.number}` : `Invoice ${i.id}` })) : d.bills.map(b => ({ id: b.id, contact_id: b.contact_id, status: b.status, balance_cents: b.balance_cents, date: b.bill_date, due: b.due_date, label: b.supplier_reference }));
    setRows(p.payments); setDocs(list); setAccounts(a.accounts); setContacts(c.contacts); setDrafts(dr.drafts);
    setDetail(x => x && (p.payments.find(r => r.id === x.id) ?? null));
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const open_ = docs.filter(d => d.status === "posted" && d.balance_cents > 0 && String(d.contact_id) === form.contact_id && (!from || d.date >= from) && (!to || d.date <= to));
  const parsed = open_.map(d => ({ d, cents: apply[d.id] ? parseCents(apply[d.id]) : 0 }));
  const used = parsed.reduce((n, x) => n + (x.cents ?? 0), 0);
  const received = parseCents(form.amount) ?? 0;
  const charge = rec ? parseCents(form.charge || "0") : 0;
  const bad = parsed.some(x => x.cents == null || x.cents < 0 || x.cents > x.d.balance_cents);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy || orgId == null) return;
    const allocations = parsed.filter(x => (x.cents ?? 0) > 0).map(x => ({ id: x.d.id, amount_cents: x.cents! }));
    if (!form.contact_id || !form.account_id || !form.date || received <= 0 || bad || charge == null || charge > received || (charge > 0 && !form.charge_account) || allocations.length === 0) { setError("Choose who and which account, then apply the amount to at least one document."); return; }
    if (used !== received) { setError(`Amount used (${formatCents(used)}) must equal the amount ${rec ? "received" : "paid"} (${formatCents(received)}). Excess and prepayments are not available.`); return; }
    const payload = { org_id: orgId, draft_id: draftId ?? 0, contact_id: Number(form.contact_id), payment_number: form.number.trim(), payment_date: form.date, payment_mode: form.mode, account_id: Number(form.account_id), amount_cents: received, ...(rec ? { bank_charge_cents: charge, bank_charge_account_id: charge > 0 ? Number(form.charge_account) : 0 } : {}), reference: form.reference.trim(), notes: form.notes, allocations };
    const body = JSON.stringify(payload), slot = `${T.path}-${orgId}`;
    const key = attempt.current?.body === body ? attempt.current.key : keyFor(slot, body);
    attempt.current = { body, key };
    setBusy(true); setError(""); setNotice("");
    try {
      const made = await api<{ id?: number }>(`/v1/${T.path}`, { method: "POST", body: JSON.stringify({ ...payload, idempotency_key: key }) });
      clearKey(slot); attempt.current = null;
      const bad = made?.id ? await saveExtras(T.kind, made.id, orgId, extras) : ""; setExtras(noExtras());
      setDraftId(null);
      if (!live.current) return;
      setNotice((rec ? "Payment received and booked against the invoices. It cannot be changed." : "Payment made and booked against the bills. It cannot be changed.") + (bad ? " " + bad : ""));
      setOpen(false); setForm(blank()); setApply({});
      await load();
    } catch (err) { if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  async function saveDraft() {
    if (busy || orgId == null) return;
    if (!form.contact_id) { setError(`Choose ${rec ? "a customer" : "a vendor"} before saving a draft.`); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      await api(draftId == null ? "/v1/payment-drafts" : `/v1/payment-drafts/${draftId}`, { method: draftId == null ? "POST" : "PUT", body: JSON.stringify({ org_id: orgId, side, data: { form, apply, from, to, extras } }) });
      if (!live.current) return;
      setNotice("Saved as draft. Nothing is booked until you record the payment."); setOpen(false); setDraftId(null); setForm(blank()); setApply({});
      await load();
    } catch (err) { if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  async function dropDraft(id: number) {
    if (busy || orgId == null) return;
    setBusy(true); setError("");
    try { await api(`/v1/payment-drafts/${id}?org_id=${orgId}`, { method: "DELETE" }); if (live.current) { if (draftId === id) setDraftId(null); await load(); } }
    catch (err) { if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  const nameOf = (id: number) => docs.find(d => d.id === id)?.label ?? `#${id}`;
  const people = contacts.filter(c => rec ? c.role !== "supplier" : c.role !== "customer");
  const assets = cashBankAccounts(accounts);

  return <>
    <PageHeader title={T.title} description={rec ? "Record money received from a customer and apply it to their invoices." : "Record money paid to a vendor and apply it to their bills."} />
    <p className="notice">SGD, no GST. Recording a payment books the money at once and reduces what is owed on each chosen document. Recorded payments cannot be changed. Drafts book nothing. Excess payments and refunds are not available.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setDetail(null); setDraftId(null); setExtras(noExtras()); setForm(blank()); setApply({}); setError(""); setNotice(""); }}>{rec ? "Record payment" : "Record payment"}</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Payment</legend><div className="form-grid">
        <div><label htmlFor="ap-who">{T.who}</label><select id="ap-who" required value={form.contact_id} onChange={e => { setForm({ ...form, contact_id: e.target.value }); setApply({}); }}><option value="">Choose</option>{people.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="ap-amount">{T.amountLabel} (SGD)</label><input id="ap-amount" required inputMode="decimal" value={form.amount} onChange={e => setForm({ ...form, amount: e.target.value })} /></div>
        {rec && <><div><label htmlFor="ap-charge">Bank charges (if any, SGD)</label><input id="ap-charge" inputMode="decimal" value={form.charge} onChange={e => setForm({ ...form, charge: e.target.value })} /></div>
          {(charge ?? 0) > 0 && <div><label htmlFor="ap-charge-acct">Bank charges account</label><select id="ap-charge-acct" required value={form.charge_account} onChange={e => setForm({ ...form, charge_account: e.target.value })}><option value="">Choose expense account</option>{accounts.filter(a => a.type === "expense").map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>}</>}
        <div><label htmlFor="ap-date">Payment date</label><DateInput id="ap-date" required value={form.date} onChange={e => setForm({ ...form, date: e.target.value })}/></div>
        <div><label htmlFor="ap-number">Payment# (blank = automatic)</label><input id="ap-number" maxLength={50} value={form.number} onChange={e => setForm({ ...form, number: e.target.value })} /></div>
        <div><label htmlFor="ap-mode">Payment mode</label>{orgId != null && <PaymentModeSelect id="ap-mode" orgId={orgId} value={form.mode} onChange={v => setForm({ ...form, mode: v })} />}<p className="section-help">Add your own modes in Settings, Custom fields and lists.</p></div>
        <div><label htmlFor="ap-account">{T.accountLabel}</label><select id="ap-account" required value={form.account_id} onChange={e => setForm({ ...form, account_id: e.target.value })}><option value="">Choose cash or bank account</option>{assets.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
        <div><label htmlFor="ap-ref">Reference#</label><input id="ap-ref" maxLength={100} value={form.reference} onChange={e => setForm({ ...form, reference: e.target.value })} /></div>
      </div></fieldset>
      <fieldset disabled={busy}><legend>{T.docs}</legend>
        {!form.contact_id ? <p>Choose {rec ? "a customer" : "a vendor"} to see what they owe.</p> : <>
          <div className="form-grid"><div><label htmlFor="ap-from">Document date from</label><DateInput id="ap-from" value={from} onChange={e => setFrom(e.target.value)}/></div><div><label htmlFor="ap-to">Document date to</label><DateInput id="ap-to" value={to} onChange={e => setTo(e.target.value)}/></div></div>
          {open_.length === 0 ? <p>{T.noDocs}</p> : <div className="table-scroll"><table className="doc-table"><caption>{T.docs}</caption><thead><tr><th>{rec ? "Invoice" : "Bill"}</th><th>Date</th><th>Due</th><th>Balance due</th><th>Payment</th></tr></thead><tbody>{open_.map(d => <tr key={d.id}><td>{d.label}</td><td>{formatDate(d.date)}</td><td>{d.due}</td><td>{formatCents(d.balance_cents)}</td><td><input aria-label={`Payment applied to ${d.label}`} inputMode="decimal" value={apply[d.id] ?? ""} onChange={e => setApply({ ...apply, [d.id]: e.target.value })} /> <button type="button" className="button-secondary" onClick={() => setApply({ ...apply, [d.id]: (d.balance_cents / 100).toFixed(2) })}>Full<span className="sr-only"> for {d.label}</span></button></td></tr>)}</tbody></table></div>}
          <button type="button" className="button-secondary" onClick={() => setApply({})}>Clear applied amount</button>
        </>}
        <p aria-live="polite">{T.amountLabel} {formatCents(received)} · {T.used} {formatCents(used)} · Amount refunded {formatCents(0)} · Amount in excess {formatCents(Math.max(0, received - used))}{rec && received > used ? " — saved as a customer credit" : ""}</p>
        {bad && <p role="alert" className="form-error">An applied amount is invalid or more than the balance due.</p>}
      </fieldset>
      <ExtrasForm kind={T.kind} orgId={orgId!} value={extras} onChange={setExtras} />
      <fieldset disabled={busy}><legend>Notes</legend><label htmlFor="ap-notes">Notes (internal only, not shown to the {rec ? "customer" : "vendor"})</label><textarea id="ap-notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /><p className="section-help">Save first, then attach up to 5 files from the payment details.</p></fieldset>
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : rec ? "Save" : "Save as paid"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => void saveDraft()}>Save as draft</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {canWrite && drafts.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Drafts (not booked)</caption><thead><tr><th>Saved</th><th>{rec ? "Customer" : "Vendor"}</th><th>Amount</th><th>Actions</th></tr></thead><tbody>{drafts.map(d => <tr key={d.id}><td>{d.saved_on}</td><td>{contacts.find(c => String(c.id) === d.data.form?.contact_id)?.name ?? "—"}</td><td>{formatCents(parseCents(d.data.form?.amount ?? "") ?? 0)}</td><td><button className="button-secondary" disabled={busy} onClick={() => { setForm({ ...blank(), ...d.data.form }); setExtras(d.data.extras ?? noExtras()); setApply(d.data.apply ?? {}); setFrom(d.data.from ?? ""); setTo(d.data.to ?? ""); setDraftId(d.id); setOpen(true); setDetail(null); setError(""); setNotice(""); }}>Open draft<span className="sr-only"> {d.id}</span></button> <button className="button-secondary" disabled={busy} onClick={() => void dropDraft(d.id)}>Delete draft<span className="sr-only"> {d.id}</span></button></td></tr>)}</tbody></table></div>}
    {detail && orgId != null && <section className="record-form"><h2>{detail.payment_number} — {detail.contact_name}</h2>
      <dl className="detail-grid"><div><dt>Date</dt><dd>{formatDate(detail.payment_date)}</dd></div><div><dt>Mode</dt><dd>{(MODE_LABELS[detail.payment_mode] ?? detail.payment_mode)}</dd></div><div><dt>Amount</dt><dd>{formatCents(detail.amount_cents)}</dd></div>{rec && <div><dt>Bank charges</dt><dd>{formatCents(detail.bank_charge_cents)}</dd></div>}<div><dt>Reference</dt><dd>{detail.reference || "—"}</dd></div></dl>
      <ul>{detail.allocations.map(a => <li key={a.id}>{nameOf(a.id)} — {formatCents(a.amount_cents)}</li>)}</ul>
      {detail.notes && <p className="long-text">{detail.notes}</p>}
      <Attachments key={detail.id} kind={T.kind} id={detail.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras kind={T.kind} id={detail.id} orgId={orgId} readOnly={!canWrite} />
      <button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></section>}
    {rows === null && !error && <p role="status">Loading…</p>}{rows?.length === 0 && <p>{T.none}</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 payments</caption><thead><tr><th>Number</th><th>{rec ? "Customer" : "Vendor"}</th><th>Date</th><th>Amount</th><th>Details</th></tr></thead><tbody>{rows.map(r => <tr key={r.id}><td>{r.payment_number}</td><td>{r.contact_name}</td><td>{formatDate(r.payment_date)}</td><td>{formatCents(r.amount_cents)}</td><td><button className="button-secondary" onClick={() => { setDetail(r); setOpen(false); }}>View<span className="sr-only"> {r.payment_number}</span></button></td></tr>)}</tbody></table></div>}
  </>;
}
