"use client";
import { useState, type FormEvent } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { parseCents } from "@/lib/money";
import DateInput from "@/components/DateInput";

export type MileageSettings = { expense_account_id: number; unit: string; default_rate_cents: number; rates: { start_date: string; rate_cents: number }[] };
export default function MileagePreferences({ orgId, initial, accounts, onSaved }: { orgId: number; initial: MileageSettings | null; accounts: { id: number; name: string; type: string }[]; onSaved: (v: MileageSettings) => void }) {
  const [account, setAccount] = useState(initial?.expense_account_id ?? 0);
  const [unit, setUnit] = useState(initial?.unit ?? "km");
  const [rate, setRate] = useState(((initial?.default_rate_cents ?? 0) / 100).toFixed(2));
  const [rates, setRates] = useState(initial?.rates.map(r => ({ start_date: r.start_date, rate: (r.rate_cents / 100).toFixed(2) })) ?? []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function save(e: FormEvent) {
    e.preventDefault(); if (busy) return;
    const base = parseCents(rate), parsed = rates.map(r => ({ start_date: r.start_date, rate_cents: parseCents(r.rate) }));
    if (!account || base == null || base < 0 || base > 100000000 || parsed.some(r => !r.start_date || r.rate_cents == null || r.rate_cents < 0 || r.rate_cents > 100000000)) { setError("Choose an expense account and valid mileage rates."); return; }
    setBusy(true); setError("");
    try { const r = await api<{ preferences: MileageSettings }>("/v1/mileage-preferences", { method: "PUT", body: JSON.stringify({ org_id: orgId, expense_account_id: account, unit, default_rate_cents: base, rates: parsed }) }); onSaved(r.preferences); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  return <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>Mileage preferences</legend><div className="form-grid"><div><label htmlFor="mileage-account">Default mileage account</label><select id="mileage-account" required value={account} onChange={e => setAccount(Number(e.target.value))}><option value={0}>Choose expense account</option>{accounts.filter(a => a.type === "expense").map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></div><div><label htmlFor="mileage-unit">Default unit</label><select id="mileage-unit" value={unit} onChange={e => setUnit(e.target.value)}><option value="km">Km</option><option value="mile">Mile</option></select></div><div><label htmlFor="mileage-default-rate">Default rate (SGD per unit)</label><input id="mileage-default-rate" required inputMode="decimal" value={rate} onChange={e => setRate(e.target.value)} /><p className="section-help">Applies before first dated rate.</p></div></div>{rates.map((r, i) => <div className="form-grid" key={i}><div><label htmlFor={`mileage-rate-date-${i}`}>Start date</label><DateInput id={`mileage-rate-date-${i}`} required value={r.start_date} onChange={e => setRates(rates.map((v, j) => i === j ? { ...v, start_date: e.target.value } : v))}/></div><div><label htmlFor={`mileage-rate-${i}`}>Mileage rate (SGD per unit)</label><input id={`mileage-rate-${i}`} required inputMode="decimal" value={r.rate} onChange={e => setRates(rates.map((v, j) => i === j ? { ...v, rate: e.target.value } : v))} /></div><button type="button" className="button-secondary" onClick={() => setRates(rates.filter((_, j) => i !== j))}>Remove rate {i + 1}</button></div>)}<button type="button" disabled={rates.length >= 100} className="button-secondary" onClick={() => setRates([...rates, { start_date: "", rate: "0.00" }])}>Add mileage rate</button></fieldset>{error && <p role="alert" className="form-error">{error}</p>}<button disabled={busy}>{busy ? "Saving…" : "Save mileage preferences"}</button></form>;
}
