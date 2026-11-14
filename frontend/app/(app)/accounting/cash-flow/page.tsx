"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import PageHeader from "@/components/PageHeader";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { party: string; document: string; due_date: string; balance_cents: number };
type Aging = { rows: Row[] };
export default function CashFlow() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} orgId={selected?.id} />;
}
function Workspace({ orgId }: { orgId: number | undefined }) {
  const [date, setDate] = useState(() => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`; });
  const [data, setData] = useState<{ receivables: Row[]; payables: Row[] } | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!orgId || !/^\d{4}-\d{2}-\d{2}$/.test(date)) return;
    let active = true;
    Promise.all([api<Aging>(`/v1/reports/receivables-aging?org_id=${orgId}&as_of=${date}`), api<Aging>(`/v1/reports/payables-aging?org_id=${orgId}&as_of=${date}`)]).then(([r,p]) => { if (active) setData({ receivables:r.rows, payables:p.rows }); }).catch((e: ApiError) => { if (active) setError(messageFor(e.error,e.status)); });
    return () => { active = false; };
  }, [orgId,date]);
  const total = (rows: Row[]) => rows.reduce((sum, row) => sum + row.balance_cents, 0);
  return <><PageHeader title="Short-term cash flow" description="Open posted invoices and bills by due date." />
    {!orgId ? <p>Choose a company first.</p> : <>
      <label htmlFor="cash-flow-date">As at</label> <DateInput id="cash-flow-date" value={date} onChange={e => { setData(null); setError(""); setDate(e.target.value); }}/>
      {error && <p role="alert">{error}</p>}{!data && date && !error && <p>Loading…</p>}
      {data && <><h2>Invoices owed: {formatCents(total(data.receivables))}</h2><CashRows rows={data.receivables} /><h2>Bills to pay: {formatCents(total(data.payables))}</h2><CashRows rows={data.payables} /></>}
      <p>This is not a cash projection: bank balances, opening balances, unapplied credits and future payments are excluded. Documents use current posted status, not historical status. No forecast or editable assumptions are available.</p>
      <p><Link href="/reports/aging">Open aging report</Link></p>
    </>}
  </>;
}
function CashRows({ rows }: { rows: Row[] }) {
  if (!rows.length) return <p>None.</p>;
  return <div className="table-scroll"><table className="doc-table"><thead><tr><th>Document</th><th>Contact</th><th>Due</th><th>Balance</th></tr></thead><tbody>{rows.map((row, i) => <tr key={`${row.document}-${i}`}><td>{row.document}</td><td>{row.party}</td><td>{formatDate(row.due_date)}</td><td>{formatCents(row.balance_cents)}</td></tr>)}</tbody></table></div>;
}
