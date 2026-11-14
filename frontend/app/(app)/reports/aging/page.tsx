"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { party: string; document_type: string; document: string; invoice_date: string; due_date: string; balance_cents: number; bucket: string; bucket_amounts_cents: Record<string, number> };
type SummaryRow = { party: string; document_type?: string; balance_cents: number; bucket_amounts_cents: Record<string, number> };
type Report = { as_of: string; period_basis?: string; rows: Row[]; totals_by_bucket: Record<string, number>; percent_by_bucket?: Record<string, number>; bucket_labels?: string[]; source?: string; summary: SummaryRow[]; summary_by_document_type: SummaryRow[] };
function bucketNames(periodBasis: string, periods: number, length: number) { return periodBasis === "months" ? ["current", "1 month", "2 months", "3+ months"] : ["current", ...Array.from({ length: periods }, (_, i) => `${i * length + 1}-${(i + 1) * length}`), `${periods * length + 1}+`]; }

// Aging reads posted documents only. Balances subtract payments already recorded.
export default function AgingPage() {
  const { selected } = useOrg();
  return <AgingWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function AgingWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [kind, setKind] = useState<"receivables" | "payables">("receivables");
  const [contact, setContact] = useState("");
  const [asOf, setAsOf] = useState(() => new Date().toISOString().slice(0, 10));
  const [basis, setBasis] = useState<"due" | "invoice">("due");
  const [periodBasis, setPeriodBasis] = useState<"days" | "months">("days");
  const [view, setView] = useState<"detail" | "summary" | "summary_type">("detail");
  const [periods, setPeriods] = useState(4);
  const [periodLength, setPeriodLength] = useState(30);
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (orgId == null || busy) return;
    setBusy(true); setError(""); setReport(null);
    try {
      setReport(await api<Report>(`/v1/reports/${kind}-aging?org_id=${orgId}&as_of=${asOf}&basis=${basis}&period_basis=${periodBasis}&periods=${periods}&period_length=${periodLength}${contact.trim() ? `&contact=${encodeURIComponent(contact.trim())}` : ""}`));
    } catch (err) {
      const x = err as ApiError;
      setError(messageFor(x.error, x.status));
    } finally {
      setBusy(false);
    }
  }

  const labels = report ? (report.bucket_labels ?? bucketNames(report.period_basis ?? periodBasis, periods, periodLength)) : [];
  const summaryRows = report ? (view === "summary_type" ? report.summary_by_document_type : report.summary) : [];
  return (
    <>
      <PageHeader title="Aging report" description="Outstanding posted documents by age from invoice date or due date." />
      <PreviewNotice>Based only on posted invoices and bills. Customer and supplier opening balances, standalone credit notes and refunds are not included. Do not use this report to confirm the complete amount owed.</PreviewNotice>
      {!selected && <p>Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {selected && (
        <form className="record-form" onSubmit={run}>
          <fieldset disabled={busy}>
            <legend>Report</legend>
            <div className="form-grid">
              <div><label htmlFor="ag-kind">Type</label><select id="ag-kind" value={kind} onChange={(e) => { setKind(e.target.value as "receivables" | "payables"); setReport(null); }}><option value="receivables">Customers owe you (receivables)</option><option value="payables">You owe suppliers (payables)</option></select></div>
              <div><label htmlFor="ag-contact">Contact name contains</label><input id="ag-contact" value={contact} onChange={(e) => { setContact(e.target.value); setReport(null); }} placeholder="optional" /></div>
              <div><label htmlFor="ag-asof">As of</label><DateInput id="ag-asof" required value={asOf} onChange={(e) => { setAsOf(e.target.value); setReport(null); }}/></div>
              <div><label htmlFor="ag-basis">Age by</label><select id="ag-basis" value={basis} onChange={(e) => setBasis(e.target.value as "due" | "invoice")}><option value="due">Due date</option><option value="invoice">Invoice date</option></select></div>
              <div><label htmlFor="ag-period-basis">Periods</label><select id="ag-period-basis" value={periodBasis} onChange={(e) => { setPeriodBasis(e.target.value as "days" | "months"); setReport(null); }}><option value="days">Day counts</option><option value="months">Calendar months (1/2/3+)</option></select></div>
              <div><label htmlFor="ag-view">View</label><select id="ag-view" value={view} onChange={(e) => setView(e.target.value as "detail" | "summary" | "summary_type")}><option value="detail">Per-document detail</option><option value="summary">Summary by contact</option><option value="summary_type">Summary by document type</option></select></div>
              {periodBasis === "days" && <><div><label htmlFor="ag-periods">Ageing periods</label><input id="ag-periods" type="number" min={1} max={12} value={periods} onChange={(e) => setPeriods(Number(e.target.value))}/></div>
              <div><label htmlFor="ag-period-length">Days per period</label><input id="ag-period-length" type="number" min={1} max={365} value={periodLength} onChange={(e) => setPeriodLength(Number(e.target.value))}/></div></>}
            </div>
            <button disabled={busy}>{busy ? "Running…" : "Run report"}</button>
          </fieldset>
        </form>
      )}
      {report && (
        <section>
          <h2>{kind === "receivables" ? "Customers owe" : "Owed to suppliers"} as of {formatDate(report.as_of)}</h2>
          <div className="table-scroll"><table className="doc-table">
            <caption className="sr-only">Totals by age</caption>
            <thead><tr>{labels.map((b) => <th key={b} scope="col" className="num">{b === "current" ? (basis === "due" ? "Not yet due" : "Not past invoice date") : report.period_basis === "months" ? b : `${b} days`}</th>)}</tr></thead>
            <tbody><tr>{labels.map((b) => <td key={b} className="num">{formatCents(report.totals_by_bucket[b] ?? 0)}</td>)}</tr>
            <tr><th scope="row">Share of total</th>{labels.map((b) => <td key={b} className="num">{`${(report.percent_by_bucket?.[b] ?? 0).toFixed(2)}%`}</td>)}</tr></tbody>
          </table></div>
          {view === "detail" ? (report.rows.length === 0 ? <p className="muted">Nothing outstanding.</p> : (
            <div className="table-scroll"><table className="doc-table">
              <caption>Outstanding documents</caption>
              <thead><tr><th>{kind === "receivables" ? "Customer" : "Supplier"}</th><th>Document</th><th>Invoice date</th><th>Due date</th>{labels.map((b) => <th key={b} className="num">{b}</th>)}<th className="num">Balance</th></tr></thead>
              <tbody>{report.rows.map((r, i) => <tr key={i}><td>{r.party}</td><td>{r.document}</td><td>{formatDate(r.invoice_date)}</td><td>{formatDate(r.due_date)}</td>{labels.map((b) => <td key={b} className="num">{formatCents(r.bucket_amounts_cents[b] ?? 0)}</td>)}<td className="num">{formatCents(r.balance_cents)}</td></tr>)}</tbody>
            </table></div>
          )) : (summaryRows.length === 0 ? <p className="muted">Nothing outstanding.</p> : (
            <div className="table-scroll"><table className="doc-table">
              <caption>{view === "summary_type" ? "Outstanding by contact and document type" : "Outstanding by contact"}</caption>
              <thead><tr><th>{kind === "receivables" ? "Customer" : "Supplier"}</th>{view === "summary_type" && <th>Document type</th>}{labels.map((b) => <th key={b} className="num">{b}</th>)}<th className="num">Balance</th></tr></thead>
              <tbody>{summaryRows.map((r, i) => <tr key={i}><td>{r.party}</td>{view === "summary_type" && <td>{r.document_type}</td>}{labels.map((b) => <td key={b} className="num">{formatCents(r.bucket_amounts_cents[b] ?? 0)}</td>)}<td className="num">{formatCents(r.balance_cents)}</td></tr>)}</tbody>
            </table></div>
          ))}
        </section>
      )}
    </>
  );
}
