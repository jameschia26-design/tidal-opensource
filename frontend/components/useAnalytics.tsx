"use client";
import { useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

export type Month = { month: string; revenue_cents: number; expense_cents: number };
export type Totals = { revenue_cents: number; expense_cents: number; gross_profit_cents: number; other_income_cents: number; other_expense_cents: number; net_cents: number; bank_cents: number; receivable_cents: number; payable_cents: number; asset_cents: number; liability_cents: number };
export type Ratios = { net_margin_bp: number; current_ratio_bp: number; debtor_days: number; creditor_days: number; cash_runway_months: number };
export type Forecast = { month: string; in_cents: number; out_cents: number };
export type AnalyticsData = { from: string; to: string; series: Month[]; totals: Totals; ratios: Ratios; forecast: Forecast[] };

// The five analytics screens share one endpoint and one date-range form. Keeping the request here
// means a range change behaves the same everywhere and no screen invents its own measure.
export function useAnalytics(orgId: number | null) {
  const [from, setFrom] = useState(""), [to, setTo] = useState("");
  const [data, setData] = useState<AnalyticsData | null>(null);
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!orgId || busy) return;
    setData(null); setError(""); setBusy(true);
    try { setData(await api<AnalyticsData>(`/v1/reports/analytics?org_id=${orgId}&from=${from}&to=${to}`)); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  const reset = () => setData(null);
  return { from, setFrom, to, setTo, data, busy, error, run, reset };
}

export function DateRange({ id, state }: { id: string; state: ReturnType<typeof useAnalytics> }) {
  const { from, setFrom, to, setTo, busy, run, reset } = state;
  return <form onSubmit={run} className="inline-form">
    <div><label htmlFor={`${id}-from`}>From</label><DateInput id={`${id}-from`} required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); reset(); }}/></div>
    <div><label htmlFor={`${id}-to`}>To</label><DateInput id={`${id}-to`} required disabled={busy} value={to} onChange={e => { setTo(e.target.value); reset(); }}/></div>
    <button disabled={busy}>{busy ? "Running…" : "Update"}</button>
  </form>;
}
