"use client";
// GST returns: the period list and the F5 drawn from posted journals. Filing records who filed it and
// freezes the figures; the typed worksheet stays available as a memo only.
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { formatDate } from "@/lib/dates";

type ReturnRow = { period_start: string; period_end: string; due_date: string; status: "draft" | "filed"; filed_at: string; filed_by: string };
type ReturnList = { year: number; tax_period: string; tax_basis: string; returns: ReturnRow[] };
type Box = { box: string; label: string; value_cents: number; yes_no?: boolean };
type F5 = { period_start: string; period_end: string; due_date: string; boxes: Box[]; net_payable_cents: number; warnings: string[]; filed: boolean; status: string; filed_by: string; filed_at: string };
const YEAR = new Date().getFullYear();

export default function GSTReturns() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = !!selected && ["owner", "admin", "accountant"].includes(selected.role);
  const [year, setYear] = useState(YEAR);
  const [list, setList] = useState<ReturnList | null>(null);
  const [f5, setF5] = useState<F5 | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<ReturnList>(`/v1/tax/gst-returns?org_id=${orgId}&year=${year}`)
      .then((r) => { if (live) setList(r); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, year]);

  async function act(fn: () => Promise<void>) {
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try { await fn(); } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }

  function open(row: ReturnRow) {
    if (orgId == null) return;
    void act(async () => {
      setF5(await api<F5>(`/v1/reports/gst-f5?org_id=${orgId}&from=${row.period_start}&to=${row.period_end}`));
    });
  }

  function file(row: ReturnRow) {
    if (orgId == null) return;
    if (!window.confirm(`File the GST return for ${formatDate(row.period_start)} to ${formatDate(row.period_end)}? The figures are frozen at filing.`)) return;
    void act(async () => {
      const filed = await api<F5>("/v1/tax/gst-returns/file", { method: "POST", body: JSON.stringify({ org_id: orgId, period_start: row.period_start, period_end: row.period_end }) });
      setF5(filed);
      setNotice("Return filed. The figures are now fixed.");
      setList(await api<ReturnList>(`/v1/tax/gst-returns?org_id=${orgId}&year=${year}`));
    });
  }

  return <>
    <PageHeader title="GST returns" description="Returns follow the company tax period. The F5 comes from posted journals; filing records who filed it and when and freezes the figures." />
    {!selected && <p className="notice">Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {selected && <p className="section-help">Tax basis: {list?.tax_basis === "payment" ? "payments (cash)" : "accrual (invoice date)"}. Filing frequency: {list?.tax_period?.replace("_", " ")}. <a href="/settings/tax">Change in tax settings</a>.</p>}
    <div className="record-toolbar" role="group" aria-label="Return filters">
      <div><label htmlFor="gst-year">Year</label><input id="gst-year" type="number" min={2000} max={2100} value={year} onChange={e => { setYear(Number(e.target.value)); setF5(null); }} /></div>
    </div>
    {list && (list.returns.length === 0 ? <p className="notice">No GST periods for this year.</p> : <div className="table-scroll"><table className="doc-table"><caption>{list.year} GST returns</caption>
      <thead><tr><th scope="col">Period</th><th scope="col">Due date</th><th scope="col">Status</th><th scope="col">Filed</th><th scope="col">Actions</th></tr></thead>
      <tbody>{list.returns.map(r => <tr key={r.period_start}>
        <th scope="row">{formatDate(r.period_start)} – {formatDate(r.period_end)}</th>
        <td>{formatDate(r.due_date)}</td>
        <td><span className="badge">{r.status === "filed" ? "Filed" : "Draft"}</span></td>
        <td>{r.status === "filed" ? `${r.filed_by} on ${formatDate(r.filed_at.slice(0, 10))}` : ""}</td>
        <td><button className="button-secondary" disabled={busy} onClick={() => open(r)}>View F5</button>{" "}
          {r.status === "draft" && canWrite && <button disabled={busy} onClick={() => file(r)}>File return</button>}</td>
      </tr>)}</tbody></table></div>)}

    {f5 && <section className="worksheet" aria-labelledby="f5-title">
      <h2 id="f5-title">F5 return {formatDate(f5.period_start)} – {formatDate(f5.period_end)}</h2>
      <p>{f5.filed ? <>Filed by {f5.filed_by} on {formatDate(f5.filed_at.slice(0, 10))}. These figures are frozen.</> : "Draft, computed from posted journals. Filing freezes these figures."}</p>
      <table className="doc-table"><caption className="sr-only">F5 boxes</caption>
        <thead><tr><th scope="col">Box</th><th scope="col">Label</th><th scope="col" className="num">Value</th></tr></thead>
        <tbody>{f5.boxes.map(b => <tr key={b.box}><td>{b.box}</td><td>{b.label}</td><td className="num">{b.yes_no ? (b.value_cents ? "Yes" : "No") : formatCents(b.value_cents)}</td></tr>)}</tbody>
      </table>
      <p>Net GST for the period: <strong>{formatCents(f5.net_payable_cents)}</strong>. Due date: {formatDate(f5.due_date)}.</p>
      {f5.warnings.length > 0 && <ul className="warnings">{f5.warnings.map(w => <li key={w}>{w}</li>)}</ul>}
    </section>}
    <p className="notice">The old typing worksheet is kept as a memo: <a href="/tax/gst/worksheet">GST worksheet</a>. It is not a filed return.</p>
  </>;
}
