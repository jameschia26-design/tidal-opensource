"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import PageHeader from "@/components/PageHeader";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";
import DateInput from "@/components/DateInput";
import Chart from "@/components/charts/Chart";

type Overview = { draft_invoices: number; awaiting_approval: number; awaiting_payment: number; overdue: number; outstanding_cents: number; draft_quotes: number; sent_quotes: number; accepted_quotes: number; expired_quotes: number };
export default function SalesOverview() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} orgId={selected?.id} />;
}
function Workspace({ orgId }: { orgId: number | undefined }) {
  const [result, setResult] = useState<Overview | null>(null);
  const [error, setError] = useState("");
  const [series, setSeries] = useState<{ month: string; revenue_cents: number; cost_of_sales_cents: number; expense_cents: number; gross_profit_cents: number }[]>([]);
  const [date, setDate] = useState(() => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`; });
  useEffect(() => {
    if (!orgId || !date) return;
    let active = true;
    api<Overview>(`/v1/sales-overview?org_id=${orgId}&as_of=${date}`).then(value => { if (active) setResult(value); }).catch((e: ApiError) => { if (active) setError(messageFor(e.error,e.status)); });
    return () => { active = false; };
  }, [orgId,date]);
  useEffect(() => {
    if (!orgId) return;
    let active = true;
    const year = new Date().getFullYear();
    api<{ series: typeof series }>(`/v1/reports/analytics?org_id=${orgId}&from=${year}-01-01&to=${year}-12-31`)
      .then(r => { if (active) setSeries(r.series); })
      .catch(() => { if (active) setSeries([]); });
    return () => { active = false; };
  }, [orgId]);
  return <><PageHeader title="Sales overview" />
    {!orgId ? <p>Choose a company first.</p> : <>
      <label htmlFor="overview-date">As at</label> <DateInput id="overview-date" value={date} onChange={e => { setResult(null); setError(""); setDate(e.target.value); }}/>
      {error && <p role="alert">{error}</p>}
      {!result && date && !error && <p>Loading…</p>}
      {result && <><h2>Invoices &amp; payments</h2><p><Link href="/sales/invoices">Draft: {result.draft_invoices} · Awaiting approval: {result.awaiting_approval} · Awaiting payment: {result.awaiting_payment} · Overdue: {result.overdue}</Link></p><p>Outstanding invoice balance: {formatCents(result.outstanding_cents)}</p>
        <h2>Quotes</h2><p><Link href="/sales/quotes">Draft: {result.draft_quotes} · Sent: {result.sent_quotes} · Accepted: {result.accepted_quotes} · Expired: {result.expired_quotes}</Link></p></>}
      <h2>Revenue, cost of sales, expenses and gross profit</h2>
      <Chart series={series.map(m => ({ label: m.month, revenue: m.revenue_cents, cost: m.cost_of_sales_cents, expense: m.expense_cents - m.cost_of_sales_cents, gross: m.gross_profit_cents }))} title="Monthly sales performance" description="This calendar year to date, from posted entries only." empty="No posted sales or spending this year yet." />
      <h2>Quick links</h2><p><Link href="/sales/invoices/new">Create invoice</Link> · <Link href="/sales/recurring">Repeating invoices</Link> · <Link href="/reports/aging">Receivables aging</Link></p>
      <p>Document statuses are current, not historical. Invoice balances exclude opening balances and unapplied credits. Online payments and customer ranking are not available here.</p>
    </>}
  </>;
}
