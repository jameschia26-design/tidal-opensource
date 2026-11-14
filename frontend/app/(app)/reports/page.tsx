"use client";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrgId } from "@/components/useOrgId";
import { api } from "@/lib/api";

type Report = { title: string; href: string };
type SavedReport = { id: number; title: string; report_key: string; status: "draft" | "published" | "archived"; favourite: boolean };
const reports: Report[] = [
  { title: "Sales by customer", href: "/reports/sales-by-customer" },
  { title: "Backorder History", href: "/reports/backorders" },
  { title: "Open Backorders", href: "/reports/backorders" },
  { title: "Blank Report", href: "/reports/blank" },
  { title: "Account Summary", href: "/reports/ledger" },
  { title: "Account Transactions", href: "/reports/ledger" },
  { title: "Aged Payables Detail", href: "/reports/aging" },
  { title: "Aged Payables Summary", href: "/reports/aging" },
  { title: "Aged Receivables Detail", href: "/reports/aging" },
  { title: "Aged Receivables Summary", href: "/reports/aging" },
  { title: "Billable Expenses - Outstanding", href: "/reports/billable-expenses" },
  { title: "Cash Validation Customer Report", href: "/reports/cash-validation" },
  { title: "Contact Transactions - Summary", href: "/reports/contact-transactions" },
  { title: "Customer Invoice Activity", href: "/reports/activity" },
  { title: "GST Audit (Beta)", href: "/reports/gst-audit" },
  { title: "General Ledger Detail", href: "/reports/ledger" },
  { title: "General Ledger Summary", href: "/reports/ledger" },
  { title: "Inventory Item Details", href: "/reports/inventory" },
  { title: "Inventory Item List", href: "/reports/inventory" },
  { title: "Inventory Item Summary", href: "/reports/inventory" },
  { title: "Payable Invoice Detail", href: "/reports/document-lines" },
  { title: "Payable Invoice Summary", href: "/reports/documents" },
  { title: "Project Details", href: "/reports/projects" },
  { title: "Project Summary", href: "/reports/projects" },
  { title: "Receivable Invoice Detail", href: "/reports/document-lines" },
  { title: "Receivable Invoice Summary", href: "/reports/documents" },
  { title: "Statement of Cash Flows - Direct Method", href: "/reports/cash-flows-direct" },
  { title: "Supplier Bill Activity", href: "/reports/activity" },
  { title: "Trial Balance by Date Range", href: "/reports/ledger" },
  { title: "Depreciation Schedule", href: "/reports/fixed-assets" },
  { title: "Disposal Schedule", href: "/reports/disposal-schedule" },
  { title: "Fixed Asset Reconciliation", href: "/reports/fixed-asset-reconciliation" },
  { title: "General Ledger Exceptions", href: "/reports/gl-exceptions" },
  { title: "Income and Expenses by Contact", href: "/reports/contact-activity" },
  { title: "Movements in Equity", href: "/reports/movements-in-equity" },
  { title: "Profit and Loss", href: "/reports/profit-loss" },
  { title: "Project Financials", href: "/reports/project-financials" },
  { title: "Reconciliation Reports", href: "/reports/reconciliation" },
  { title: "Sales Tax Report", href: "/reports/sales-tax" },
  { title: "Tracking Summary", href: "/reports/tracking-summary" },
  { title: "Trial Balance", href: "/reports/trial-balance" },
  { title: "Duplicate statement lines", href: "/reports/duplicate-statement-lines" },
  { title: "Executive summary", href: "/reports/executive-summary" },
  { title: "Budget summary", href: "/reports/budget-summary" },
  { title: "Budget variance", href: "/reports/budget-variance" },
  { title: "Journal report", href: "/reports/journals" },
  { title: "Cash summary", href: "/reports/cash-summary" },
  { title: "Bank summary", href: "/reports/bank-summary" },
  { title: "Bank reconciliation", href: "/reports/bank-reconciliation" },
  { title: "Management report", href: "/reports/management" },
  { title: "Cash flows (direct)", href: "/reports/cash-flows-direct" },
  { title: "Expense claim detail", href: "/reports/expense-claim-detail" },
  { title: "Sales by item", href: "/reports/sales-by-item" },
  { title: "Tax reconciliation", href: "/reports/tax-reconciliation" },
  { title: "Foreign Currency Gains and Losses", href: "/reports/fx-gains-losses" },
  { title: "General ledger exceptions", href: "/reports/gl-exceptions" },
  { title: "Fixed asset reconciliation", href: "/reports/fixed-asset-reconciliation" },
  { title: "GST audit", href: "/reports/gst-audit" },
  { title: "Sales tax summary", href: "/reports/sales-tax" },
  { title: "Customer and supplier activity", href: "/reports/activity" },
  { title: "Billable expenses — outstanding", href: "/reports/billable-expenses" },
  { title: "Statement of cash flows (direct)", href: "/reports/cash-flows-direct" },
  { title: "Project time", href: "/reports/projects" },
  { title: "Detailed time", href: "/reports/detailed-time" },
  { title: "Depreciation schedule", href: "/reports/fixed-assets" },
  { title: "Ledger reports", href: "/reports/ledger" },
  { title: "Trial balance", href: "/reports/trial-balance" },
  { title: "Profit and loss", href: "/reports/profit-loss" },
  { title: "Balance sheet", href: "/reports/balance-sheet" },
  { title: "Aging report", href: "/reports/aging" },
];
const routeForKey = new Map(reports.map(r => [r.href.split("/").at(-1) ?? "", r.href]));

export default function Reports() {
  const orgId = useOrgId();
  const [query, setQuery] = useState("");
  const [saved, setSaved] = useState<SavedReport[]>([]);
  useEffect(() => {
    if (orgId == null) { setSaved([]); return; }
    let live = true;
    api<{ saved_reports: SavedReport[] }>(`/v1/saved-reports?org_id=${orgId}`)
      .then(r => { if (live) setSaved(r.saved_reports); })
      .catch(() => { if (live) setSaved([]); });
    return () => { live = false; };
  }, [orgId]);
  const filtered = useMemo(() => {
    const term = query.trim().toLocaleLowerCase();
    return term ? reports.filter(r => r.title.toLocaleLowerCase().includes(term)) : reports;
  }, [query]);
  const favourites = saved.filter(r => r.favourite && r.status !== "archived");
  return <>
    <PageHeader title="Reports" description="Figures come from posted entries only." actions={<Link className="button-secondary" href="/reports/aging">Aging report</Link>} />
    <section aria-label="Favourite reports"><h2>Favourites</h2>{favourites.length ? <ul className="task-list">{favourites.map(r => <li key={r.id}><Link href={routeForKey.get(r.report_key) ?? `/reports/${encodeURIComponent(r.report_key)}`}>{r.title}</Link>{r.status === "draft" && <span className="badge">Draft</span>}</li>)}</ul> : <p className="notice">No favourite reports yet. Add one from a report toolbar.</p>}</section>
    <section aria-label="Find a report"><h2>Find a report</h2><label htmlFor="report-search">Search reports</label><input id="report-search" type="search" value={query} onChange={e => setQuery(e.target.value)} placeholder="Search by report name" />
      {filtered.length ? <ul className="task-list">{filtered.map(r => <li key={r.href}><Link href={r.href}>{r.title}</Link></li>)}</ul> : <p role="status">No reports match “{query}”.</p>}
    </section>
  </>;
}
