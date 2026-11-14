"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { csvCell, downloadCsv } from "@/lib/csv";
import { messageFor } from "@/lib/errors";
import { formatDate } from "@/lib/dates";

type Invoice = { id: number; number: number | null; document_number: string; lifecycle_status: string; contact_id: number; contact_name: string; reference: string; status: string; invoice_date: string; due_date: string; total_cents: number; paid_cents: number; balance_cents: number; sent_date: string };
type Counts = { all: number; draft: number; awaiting_approval: number; awaiting_payment: number; paid: number; voided: number };
const TABS: { id: keyof Counts; label: string }[] = [
  { id: "all", label: "All" },
  { id: "draft", label: "Draft" },
  { id: "awaiting_approval", label: "Awaiting approval" },
  { id: "awaiting_payment", label: "Awaiting payment" },
  { id: "paid", label: "Paid" },
  { id: "voided", label: "Voided" },
];

export default function InvoicesPage() {
  const { selected } = useOrg();
  if (!selected) return <><PageHeader title="Invoices" description="Numbers are set on the draft and stay fixed when posted. Paid is worked out from payments and credits." /><p className="notice">Choose a company to see invoices.</p></>;
  return <InvoicesWorkspace key={`${selected.id}-${selected.role}`} orgId={selected.id} canWrite={selected.role !== "viewer"} />;
}

function InvoicesWorkspace({ orgId, canWrite }: { orgId: number; canWrite: boolean }) {
  const [status, setStatus] = useState<keyof Counts>("all");
  const [tick, setTick] = useState(0);
  const [page, setPage] = useState(0);
  const [query, setQuery] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [within, setWithin] = useState("all");
  const [rows, setRows] = useState<Invoice[] | null>(null);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 50;

  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    setRows(null);
    setError(null);
    const q = new URLSearchParams({ org_id: String(orgId), limit: String(pageSize), offset: String(page * pageSize) });
    if (status && status !== "all") q.set("status", status);
    if (query.trim()) q.set("q", query.trim());
    if (from) q.set("from", from);
    if (to) q.set("to", to);
    if (within !== "all") q.set("within", within);
    api<{ invoices: Invoice[]; has_more: boolean; counts: Counts }>(`/v1/invoices?${q}`)
      .then((r) => { if (live) { setRows(r.invoices); setHasMore(r.has_more); setCounts(r.counts); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, status, tick, page, query, from, to, within]);

  return (
    <>
      <PageHeader
        title="Invoices"
        description="Numbers are set on the draft and stay fixed when posted. Paid is worked out from payments and credits."
        actions={<>{canWrite && <Link className="button-link" href="/sales/invoices/new">New invoice</Link>}<Link className="button-secondary" href="/import">Import</Link>{rows && rows.length > 0 && <button type="button" className="button-secondary" onClick={() => downloadCsv("invoices.csv", [["Number", "Reference", "To", "Status", "Invoice date", "Due date", "Paid", "Due"], ...rows.map(i => [i.document_number || String(i.number ?? "Draft"), i.reference, i.contact_name, i.lifecycle_status || i.status, i.invoice_date, i.due_date, (i.paid_cents / 100).toFixed(2), (i.balance_cents / 100).toFixed(2)])].map(r => r.map(csvCell).join(",")).join("\n") + "\n")}>Export</button>}</>}
      />
        <PreviewNotice>Payments are recorded against posted invoices. GST invoices post GST Output.</PreviewNotice>
      <div className="record-toolbar" role="search" aria-label="Find invoices">
        <div><label htmlFor="invoice-search">Search</label><input id="invoice-search" type="search" value={query} onChange={e => { setQuery(e.target.value); setPage(0); }} placeholder="Number, reference, contact or amount" /></div>
        <div><label htmlFor="invoice-from">From</label><input id="invoice-from" type="date" value={from} onChange={e => { setFrom(e.target.value); setPage(0); }} /></div>
        <div><label htmlFor="invoice-to">To</label><input id="invoice-to" type="date" value={to} onChange={e => { setTo(e.target.value); setPage(0); }} /></div>
        <label>Search within<select id="invoice-within" value={within} onChange={e => { setWithin(e.target.value); setPage(0); }}><option value="all">All invoices</option><option value="unsent">Not sent</option><option value="voided">Voided</option></select></label>
      </div>
      <nav className="tabs" aria-label="Invoice status">
        <ul>{TABS.map(tab => <li key={tab.id}><button type="button" className="button-secondary" aria-pressed={status === tab.id} onClick={() => { setStatus(tab.id); setPage(0); }}>{tab.label}{counts ? ` (${counts[tab.id]})` : ""}</button></li>)}</ul>
      </nav>
      {error && <p role="alert" className="form-error">{error}</p>}
      {orgId == null && !error && <p className="muted">Choose a company to see invoices.</p>}
      {rows === null && !error && orgId != null && <p className="muted">Loading…</p>}
      {rows && rows.length === 0 && <div className="empty-state" role="status"><p>No invoices yet.</p>{canWrite && <p><Link href="/sales/invoices/new">Create the first invoice</Link></p>}</div>}
      {rows && rows.length > 0 && (
        <div className="table-scroll"><table className="doc-table">
          <caption className="sr-only">Invoices</caption>
          <thead><tr><th scope="col">Number</th><th scope="col">Ref</th><th scope="col">To</th><th scope="col">Status</th><th scope="col">Invoice date</th><th scope="col">Due date</th><th scope="col" className="num">Paid</th><th scope="col" className="num">Due</th><th scope="col">Sent</th></tr></thead>
          <tbody>
            {rows.map((i) => (
              <tr key={i.id}>
                <td><Link href={`/sales/invoices/${i.id}`}>{i.document_number || i.number || "Draft"}</Link></td>
                <td>{i.reference || "—"}</td>
                <td>{i.contact_name || "—"}</td>
                <td>{i.lifecycle_status || i.status}</td>
                <td>{formatDate(i.invoice_date)}</td>
                <td>{formatDate(i.due_date)}</td>
                <td className="num">{formatCents(i.paid_cents)}</td>
                <td className="num">{formatCents(i.balance_cents)}</td>
                <td>{i.sent_date ? formatDate(i.sent_date) : "Not sent"}</td>
              </tr>
            ))}
          </tbody>
        </table></div>
      )}
      {rows && <p className="section-help" role="status">Showing {rows.length} invoice(s) on this page{counts ? `, ${counts.all} in total` : ""}.</p>}
      {rows && rows.length > 0 && (
        <div className="filter-row pager" role="group" aria-label="Invoice pages">
          <button type="button" className="button-secondary" disabled={page === 0} onClick={() => setPage((p) => Math.max(0, p - 1))}>Previous</button>
          <span className="muted">Page {page + 1}{hasMore ? "" : " of the last page"}</span>
          <button type="button" className="button-secondary" disabled={!hasMore} onClick={() => setPage((p) => p + 1)}>Next</button>
        </div>
      )}
    </>
  );
}
