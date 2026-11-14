"use client";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { DateRange, useAnalytics } from "@/components/useAnalytics";
import { useEffect, useState } from "react";
import { formatCents } from "@/lib/money";
import { api } from "@/lib/api";

const DEFAULT_TARGETS: Record<string, number> = { net_margin_bp: 1000, current_ratio_bp: 12000, debtor_days: 45, creditor_days: 60, cash_runway_months: 3 };

// Band returns a plain-language reading of one ratio. The thresholds are deliberately simple and
// stated on the page, so nobody has to guess what "healthy" means here.
function band(value: number, good: number, warnIsLow: boolean) {
  if (warnIsLow) return value >= good ? "Healthy" : value > 0 ? "Watch" : "No data";
  return value <= good ? "Healthy" : "Watch";
}

export default function Scorecard() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const state = useAnalytics(orgId);
  const { data, busy, error } = state;
  const [targets, setTargets] = useState(DEFAULT_TARGETS);
  const [saved, setSaved] = useState("");
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    api<{ targets: Record<string, number> }>(`/v1/scorecard-targets?org_id=${orgId}`)
      .then(r => { if (live) setTargets({ ...DEFAULT_TARGETS, ...r.targets }); })
      .catch(() => { if (live) setTargets(DEFAULT_TARGETS); });
    return () => { live = false; };
  }, [orgId]);
  async function saveTargets() {
    if (!orgId) return;
    await api("/v1/scorecard-targets", { method: "PUT", body: JSON.stringify({ org_id: orgId, targets }) });
    setSaved("Targets saved for this company.");
  }
  const target = (key: string) => targets[key] ?? DEFAULT_TARGETS[key];
  return <ReportFrame title="Business health scorecard" description="A short list of ratios with a plain reading, for the chosen period." filters={<DateRange id="sc" state={state} />}>
    <form className="record-form" onSubmit={e => { e.preventDefault(); void saveTargets(); }}><fieldset><legend>Targets</legend><p className="section-help">A measure reads healthy when it reaches its target. Net margin and current ratio are entered as percentages; days and months as whole numbers.</p><div className="form-grid">
      {[["net_margin_bp","Net margin %","percent"],["current_ratio_bp","Current ratio","ratio"],["debtor_days","Debtor days","days"],["creditor_days","Creditor days","days"],["cash_runway_months","Cash runway months","months"]].map(([key,label,kind]) => <div key={key}><label htmlFor={`target-${key}`}>{label}</label><input id={`target-${key}`} inputMode="decimal" value={kind === "percent" || kind === "ratio" ? (target(key) / 100).toString() : String(target(key))} onChange={e => { const n = Number(e.target.value); const v = Number.isFinite(n) && n >= 0 ? (kind === "percent" || kind === "ratio" ? Math.round(n * 100) : Math.round(n)) : 0; setTargets(t => ({ ...t, [key]: v })); setSaved(""); }} /></div>)}
    </div><button>Save targets</button>{saved && <p role="status">{saved}</p>}</fieldset></form>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}{busy && <p>Running…</p>}
    {data && <div className="table-scroll"><table className="doc-table"><caption>Business health</caption><thead><tr><th scope="col">Measure</th><th scope="col">Value</th><th scope="col">Reading</th></tr></thead><tbody>
      <tr><th scope="row">Net margin</th><td>{(data.ratios.net_margin_bp / 100).toFixed(2)}%</td><td>{band(data.ratios.net_margin_bp, target("net_margin_bp"), true)}</td></tr>
      <tr><th scope="row">Current ratio</th><td>{(data.ratios.current_ratio_bp / 100).toFixed(2)}</td><td>{band(data.ratios.current_ratio_bp, target("current_ratio_bp"), true)}</td></tr>
      <tr><th scope="row">Debtor days</th><td>{data.ratios.debtor_days}</td><td>{band(data.ratios.debtor_days, target("debtor_days"), false)}</td></tr>
      <tr><th scope="row">Creditor days</th><td>{data.ratios.creditor_days}</td><td>{band(data.ratios.creditor_days, target("creditor_days"), false)}</td></tr>
      <tr><th scope="row">Cash runway (months)</th><td>{data.ratios.cash_runway_months}</td><td>{band(data.ratios.cash_runway_months, target("cash_runway_months"), true)}</td></tr>
    </tbody></table></div>}
    {data && <div className="table-scroll"><table className="doc-table"><caption>Figures behind the readings</caption><tbody>
      <tr><th scope="row">Revenue</th><td>{formatCents(data.totals.revenue_cents)}</td></tr>
      <tr><th scope="row">Spending</th><td>{formatCents(data.totals.expense_cents)}</td></tr>
      <tr><th scope="row">Net profit</th><td>{formatCents(data.totals.net_cents)}</td></tr>
      <tr><th scope="row">Bank</th><td>{formatCents(data.totals.bank_cents)}</td></tr>
      <tr><th scope="row">Assets</th><td>{formatCents(data.totals.asset_cents)}</td></tr>
      <tr><th scope="row">Liabilities</th><td>{formatCents(data.totals.liability_cents)}</td></tr>
    </tbody></table></div>}
  </ReportFrame>;
}
