"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";

type Product = { id: number; name: string; description: string };
type Plan = { id: number; product_id: number; name: string; code: string; interval_count: number; interval_unit: string; billing_cycles: number | null; description: string; unit_name: string; price_cents: number; trial_days: number; setup_fee_cents: number; item_type: string; show_in_widget: boolean; allow_plan_change: boolean; plan_account_id: number; setup_fee_account_id: number; active: boolean };
type Addon = { id: number; product_id: number; name: string; code: string; description: string; addon_type: string; interval_count: number; interval_unit: string; unit_name: string; price_cents: number; plan_ids: number[]; item_type: string; show_in_widget: boolean; allow_portal_association: boolean; account_id: number; active: boolean };
type Coupon = { id: number; product_id: number; name: string; code: string; discount_type: string; discount_cents: number; discount_basis_points: number; redemption_type: string; redemption_cycles: number; applicability: string; plan_ids: number[]; addon_ids: number[]; expires_on: string | null; max_redemptions: number | null; redemptions: number; active: boolean };
type Account = { id: number; code: string; name: string; type: string };
const unitLabel = (n: number, u: string) => `${n} ${u}${n === 1 ? "" : "s"}`;
const TABS = ["Plans", "Addons", "Coupons", "Products"] as const;

export default function CatalogPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [tab, setTab] = useState<(typeof TABS)[number]>("Plans");
  const [products, setProducts] = useState<Product[]>([]);
  const [plans, setPlans] = useState<Plan[] | null>(null);
  const [addons, setAddons] = useState<Addon[]>([]);
  const [coupons, setCoupons] = useState<Coupon[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<{ kind: "billing-plans" | "billing-addons"; id: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [f, setF] = useState<Record<string, string>>({});
  const [planPick, setPlanPick] = useState<number[]>([]);
  const [addonPick, setAddonPick] = useState<number[]>([]);
  const set = (k: string, v: string) => setF(x => ({ ...x, [k]: v }));

  async function load() {
    if (orgId == null) return;
    const [p, pl, ad, co, ac] = await Promise.all([
      api<{ rows: Product[] }>(`/v1/billing/products?org_id=${orgId}`),
      api<{ rows: Plan[] }>(`/v1/billing/plans?org_id=${orgId}`),
      api<{ rows: Addon[] }>(`/v1/billing/addons?org_id=${orgId}`),
      api<{ rows: Coupon[] }>(`/v1/billing/coupons?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
    ]);
    if (!live.current) return;
    setProducts(p.rows); setPlans(pl.rows); setAddons(ad.rows); setCoupons(co.rows); setAccounts(ac.accounts);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function openForm() { setOpen(true); setDetail(null); setError(""); setNotice(""); setPlanPick([]); setAddonPick([]); setF({ product_id: products[0] ? String(products[0].id) : "", interval_count: "1", interval_unit: "month", cycles: "", pricing_model: "per_unit", item_type: "service", addon_type: "recurring", discount_type: "flat", redemption_type: "one_time", applicability: "both", widget: "yes", change: "yes", portal: "yes" }); }
  async function post(path: string, body: object, done: string) {
    if (busy || orgId == null) return false;
    setBusy(true); setError(""); setNotice("");
    try { await api(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) }); if (!live.current) return false; setNotice(done); await load(); return true; }
    catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } return false; }
    finally { if (live.current) setBusy(false); }
  }
  const toggle = (list: number[], id: number) => list.includes(id) ? list.filter(x => x !== id) : [...list, id];
  const whole = (s: string | undefined) => (s && /^\d{1,7}$/.test(s) ? Number(s) : null);

  async function save(e: FormEvent) {
    e.preventDefault();
    const bad = () => setError("Some details are missing or not valid. Check the form.");
    if (tab === "Products") {
      if (!f.name?.trim()) return bad();
      if (await post("/v1/billing/products", { name: f.name.trim(), description: f.description ?? "" }, "Product saved.")) setOpen(false);
      return;
    }
    const product_id = Number(f.product_id);
    if (!product_id || !f.name?.trim() || !f.code?.trim()) return bad();
    if (tab === "Plans") {
      const price = parseCents(f.price ?? ""), setup = parseCents(f.setup || "0"), trial = whole(f.trial || "0"), count = whole(f.interval_count), cycles = f.cycles ? whole(f.cycles) : undefined;
      if (price == null || setup == null || trial == null || count == null || cycles === null) return bad();
      const body = { product_id, name: f.name.trim(), code: f.code.trim(), interval_count: count, interval_unit: f.interval_unit, billing_cycles: cycles ?? null, description: f.description ?? "", pricing_model: "per_unit", unit_name: f.unit_name ?? "", price_cents: price, trial_days: trial, setup_fee_cents: setup, item_type: f.item_type, tax: "none", show_in_widget: f.widget === "yes", allow_plan_change: f.change === "yes", plan_account_id: Number(f.plan_account_id || 0), setup_fee_account_id: Number(f.setup_account_id || 0) };
      if (await post("/v1/billing/plans", body, "Plan saved.")) setOpen(false);
    } else if (tab === "Addons") {
      const price = parseCents(f.price ?? ""), count = whole(f.interval_count);
      if (price == null || count == null) return bad();
      const body = { product_id, name: f.name.trim(), code: f.code.trim(), description: f.description ?? "", addon_type: f.addon_type, interval_count: count, interval_unit: f.interval_unit, pricing_model: "per_unit", unit_name: f.unit_name ?? "", price_cents: price, plan_ids: planPick, item_type: f.item_type, tax: "none", show_in_widget: f.widget === "yes", allow_portal_association: f.portal === "yes", account_id: Number(f.account_id || 0) };
      if (await post("/v1/billing/addons", body, "Addon saved.")) setOpen(false);
    } else {
      const flat = f.discount_type === "flat";
      const amount = flat ? parseCents(f.discount ?? "") : Math.round(Number(f.discount) * 100);
      const cycles = f.redemption_type === "limited" ? whole(f.redemption_cycles) : 0, max = f.max ? whole(f.max) : null;
      if (amount == null || !(amount > 0) || Number.isNaN(amount) || cycles == null || (f.max && max == null)) return bad();
      const body = { product_id, name: f.name.trim(), code: f.code.trim(), discount_type: f.discount_type, discount_cents: flat ? amount : 0, discount_basis_points: flat ? 0 : amount, redemption_type: f.redemption_type, redemption_cycles: cycles, applicability: f.applicability, plan_ids: planPick, addon_ids: addonPick, expires_on: f.expires_on ?? "", max_redemptions: max };
      if (await post("/v1/billing/coupons", body, "Coupon saved.")) setOpen(false);
    }
  }
  const stage = (kind: string, id: number, to: string) => void post(`/v1/billing/${kind}/${id}/stage`, { stage: to }, to === "activate" ? "Activated." : "Deactivated. Existing subscriptions keep their prices.");
  const productName = (id: number) => products.find(p => p.id === id)?.name ?? "—";
  const acctName = (id: number) => accounts.find(a => a.id === id)?.name ?? "—";
  const revenue = accounts.filter(a => a.type === "revenue");
  const productPlans = plans?.filter(p => p.product_id === Number(f.product_id)) ?? [];
  const productAddons = addons.filter(a => a.product_id === Number(f.product_id) && a.addon_type === "recurring");
  const select = (id: string, label: string, key: string, opts: [string, string][], help?: string) => <div><label htmlFor={id}>{label}</label><select id={id} value={f[key] ?? ""} onChange={e => set(key, e.target.value)}>{opts.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>{help && <p className="section-help">{help}</p>}</div>;
  const text = (id: string, label: string, key: string, extra: object = {}, help?: string) => <div><label htmlFor={id}>{label}</label><input id={id} value={f[key] ?? ""} onChange={e => set(key, e.target.value)} {...extra} />{help && <p className="section-help">{help}</p>}</div>;
  const productSelect = <div><label htmlFor="bc-product">Product</label><select id="bc-product" required value={f.product_id ?? ""} onChange={e => { set("product_id", e.target.value); setPlanPick([]); setAddonPick([]); }}><option value="">Choose product</option>{products.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></div>;
  const interval = <><div><label htmlFor="bc-count">{tab === "Plans" ? "Billing frequency" : "Pricing interval"}</label><input id="bc-count" required inputMode="numeric" value={f.interval_count ?? ""} onChange={e => set("interval_count", e.target.value)} /></div>{select("bc-unit", "Unit", "interval_unit", [["week", "Week(s)"], ["month", "Month(s)"], ["year", "Year(s)"]])}</>;
  const accountSelect = (id: string, label: string, key: string) => <div><label htmlFor={id}>{label}</label><select id={id} value={f[key] ?? ""} onChange={e => set(key, e.target.value)}><option value="">4000 — Sales (default)</option>{revenue.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>;
  const yesNo = (id: string, label: string, key: string): React.ReactNode => select(id, label, key, [["yes", "Yes"], ["no", "No"]]);
  const noProducts = products.length === 0 && tab !== "Products";

  return <>
    <PageHeader title="Product catalog" description="Plans, addons and coupons you can put on a subscription." />
    <p className="notice">SGD, no GST. Pricing is per unit only. Saved catalog entries cannot be edited; deactivate one and add a new one instead. Attach files from the details of a saved plan or addon.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    <div role="group" aria-label="Catalog section" className="form-actions">{TABS.map(t => <button key={t} type="button" className={tab === t ? "" : "button-secondary"} aria-pressed={tab === t} onClick={() => { setTab(t); setOpen(false); setDetail(null); }}>{t}</button>)}</div>
    {canWrite && <button disabled={busy || open} onClick={openForm}>New {tab === "Plans" ? "plan" : tab === "Addons" ? "addon" : tab === "Coupons" ? "coupon" : "product"}</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      {tab === "Products" ? <fieldset disabled={busy}><legend>New product</legend>{text("bc-name", "Product name", "name", { required: true, maxLength: 100 })}{text("bc-desc", "Description", "description", { maxLength: 4000 })}</fieldset>
      : noProducts ? <p role="alert" className="form-error">Add a product first (Products tab).</p>
      : <>
        <fieldset disabled={busy}><legend>{tab === "Plans" ? "Plan" : tab === "Addons" ? "Addon" : "Coupon"} details</legend><div className="form-grid">
          {productSelect}
          {text("bc-name", tab === "Coupons" ? "Coupon name" : `${tab === "Plans" ? "Plan" : "Addon"} name`, "name", { required: true, maxLength: 100 })}
          {text("bc-code", tab === "Coupons" ? "Coupon code" : `${tab === "Plans" ? "Plan" : "Addon"} code`, "code", { required: true, maxLength: 50 }, "Letters, numbers, dash, underscore.")}
        </div>
        {tab === "Plans" && <div className="form-grid">{interval}{text("bc-cycles", "Billing cycles (blank = auto-renews until cancelled)", "cycles", { inputMode: "numeric" })}</div>}
        {tab === "Addons" && <div className="form-grid">{select("bc-type", "Addon type", "addon_type", [["recurring", "Recurring"], ["one_time", "One-time"]])}{f.addon_type === "recurring" && interval}</div>}
        {tab !== "Coupons" && text("bc-desc", `${tab === "Plans" ? "Plan" : "Addon"} description`, "description", { maxLength: 4000 })}
        </fieldset>
        {tab !== "Coupons" && <fieldset disabled={busy}><legend>Pricing</legend><div className="form-grid">
          {select("bc-model", "Pricing model", "pricing_model", [["per_unit", "Per unit"]], "Other pricing models are not available.")}
          {text("bc-unit-name", "Unit name", "unit_name", { maxLength: 50 })}
          {text("bc-price", "Price (SGD per unit)", "price", { required: true, inputMode: "decimal" })}
          {tab === "Plans" && <>{text("bc-trial", "Free trial (days)", "trial", { inputMode: "numeric" })}{text("bc-setup", "Setup fee (SGD)", "setup", { inputMode: "decimal" })}</>}
          {select("bc-itype", "Type", "item_type", [["service", "Service"], ["goods", "Goods"]])}
          <div><label htmlFor="bc-tax">Tax</label><select id="bc-tax" value="none" disabled><option value="none">No GST</option></select><p className="section-help">Fixed. Tax groups are not available.</p></div>
        </div></fieldset>}
        {tab === "Addons" && <fieldset disabled={busy}><legend>Associated plans</legend><p className="section-help">Tick plans this addon can be used with. None ticked = all plans of the product.</p>{productPlans.map(p => <label key={p.id} className="check-label"><input type="checkbox" checked={planPick.includes(p.id)} onChange={() => setPlanPick(toggle(planPick, p.id))} />{p.name}</label>)}</fieldset>}
        {tab !== "Coupons" && <fieldset disabled={busy}><legend>Preferences and accounts</legend><div className="form-grid">
          {yesNo("bc-widget", "Show in pricing widgets", "widget")}
          {tab === "Plans" ? yesNo("bc-change", "Allow customers to change plan", "change") : yesNo("bc-portal", "Allow customers to associate addon", "portal")}
          {tab === "Plans" ? <>{accountSelect("bc-plan-acct", "Plan account", "plan_account_id")}{accountSelect("bc-setup-acct", "Setup fee account", "setup_account_id")}</> : accountSelect("bc-acct", "Account", "account_id")}
        </div><p className="section-help">Widgets and the customer portal are not built, so those two choices are only stored. Invoices from subscriptions currently post income to Sales; the account choice is kept for later.</p></fieldset>}
        {tab === "Coupons" && <>
          <fieldset disabled={busy}><legend>Discount</legend><div className="form-grid">
            {select("bc-dtype", "Discount type", "discount_type", [["flat", "Flat (SGD)"], ["percent", "Percent"]])}
            {text("bc-discount", f.discount_type === "percent" ? "Discount (%)" : "Discount (SGD)", "discount", { required: true, inputMode: "decimal" })}
            {select("bc-redeem", "Redemption type", "redemption_type", [["one_time", "One-time (first invoice)"], ["limited", "Limited cycles"], ["forever", "Every invoice"]])}
            {f.redemption_type === "limited" && text("bc-cycles", "Number of invoices", "redemption_cycles", { required: true, inputMode: "numeric" })}
            {select("bc-appl", "Applicability", "applicability", [["both", "Plan and addons"], ["plan", "Plan only"], ["addon", "Recurring addons only"]])}
            {text("bc-expires", "Expiration date", "expires_on", { type: "date" })}
            {text("bc-max", "Maximum redemptions", "max", { inputMode: "numeric" }, "Blank = no limit.")}
          </div></fieldset>
          <fieldset disabled={busy}><legend>Associate plans and addons</legend><p className="section-help">None ticked = all plans, all recurring addons.</p>
            {productPlans.map(p => <label key={p.id} className="check-label"><input type="checkbox" checked={planPick.includes(p.id)} onChange={() => setPlanPick(toggle(planPick, p.id))} />Plan: {p.name}</label>)}
            {productAddons.map(a => <label key={a.id} className="check-label"><input type="checkbox" checked={addonPick.includes(a.id)} onChange={() => setAddonPick(toggle(addonPick, a.id))} />Addon: {a.name}</label>)}
          </fieldset></>}
      </>}
      <div className="form-actions"><button disabled={busy || noProducts}>{busy ? "Saving…" : "Save"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {detail && orgId != null && <section className="record-form"><h2>Attachments</h2><Attachments key={`${detail.kind}-${detail.id}`} kind={detail.kind} id={detail.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras key={`x-${detail.kind}-${detail.id}`} kind={detail.kind} id={detail.id} orgId={orgId} readOnly={!canWrite} /><button className="button-secondary" onClick={() => setDetail(null)}>Close</button></section>}
    {plans === null && !error && <p role="status">Loading catalog…</p>}
    {tab === "Products" && <div className="table-scroll"><table className="doc-table"><caption>Products</caption><thead><tr><th>Name</th><th>Description</th></tr></thead><tbody>{products.map(p => <tr key={p.id}><td>{p.name}</td><td>{p.description || "—"}</td></tr>)}</tbody></table></div>}
    {tab === "Plans" && !!plans?.length && <div className="table-scroll"><table className="doc-table"><caption>Plans</caption><thead><tr><th>Name</th><th>Code</th><th>Product</th><th>Billed every</th><th>Price</th><th>Trial / setup</th><th>Accounts</th><th>Status</th><th>Actions</th></tr></thead><tbody>{plans.map(p => <tr key={p.id}><td>{p.name}</td><td>{p.code}</td><td>{productName(p.product_id)}</td><td>{unitLabel(p.interval_count, p.interval_unit)}{p.billing_cycles ? `, ${p.billing_cycles} cycles` : ", until cancelled"}</td><td>{formatCents(p.price_cents)}{p.unit_name && ` / ${p.unit_name}`}</td><td>{p.trial_days} days / {formatCents(p.setup_fee_cents)}</td><td>{acctName(p.plan_account_id)} / {acctName(p.setup_fee_account_id)}</td><td>{p.active ? "Active" : "Inactive"}</td><td><button className="button-secondary" onClick={() => { setDetail({ kind: "billing-plans", id: p.id }); setOpen(false); }}>Files<span className="sr-only"> for {p.name}</span></button>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => stage("plans", p.id, p.active ? "deactivate" : "activate")}>{p.active ? "Deactivate" : "Activate"}<span className="sr-only"> {p.name}</span></button>}</td></tr>)}</tbody></table></div>}
    {tab === "Addons" && !!addons.length && <div className="table-scroll"><table className="doc-table"><caption>Addons</caption><thead><tr><th>Name</th><th>Code</th><th>Product</th><th>Type</th><th>Price</th><th>Plans</th><th>Status</th><th>Actions</th></tr></thead><tbody>{addons.map(a => <tr key={a.id}><td>{a.name}</td><td>{a.code}</td><td>{productName(a.product_id)}</td><td>{a.addon_type === "one_time" ? "One-time" : `Every ${unitLabel(a.interval_count, a.interval_unit)}`}</td><td>{formatCents(a.price_cents)}{a.unit_name && ` / ${a.unit_name}`}</td><td>{a.plan_ids.length ? a.plan_ids.map(id => plans?.find(p => p.id === id)?.name ?? id).join(", ") : "All plans"}</td><td>{a.active ? "Active" : "Inactive"}</td><td><button className="button-secondary" onClick={() => { setDetail({ kind: "billing-addons", id: a.id }); setOpen(false); }}>Files<span className="sr-only"> for {a.name}</span></button>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => stage("addons", a.id, a.active ? "deactivate" : "activate")}>{a.active ? "Deactivate" : "Activate"}<span className="sr-only"> {a.name}</span></button>}</td></tr>)}</tbody></table></div>}
    {tab === "Coupons" && !!coupons.length && <div className="table-scroll"><table className="doc-table"><caption>Coupons</caption><thead><tr><th>Name</th><th>Code</th><th>Product</th><th>Discount</th><th>Redemption</th><th>Applies to</th><th>Expires</th><th>Used</th><th>Status</th><th>Actions</th></tr></thead><tbody>{coupons.map(c => <tr key={c.id}><td>{c.name}</td><td>{c.code}</td><td>{productName(c.product_id)}</td><td>{c.discount_type === "flat" ? formatCents(c.discount_cents) : `${c.discount_basis_points / 100}%`}</td><td>{c.redemption_type === "one_time" ? "One-time" : c.redemption_type === "forever" ? "Every invoice" : `${c.redemption_cycles} invoices`}</td><td>{c.applicability}</td><td>{c.expires_on ?? "—"}</td><td>{c.redemptions}{c.max_redemptions ? ` / ${c.max_redemptions}` : ""}</td><td>{c.active ? "Active" : "Inactive"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => stage("coupons", c.id, c.active ? "deactivate" : "activate")}>{c.active ? "Deactivate" : "Activate"}<span className="sr-only"> {c.name}</span></button>}</td></tr>)}</tbody></table></div>}
  </>;
}
