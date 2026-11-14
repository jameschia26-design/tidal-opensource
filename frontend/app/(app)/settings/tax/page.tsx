"use client";
// Financial settings — tax block: basis, tax ID, display name, tax period and the default treatment
// for sales and purchases. Rates come from the dated catalogue and cannot be edited here.
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Settings = { tax_basis: string; tax_id: string; tax_display_name: string; tax_period: string; default_sales_treatment: string; default_purchase_treatment: string };
type Rate = { code: string; name: string; rate_bp: number; kind: string; applies_to: string; effective_from: string; effective_to: string };
const TREATMENTS = [["exclusive", "Tax exclusive"], ["inclusive", "Tax inclusive"]] as const;
const PERIODS = [["monthly", "Monthly"], ["quarterly", "Quarterly"], ["twice_monthly", "Twice monthly"]] as const;

export default function TaxSettings() {
  const { selected } = useOrg();
  return <><PageHeader title="Tax settings" description="Tax basis, tax ID, tax period and default treatments." />{!selected ? <p className="notice">Choose a company first.</p> : <TaxWorkspace key={`${selected.id}-${selected.role}`} orgId={selected.id} canWrite={selected.role === "owner" || selected.role === "admin"} />}</>;
}

function TaxWorkspace({ orgId, canWrite }: { orgId: number; canWrite: boolean }) {
  const active = useRef(true);
  useEffect(() => () => { active.current = false; }, []);
  const [form, setForm] = useState<Settings | null>(null);
  const [rates, setRates] = useState<Rate[]>([]);
  const [accounts, setAccounts] = useState<{ code: string; name: string; tax_rate_code: string }[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [custom, setCustom] = useState({ code: "", name: "", rate: "", kind: "standard", applies_to: "both", effective_from: "", effective_to: "" });

  useEffect(() => {
    let live = true;
    Promise.all([api<Settings>(`/v1/tax/settings?org_id=${orgId}`), api<{ rates: Rate[] }>(`/v1/tax/rates?org_id=${orgId}`), api<{ accounts: { code: string; name: string; tax_rate_code: string }[] }>(`/v1/accounts?org_id=${orgId}`)])
      .then(([s, r, a]) => { if (live) { setForm(s); setRates(r.rates); setAccounts(a.accounts); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!form || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const saved = await api<Settings>("/v1/tax/settings", { method: "PUT", body: JSON.stringify({ org_id: orgId, ...form }) });
      if (active.current) { setForm(saved); setNotice("Tax settings saved."); }
    } catch (err) {
      const e2 = err as ApiError;
      if (active.current) setError(messageFor(e2.error, e2.status));
    } finally {
      setBusy(false);
    }
  }

  async function createRate(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api("/v1/tax/rates", { method: "POST", body: JSON.stringify({ org_id: orgId, code: custom.code, name: custom.name, rate_bp: Math.round(Number(custom.rate) * 100), kind: custom.kind, applies_to: custom.applies_to, effective_from: custom.effective_from, effective_to: custom.effective_to }) });
      const r = await api<{ rates: Rate[] }>(`/v1/tax/rates?org_id=${orgId}`);
      if (active.current) { setRates(r.rates); setNotice("Tax rate added."); setCustom({ code: "", name: "", rate: "", kind: "standard", applies_to: "both", effective_from: "", effective_to: "" }); }
    } catch (err) { const e2 = err as ApiError; if (active.current) setError(messageFor(e2.error, e2.status)); }
    finally { if (active.current) setBusy(false); }
  }

  if (!form) return <>{error ? <p role="alert" className="form-error">{error}</p> : <p role="status">Loading tax settings…</p>}</>;

  return <>
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy || !canWrite}>
        <legend>Financial settings</legend>
        <div className="form-grid">
          <div><label htmlFor="tax-basis">Tax basis</label>
            <select id="tax-basis" value={form.tax_basis} onChange={e => setForm({ ...form, tax_basis: e.target.value })}>
              <option value="accrual">Accrual (invoice date)</option><option value="payment">Cash (payment date)</option>
            </select></div>
          <div><label htmlFor="tax-id">Tax ID number <span className="field-optional">Optional</span></label>
            <input id="tax-id" maxLength={30} value={form.tax_id} onChange={e => setForm({ ...form, tax_id: e.target.value })} />
            <p className="section-help">The company GST registration number. It is the same field the company profile holds.</p></div>
          <div><label htmlFor="tax-name">Tax ID display name</label>
            <input id="tax-name" required maxLength={40} value={form.tax_display_name} onChange={e => setForm({ ...form, tax_display_name: e.target.value })} /></div>
          <div><label htmlFor="tax-period">Tax period</label>
            <select id="tax-period" value={form.tax_period} onChange={e => setForm({ ...form, tax_period: e.target.value })}>
              {PERIODS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select></div>
          <div><label htmlFor="tax-sales-default">For sales</label>
            <select id="tax-sales-default" value={form.default_sales_treatment} onChange={e => setForm({ ...form, default_sales_treatment: e.target.value })}>
              {TREATMENTS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select><p className="section-help">A new invoice starts with this treatment; the invoice itself can change it.</p></div>
          <div><label htmlFor="tax-purchase-default">For purchases</label>
            <select id="tax-purchase-default" value={form.default_purchase_treatment} onChange={e => setForm({ ...form, default_purchase_treatment: e.target.value })}>
              {TREATMENTS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select></div>
        </div>
        {!canWrite && <p className="section-help">Only an owner or admin can change these settings.</p>}
      </fieldset>
      {canWrite && <button disabled={busy}>Save tax settings</button>}
    </form>
    <h2>Tax rates</h2>
    <p className="section-help">Singapore GST rates with their dates. A document uses the rate in force on its own date, and a line can still override the account default. Imported services, low-value goods and reverse charge are self-assessed on purchase bills. A custom rate is a single rate; component breakdowns are not stored separately.</p>
    <div className="table-scroll"><table className="doc-table"><caption>Rate catalogue in force today</caption>
      <thead><tr><th scope="col">Code</th><th scope="col">Name</th><th scope="col" className="num">Rate</th><th scope="col">Kind</th><th scope="col">Applies to</th><th scope="col">From</th><th scope="col">To</th><th scope="col">Accounts using this rate</th></tr></thead>
      <tbody>{rates.map(r => <tr key={`${r.code}-${r.effective_from}-${r.applies_to}`}>
        <th scope="row">{r.code}</th><td>{r.name}</td><td className="num">{(r.rate_bp / 100).toFixed(2)}%</td><td>{r.kind.replace(/_/g, " ")}</td>
        <td>{r.applies_to}</td><td>{r.effective_from}</td><td>{r.effective_to || "open"}</td>
        <td>{accounts.filter(a => a.tax_rate_code === r.code).map(a => `${a.code} — ${a.name}`).join(", ") || "—"}</td>
      </tr>)}</tbody>
    </table></div>
    {canWrite && <form className="record-form" onSubmit={createRate}><fieldset disabled={busy}><legend>Add a tax rate</legend>
      <div className="form-grid">
        <div><label htmlFor="rate-code">Code</label><input id="rate-code" required maxLength={20} pattern="[A-Za-z0-9_]+" value={custom.code} onChange={e => setCustom({ ...custom, code: e.target.value.toUpperCase() })} /></div>
        <div><label htmlFor="rate-name">Name</label><input id="rate-name" required maxLength={100} value={custom.name} onChange={e => setCustom({ ...custom, name: e.target.value })} /></div>
        <div><label htmlFor="rate-rate">Rate (%)</label><input id="rate-rate" required inputMode="decimal" value={custom.rate} onChange={e => setCustom({ ...custom, rate: e.target.value })} /></div>
        <div><label htmlFor="rate-kind">Kind</label><select id="rate-kind" value={custom.kind} onChange={e => setCustom({ ...custom, kind: e.target.value })}><option value="standard">Standard</option><option value="zero">Zero-rated</option><option value="exempt">Exempt</option><option value="out_of_scope">Out of scope</option><option value="no_tax">No tax</option><option value="import">Imported services</option><option value="reverse_charge">Reverse charge</option></select></div>
        <div><label htmlFor="rate-applies">Applies to</label><select id="rate-applies" value={custom.applies_to} onChange={e => setCustom({ ...custom, applies_to: e.target.value })}><option value="both">Sales and purchases</option><option value="sale">Sales</option><option value="purchase">Purchases</option></select></div>
        <div><label htmlFor="rate-from">Effective from</label><input id="rate-from" type="date" required value={custom.effective_from} onChange={e => setCustom({ ...custom, effective_from: e.target.value })} /></div>
        <div><label htmlFor="rate-to">Effective to <span className="field-optional">Optional</span></label><input id="rate-to" type="date" value={custom.effective_to} onChange={e => setCustom({ ...custom, effective_to: e.target.value })} /></div>
      </div><button disabled={busy}>Add tax rate</button></fieldset></form>}
  </>;
}
