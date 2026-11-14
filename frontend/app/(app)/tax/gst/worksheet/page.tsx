"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Box = { box: string; label: string; value_cents: number; yes_no?: boolean };
type F5 = { worksheet_only: true; rate_bp: number; boxes: Box[]; net_gst_cents: number; due_date: string; warnings: string[] };
type Row = { kind: "sale" | "purchase"; code: string; net: string; gst: string; description: string };
const DATE = /^\d{4}-\d{2}-\d{2}$/;
const blank = (): Row => ({ kind: "sale", code: "", net: "", gst: "", description: "" });

export default function GstPage() {
  const orgId = useOrgId();
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  const [rows, setRows] = useState<Row[]>([blank()]);
  const [result, setResult] = useState<F5 | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (i: number, k: keyof Row, v: string) => setRows(rows.map((r, idx) => (idx === i ? { ...r, [k]: v } : r)));

  async function run(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setResult(null);
    if (!DATE.test(start) || !DATE.test(end)) { setError("Enter the period start and end dates."); return; }
    const parsed = rows.map((r) => ({ kind: r.kind, code: r.code.trim(), net_cents: parseCents(r.net), gst_cents: parseCents(r.gst), description: r.description.trim() || undefined }));
    if (parsed.some((p) => p.code === "" || p.net_cents === null || p.gst_cents === null)) {
      setError("Each line needs a code and net and GST amounts (SGD).");
      return;
    }
    setBusy(true);
    try {
      const revenue = parsed.filter((p) => p.kind === "sale").reduce((s, p) => s + (p.net_cents ?? 0), 0);
      const out = await api<F5>("/v1/tax/gst-f5", {
        method: "POST",
        body: JSON.stringify({ org_id: orgId ?? undefined, period_start: start, period_end: end, revenue_cents: revenue, lines: parsed }),
      });
      setResult(out);
    } catch (err) {
      const e2 = err as ApiError;
      setError(messageFor(e2.error, e2.status));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <p className="notice">
        SGD only. InvoiceNow is not supported (see the{" "}
        <a href="https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/gst-invoicing-requirements/invoicenow" target="_blank" rel="noreferrer">IRAS InvoiceNow requirements</a>).
        This worksheet is a draft, not a filed return.
      </p>
      <PageHeader title="GST worksheet (memo)" description="Adds up typed lines into the F5 boxes so you can check a figure by hand. Use GST returns for the ledger figures you file." />
      <PreviewNotice>
        This is a memo worksheet, not a filed return. The ledger figures live in <a href="/tax/gst">GST returns</a>; Tidal does not submit to IRAS.
      </PreviewNotice>
      {orgId == null && <p className="muted">Choose a company first.</p>}
      <form onSubmit={run} noValidate>
        <div className="inline-form">
          <div><label htmlFor="gst-start">Period start</label><DateInput id="gst-start" value={start} onChange={(e) => setStart(e.target.value)}/></div>
          <div><label htmlFor="gst-end">Period end</label><DateInput id="gst-end" value={end} onChange={(e) => setEnd(e.target.value)}/></div>
        </div>
        <fieldset className="line-set">
          <legend>Lines</legend>
          {rows.map((r, i) => (
            <div className="line-row gst-row" key={i}>
              <div><label htmlFor={`gk${i}`}>Type</label>
                <select id={`gk${i}`} value={r.kind} onChange={(e) => set(i, "kind", e.target.value)}>
                  <option value="sale">Sale</option><option value="purchase">Purchase</option>
                </select></div>
              <div><label htmlFor={`gc${i}`}>Tax code</label><input id={`gc${i}`} value={r.code} onChange={(e) => set(i, "code", e.target.value)} placeholder="e.g. SR" /></div>
              <div><label htmlFor={`gn${i}`}>Net (SGD)</label><input id={`gn${i}`} inputMode="decimal" value={r.net} onChange={(e) => set(i, "net", e.target.value)} /></div>
              <div><label htmlFor={`gg${i}`}>GST (SGD)</label><input id={`gg${i}`} inputMode="decimal" value={r.gst} onChange={(e) => set(i, "gst", e.target.value)} /></div>
              {rows.length > 1 && <button type="button" className="button-secondary" onClick={() => setRows(rows.filter((_, idx) => idx !== i))}>Remove line {i + 1}</button>}
            </div>
          ))}
          <button type="button" className="button-secondary" onClick={() => setRows([...rows, blank()])}>Add line</button>
        </fieldset>
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy || orgId == null}>{busy ? "Working…" : "Calculate worksheet"}</button>
      </form>

      {result && (
        <section className="worksheet" aria-labelledby="f5-title">
          <h2 id="f5-title">F5 boxes (worksheet only)</h2>
          <table className="doc-table">
            <caption className="sr-only">F5 boxes</caption>
            <thead><tr><th scope="col">Box</th><th scope="col">Label</th><th scope="col" className="num">Value</th></tr></thead>
            <tbody>
              {result.boxes.map((b) => (
                <tr key={b.box}><td>{b.box}</td><td>{b.label}</td><td className="num">{b.yes_no ? (b.value_cents ? "Yes" : "No") : formatCents(b.value_cents)}</td></tr>
              ))}
            </tbody>
          </table>
          <p>Net GST for the period: <strong>{formatCents(result.net_gst_cents)}</strong>. Due date: {formatDate(result.due_date)}.</p>
          {result.warnings.length > 0 && <ul className="warnings">{result.warnings.map((w) => <li key={w}>{w}</li>)}</ul>}
        </section>
      )}
    </>
  );
}
