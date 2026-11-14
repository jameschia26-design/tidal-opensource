"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import PageHeader from "@/components/PageHeader";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";
import DateInput from "@/components/DateInput";

type Overview = { draft_bills: number; awaiting_approval: number; awaiting_payment: number; overdue: number; outstanding_cents: number; draft_orders: number; issued_orders: number; received_orders: number };
export default function PurchasesOverview() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} orgId={selected?.id} />;
}
function Workspace({ orgId }: { orgId: number | undefined }) {
  const [result, setResult] = useState<Overview | null>(null);
  const [error, setError] = useState("");
  const [date, setDate] = useState(() => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`; });
  useEffect(() => {
    if (!orgId || !date) return;
    let active = true;
    api<Overview>(`/v1/purchases-overview?org_id=${orgId}&as_of=${date}`).then(value => { if (active) setResult(value); }).catch((e: ApiError) => { if (active) setError(messageFor(e.error,e.status)); });
    return () => { active = false; };
  }, [orgId,date]);
  return <><PageHeader title="Purchases overview" />
    {!orgId ? <p>Choose a company first.</p> : <>
      <label htmlFor="purchases-date">As at</label> <DateInput id="purchases-date" value={date} onChange={e => { setResult(null); setError(""); setDate(e.target.value); }}/>
      {error && <p role="alert">{error}</p>}
      {!result && date && !error && <p>Loading…</p>}
      {result && <><h2>Bills</h2><p><Link href="/purchases/bills">Draft: {result.draft_bills} · Awaiting approval: {result.awaiting_approval} · Awaiting payment: {result.awaiting_payment} · Overdue: {result.overdue}</Link></p><p>Outstanding bill balance: {formatCents(result.outstanding_cents)}</p>
        <h2>Purchase orders</h2><p><Link href="/purchases/orders">Draft: {result.draft_orders} · Issued: {result.issued_orders} · Received: {result.received_orders}</Link></p></>}
      <h2>Quick links</h2><p><Link href="/purchases/bills">Bills</Link> · <Link href="/purchases/orders">Purchase orders</Link> · <Link href="/reports/aging">Payables aging</Link></p>
      <p>Document statuses are current, not historical. Bill balances exclude opening balances. Upcoming bills chart and document search are not available here.</p>
    </>}
  </>;
}
