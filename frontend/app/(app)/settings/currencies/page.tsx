"use client";
// Currencies and dated exchange rates. SGD is the base: a rate says one unit of the foreign currency
// is worth so many SGD on that date, and a document issued on that date defaults to it.
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import DateInput from "@/components/DateInput";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatDate } from "@/lib/dates";

type Rate = { rate_date: string; rate_micro: number };
type Currency = { code: string; name: string; is_base: boolean; active: boolean; latest_rate_micro: number; latest_rate_date: string; rates: Rate[] };
type Data = { base: string; currencies: Currency[] };

const CODE = /^[A-Za-z]{3}$/;
const RATE = /^\d+(\.\d{1,6})?$/;

function rateText(micro: number) {
  return micro > 0 ? (micro / 1_000_000).toFixed(6).replace(/0+$/, "").replace(/\.$/, "") : "—";
}

export default function Currencies() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = !!selected && ["owner", "admin", "accountant"].includes(selected.role);
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [rateCode, setRateCode] = useState("");
  const [rateDate, setRateDate] = useState("");
  const [rate, setRate] = useState("");
  const [renaming, setRenaming] = useState<Record<string, string>>({});

  async function load(id: number) {
    setData(await api<Data>(`/v1/currencies?org_id=${id}`));
  }
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<Data>(`/v1/currencies?org_id=${orgId}`)
      .then((d) => { if (live) setData(d); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);

  async function act(fn: () => Promise<void>) {
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try { await fn(); } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }

  const addCurrency = (e: FormEvent) => {
    e.preventDefault();
    if (orgId == null) return;
    const c = code.trim().toUpperCase();
    if (!CODE.test(c) || !name.trim()) { setError("Enter a three-letter currency code and a name."); return; }
    void act(async () => {
      await api("/v1/currencies", { method: "POST", body: JSON.stringify({ org_id: orgId, code: c, name: name.trim() }) });
      setCode(""); setName(""); setNotice(`${c} added. Add a rate so documents can use it.`);
      await load(orgId);
    });
  };

  const addRate = (e: FormEvent) => {
    e.preventDefault();
    if (orgId == null) return;
    if (!CODE.test(rateCode) || !RATE.test(rate.trim())) { setError("Choose a currency and type the rate in SGD, for example 1.35."); return; }
    const micro = Math.round(Number(rate) * 1_000_000);
    if (micro < 1000) { setError("The rate is too small."); return; }
    void act(async () => {
      await api("/v1/currencies/rates", { method: "POST", body: JSON.stringify({ org_id: orgId, code: rateCode, rate_date: rateDate, rate_micro: micro }) });
      setRate(""); setNotice("Rate saved. Documents dated on or after it use this rate.");
      await load(orgId);
    });
  };

  const rename = (code: string) => (e: FormEvent) => {
    e.preventDefault();
    if (orgId == null) return;
    const value = (renaming[code] ?? "").trim();
    if (!value) return;
    void act(async () => {
      await api(`/v1/currencies/${code}`, { method: "PUT", body: JSON.stringify({ org_id: orgId, code, name: value }) });
      setNotice("Currency renamed.");
      await load(orgId);
    });
  };

  const foreign = data?.currencies.filter((c) => !c.is_base) ?? [];
  return <>
    <PageHeader title="Currencies" description="SGD is the base currency. Foreign documents use the rate for their own date, and month-end revaluation keeps open balances current." />
    {!selected && <p className="notice">Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {canWrite && orgId != null && <>
      <form className="record-form" onSubmit={addCurrency}>
        <fieldset disabled={busy}><legend>Add currency</legend>
          <div className="form-grid">
            <div><label htmlFor="fx-code">Currency code</label><input id="fx-code" maxLength={3} required value={code} onChange={e => setCode(e.target.value.toUpperCase())} placeholder="USD" /></div>
            <div><label htmlFor="fx-name">Name</label><input id="fx-name" maxLength={100} required value={name} onChange={e => setName(e.target.value)} placeholder="US dollar" /></div>
          </div>
        </fieldset>
        <button type="submit" disabled={busy}>Add currency</button>
      </form>
      <form className="record-form" onSubmit={addRate}>
        <fieldset disabled={busy}><legend>Exchange rate</legend>
          <p className="section-help">One unit of the currency in SGD on that date. A document dated after this rate picks it up automatically; a later rate replaces it from its own date.</p>
          <div className="form-grid">
            <div><label htmlFor="rate-code">Currency</label>
              <select id="rate-code" required value={rateCode} onChange={e => setRateCode(e.target.value)}>
                <option value="">Choose a currency</option>
                {foreign.map(c => <option key={c.code} value={c.code}>{c.code} — {c.name}</option>)}
              </select></div>
            <div><label htmlFor="rate-date">Rate date</label><DateInput id="rate-date" required value={rateDate} onChange={e => setRateDate(e.target.value)} /></div>
            <div><label htmlFor="rate-value">Rate to SGD</label><input id="rate-value" inputMode="decimal" required value={rate} onChange={e => setRate(e.target.value)} placeholder="1.35" /></div>
          </div>
        </fieldset>
        <button type="submit" disabled={busy || foreign.length === 0}>Save rate</button>
        {foreign.length === 0 && <p className="section-help">Add a currency first.</p>}
      </form>
    </>}
    {data && <div className="table-scroll"><table className="doc-table"><caption>Currencies and rates</caption>
      <thead><tr><th scope="col">Code</th><th scope="col">Name</th><th scope="col">Latest rate</th><th scope="col">Rate history</th>{canWrite && <th scope="col">Rename</th>}</tr></thead>
      <tbody>{data.currencies.map(c => <tr key={c.code}>
        <th scope="row">{c.code}{c.is_base ? " (base)" : ""}</th>
        <td>{c.is_base ? "Singapore dollar" : c.name}</td>
        <td>{c.is_base ? "1" : c.latest_rate_date ? `${rateText(c.latest_rate_micro)} from ${formatDate(c.latest_rate_date)}` : "No rate yet"}</td>
        <td>{c.rates.length === 0 ? "—" : <ul className="task-list">{c.rates.map(r => <li key={r.rate_date}>{formatDate(r.rate_date)}: {rateText(r.rate_micro)}</li>)}</ul>}</td>
        {canWrite && <td>{!c.is_base && <form onSubmit={rename(c.code)}><label className="sr-only" htmlFor={`rn-${c.code}`}>New name for {c.code}</label><input id={`rn-${c.code}`} maxLength={100} value={renaming[c.code] ?? c.name} onChange={e => setRenaming({ ...renaming, [c.code]: e.target.value })} /> <button className="button-secondary" disabled={busy} type="submit">Save</button></form>}</td>}
      </tr>)}</tbody>
    </table></div>}
  </>;
}
