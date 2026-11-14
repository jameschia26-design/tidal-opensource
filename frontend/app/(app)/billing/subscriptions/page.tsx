"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";

type Line = { kind: string; description: string; quantity: number; unit_price_cents: number };
type Sub = { id: number; customer_name: string; mode: string; plan_name: string; quantity: number; interval_count: number; interval_unit: string; billing_cycles: number | null; start_date: string; first_bill_date: string; next_bill_date: string | null; cycles_billed: number; status: string; recurring_lines: Line[]; coupon: { code: string } | null };
type Plan = { id: number; name: string; price_cents: number; setup_fee_cents: number; trial_days: number; interval_count: number; interval_unit: string; active: boolean; product_id: number };
type Addon = { id: number; name: string; addon_type: string; price_cents: number; plan_ids: number[]; active: boolean; product_id: number };
type Contact = { id: number; name: string; role: string };
const unitLabel = (n: number, u: string) => `${n} ${u}${n === 1 ? "" : "s"}`;
const blankItem = () => ({ description: "", quantity: "1", rate: "" });
const today = todayISO;

export default function SubscriptionsPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Sub[] | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [addons, setAddons] = useState<Addon[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<"plan" | "items">("plan");
  const [contact, setContact] = useState("");
  const [planId, setPlanId] = useState("");
  const [quantity, setQuantity] = useState("1");
  const [picked, setPicked] = useState<Record<number, string>>({});
  const [coupon, setCoupon] = useState("");
  const [start, setStart] = useState(today);
  const [every, setEvery] = useState("1");
  const [unit, setUnit] = useState("month");
  const [cycles, setCycles] = useState("");
  const [items, setItems] = useState([blankItem()]);
  const [reference, setReference] = useState("");
  const [notes, setNotes] = useState("");
  const [asOf, setAsOf] = useState(today);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const [s, p, a, c] = await Promise.all([
      api<{ rows: Sub[] }>(`/v1/billing/subscriptions?org_id=${orgId}`),
      api<{ rows: Plan[] }>(`/v1/billing/plans?org_id=${orgId}`),
      api<{ rows: Addon[] }>(`/v1/billing/addons?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    ]);
    if (!live.current) return;
    setRows(s.rows); setPlans(p.rows.filter(x => x.active)); setAddons(a.rows.filter(x => x.active)); setContacts(c.contacts);
  }
  useEffect(() => {
    live.current = true;
    // "Save and Subscribe" on a new customer lands here with ?customer=ID: open the form for them.
    const fromCustomer = new URLSearchParams(window.location.search).get("customer");
    if (fromCustomer && /^\d+$/.test(fromCustomer)) { setContact(fromCustomer); setOpen(true); }
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function post(path: string, body: object, done: string) {
    if (busy || orgId == null) return false;
    setBusy(true); setError(""); setNotice("");
    try { const r = await api<Record<string, unknown>>(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) }); if (!live.current) return false; setNotice(typeof done === "string" ? done : ""); await load(); return r; }
    catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } return false; }
    finally { if (live.current) setBusy(false); }
  }
  const plan = plans.find(p => p.id === Number(planId));
  const available = addons.filter(a => plan && a.product_id === plan.product_id && (a.plan_ids.length === 0 || a.plan_ids.includes(plan.id)));
  const qty = /^\d{1,7}$/.test(quantity) ? Number(quantity) : 0;
  const parsedItems = items.map(i => ({ description: i.description.trim(), quantity: /^\d{1,7}$/.test(i.quantity) ? Number(i.quantity) : 0, unit_price_cents: parseCents(i.rate) }));
  const addonLines = available.filter(a => picked[a.id] !== undefined).map(a => ({ a, q: /^\d{1,7}$/.test(picked[a.id]) ? Number(picked[a.id]) : 0 }));
  const recurringTotal = mode === "plan" ? qty * (plan?.price_cents ?? 0) + addonLines.filter(x => x.a.addon_type === "recurring").reduce((n, x) => n + x.q * x.a.price_cents, 0) : parsedItems.reduce((n, i) => n + i.quantity * (i.unit_price_cents ?? 0), 0);

  async function save(e: FormEvent) {
    e.preventDefault();
    const count = /^\d{1,3}$/.test(every) ? Number(every) : 0, cyc = cycles ? (/^\d{1,4}$/.test(cycles) ? Number(cycles) : 0) : null;
    const common = { contact_id: Number(contact), mode, start_date: start, reference: reference.trim(), notes };
    if (!contact || !start || (mode === "plan" ? !plan || qty < 1 || addonLines.some(x => x.q < 1) : count < 1 || cyc === 0 || parsedItems.some(i => !i.description || i.quantity < 1 || i.unit_price_cents == null) || recurringTotal <= 0)) { setError("Choose a customer and fill in the details."); return; }
    const body = mode === "plan"
      ? { ...common, plan_id: plan!.id, quantity: qty, addons: addonLines.map(x => ({ addon_id: x.a.id, quantity: x.q })), coupon_code: coupon.trim() }
      : { ...common, interval_count: count, interval_unit: unit, billing_cycles: cyc, items: parsedItems };
    if (await post("/v1/billing/subscriptions", body, "Subscription created. No invoice yet: use Run billing.")) { setOpen(false); setPicked({}); setCoupon(""); setItems([blankItem()]); }
  }
  async function run() {
    const r = await post("/v1/billing/subscriptions/run", { as_of: asOf }, "");
    if (r) { const made = (r.created_invoice_ids as number[]).length, skipped = Number(r.skipped ?? 0); setNotice(`${made} invoice draft${made === 1 ? "" : "s"} created${skipped ? `, ${skipped} cycle${skipped === 1 ? "" : "s"} skipped (nothing to bill)` : ""}. Review and post them under Invoices.`); }
  }
  const setItem = (i: number, patch: object) => setItems(items.map((v, j) => i === j ? { ...v, ...patch } : v));

  return <>
    <PageHeader title="Subscriptions" description="Bill a customer on a repeating schedule." />
    <p className="notice">SGD, no GST. Running billing creates an invoice draft for each cycle that is due; nothing is posted, charged or emailed by itself. Prices are copied when you subscribe, so later catalog changes do not alter a subscription. To change a plan, cancel and create a new subscription instead.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <div className="form-actions"><button disabled={busy || open} onClick={() => { setOpen(true); setError(""); setNotice(""); }}>New subscription</button>
      <label htmlFor="sub-asof">Bill cycles due up to</label><DateInput id="sub-asof" value={asOf} onChange={e => setAsOf(e.target.value)}/><button className="button-secondary" disabled={busy || !asOf} onClick={() => void run()}>Run billing</button></div>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Subscription</legend><div className="form-grid">
        <div><label htmlFor="sub-customer">Customer name</label><select id="sub-customer" required value={contact} onChange={e => setContact(e.target.value)}><option value="">Choose customer</option>{contacts.filter(c => c.role !== "supplier").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="sub-mode">Create subscription for</label><select id="sub-mode" value={mode} onChange={e => setMode(e.target.value as "plan" | "items")}><option value="plan">Product (plan &amp; addons)</option><option value="items">Items</option></select></div>
        <div><label htmlFor="sub-start">Start date</label><DateInput id="sub-start" required value={start} onChange={e => setStart(e.target.value)}/></div>
      </div></fieldset>
      {mode === "plan" ? <fieldset disabled={busy}><legend>Plan and addons</legend>
        {plans.length === 0 ? <p>You have not added any plans yet. Add one under Product catalog.</p> : <div className="form-grid">
          <div><label htmlFor="sub-plan">Plan</label><select id="sub-plan" required value={planId} onChange={e => { setPlanId(e.target.value); setPicked({}); }}><option value="">Choose plan</option>{plans.map(p => <option key={p.id} value={p.id}>{p.name} — {formatCents(p.price_cents)} every {unitLabel(p.interval_count, p.interval_unit)}</option>)}</select></div>
          <div><label htmlFor="sub-qty">Quantity</label><input id="sub-qty" required inputMode="numeric" value={quantity} onChange={e => setQuantity(e.target.value)} /></div>
          <div><label htmlFor="sub-coupon">Coupon code (optional)</label><input id="sub-coupon" maxLength={50} value={coupon} onChange={e => setCoupon(e.target.value)} /></div>
        </div>}
        {plan && <p className="section-help">{plan.trial_days > 0 ? `Free trial ${plan.trial_days} days: first invoice is dated ${plan.trial_days} days after the start date. ` : ""}{plan.setup_fee_cents > 0 ? `Setup fee ${formatCents(plan.setup_fee_cents)} is added to the first invoice.` : ""}</p>}
        {plan && available.length > 0 && <><h3>Addons</h3>{available.map(a => <div key={a.id} className="form-grid"><label className="check-label"><input type="checkbox" checked={picked[a.id] !== undefined} onChange={e => setPicked(p => { const n = { ...p }; if (e.target.checked) n[a.id] = "1"; else delete n[a.id]; return n; })} />{a.name} — {formatCents(a.price_cents)} ({a.addon_type === "one_time" ? "one-time" : "recurring"})</label>{picked[a.id] !== undefined && <div><label htmlFor={`sub-addon-${a.id}`}>Quantity of {a.name}</label><input id={`sub-addon-${a.id}`} inputMode="numeric" value={picked[a.id]} onChange={e => setPicked(p => ({ ...p, [a.id]: e.target.value }))} /></div>}</div>)}</>}
      </fieldset> : <fieldset disabled={busy}><legend>Items</legend><div className="form-grid">
        <div><label htmlFor="sub-every">Repeat every</label><input id="sub-every" required inputMode="numeric" value={every} onChange={e => setEvery(e.target.value)} /></div>
        <div><label htmlFor="sub-unit">Unit</label><select id="sub-unit" value={unit} onChange={e => setUnit(e.target.value)}><option value="week">Week(s)</option><option value="month">Month(s)</option><option value="year">Year(s)</option></select></div>
        <div><label htmlFor="sub-cycles">Billing cycles (blank = until cancelled)</label><input id="sub-cycles" inputMode="numeric" value={cycles} onChange={e => setCycles(e.target.value)} /></div></div>
        {items.map((it, i) => <fieldset key={i}><legend>Item {i + 1}</legend><div className="form-grid">
          <div><label htmlFor={`sub-d-${i}`}>Item details</label><input id={`sub-d-${i}`} required maxLength={200} value={it.description} onChange={e => setItem(i, { description: e.target.value })} /></div>
          <div><label htmlFor={`sub-q-${i}`}>Quantity</label><input id={`sub-q-${i}`} required inputMode="numeric" value={it.quantity} onChange={e => setItem(i, { quantity: e.target.value })} /></div>
          <div><label htmlFor={`sub-r-${i}`}>Rate (SGD)</label><input id={`sub-r-${i}`} required inputMode="decimal" value={it.rate} onChange={e => setItem(i, { rate: e.target.value })} /></div>
        </div>{items.length > 1 && <button type="button" className="button-secondary" onClick={() => setItems(items.filter((_, j) => j !== i))}>Remove item {i + 1}</button>}</fieldset>)}
        <button type="button" className="button-secondary" disabled={items.length >= 200} onClick={() => setItems([...items, blankItem()])}>Add new row</button>
      </fieldset>}
      <fieldset disabled={busy}><legend>Reference and notes</legend><label htmlFor="sub-ref">Reference#</label><input id="sub-ref" maxLength={100} value={reference} onChange={e => setReference(e.target.value)} /><label htmlFor="sub-notes">Notes</label><textarea id="sub-notes" rows={3} maxLength={4000} value={notes} onChange={e => setNotes(e.target.value)} /></fieldset>
      <p aria-live="polite">Each later invoice: {formatCents(recurringTotal)} before any coupon.</p>
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Create subscription"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {rows === null && !error && <p role="status">Loading subscriptions…</p>}{rows?.length === 0 && <p>No subscriptions yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 subscriptions</caption><thead><tr><th>Customer</th><th>What</th><th>Every</th><th>Cycles billed</th><th>Next bill</th><th>Coupon</th><th>Status</th><th>Actions</th></tr></thead><tbody>{rows.map(s => <tr key={s.id}><td>{s.customer_name}</td><td>{s.mode === "plan" ? `${s.plan_name} × ${s.quantity}` : s.recurring_lines.map(l => l.description).join(", ")}</td><td>{unitLabel(s.interval_count, s.interval_unit)}</td><td>{s.cycles_billed}{s.billing_cycles ? ` / ${s.billing_cycles}` : ""}</td><td>{s.next_bill_date ?? "—"}</td><td>{s.coupon?.code ?? "—"}</td><td>{s.status}</td><td>{canWrite && s.status === "active" && <button className="button-secondary" disabled={busy} onClick={() => void post(`/v1/billing/subscriptions/${s.id}/stage`, { stage: "cancel" }, "Subscription cancelled. Invoices already created stay.")}>Cancel<span className="sr-only"> subscription for {s.customer_name}</span></button>}</td></tr>)}</tbody></table></div>}
  </>;
}
