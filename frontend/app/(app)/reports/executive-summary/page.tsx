"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type P = { from: string; to: string; cash_received_cents: number; cash_spent_cents: number; cash_surplus_cents: number; closing_bank_cents: number; income_cents: number; expenses_cents: number; profit_cents: number; receivables_cents: number; payables_cents: number; sales_cents: number; other_income_cents: number; cost_of_sales_cents: number; other_expense_cents: number; gross_profit_cents: number; gross_margin_bp: number; net_margin_bp: number; debtor_days: number; creditor_days: number; bank_cents: number; current_assets_cents: number; fixed_assets_cents: number; current_liabilities_cents: number; equity_cents: number; assets_cents: number };
type Data = { current: P; previous: P };
type Row = { label: string; key: keyof P; money?: boolean };
const sections: { title: string; rows: Row[] }[] = [
 { title: "Cash", rows: [{ label: "Cash received", key: "cash_received_cents" }, { label: "Cash spent", key: "cash_spent_cents" }, { label: "Cash surplus (deficit)", key: "cash_surplus_cents" }, { label: "Closing bank balance", key: "closing_bank_cents" }] },
 { title: "Profit and loss", rows: [{ label: "Sales", key: "sales_cents" }, { label: "Cost of sales", key: "cost_of_sales_cents" }, { label: "Gross profit", key: "gross_profit_cents" }, { label: "Other income", key: "other_income_cents" }, { label: "Operating expenses", key: "expenses_cents" }, { label: "Other expenses", key: "other_expense_cents" }, { label: "Income", key: "income_cents" }, { label: "Profit (loss)", key: "profit_cents" }] },
 { title: "Ratios and days", rows: [{ label: "Gross margin", key: "gross_margin_bp", money: false }, { label: "Net margin", key: "net_margin_bp", money: false }, { label: "Debtor days", key: "debtor_days", money: false }, { label: "Creditor days", key: "creditor_days", money: false }] },
 { title: "Balance sheet", rows: [{ label: "Bank", key: "bank_cents" }, { label: "Current assets (other)", key: "current_assets_cents" }, { label: "Fixed assets", key: "fixed_assets_cents" }, { label: "Total assets", key: "assets_cents" }, { label: "Current liabilities", key: "current_liabilities_cents" }, { label: "Equity", key: "equity_cents" }, { label: "Debtors (receivables)", key: "receivables_cents" }, { label: "Creditors (payables)", key: "payables_cents" }] },
];
export default function ExecutiveSummary() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");
 const [compare, setCompare] = useState(true);
 const [shown, setShown] = useState<string[]>(sections.map(s => s.title));
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Data>(`/v1/reports/executive-summary?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 const render = (v: number, money = true) => money ? formatCents(v) : `${(v / 100).toFixed(2)}${v === 0 ? "" : ""}`;
 function exportCSV() {
  if (!data) return;
  const rows = sections.filter(s => shown.includes(s.title)).flatMap(s => s.rows.map(r => [s.title, r.label, String(data.current[r.key]), String(data.previous[r.key]), String((data.current[r.key] as number) - (data.previous[r.key] as number))]));
  const csv = [["Section", "Item", data.current.from + " to " + data.current.to, data.previous.from + " to " + data.previous.to, "Variance"], ...rows].map(r => r.map(v => `"${v}"`).join(",")).join("\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = `executive-summary-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
 }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="es-from">From</label><DateInput id="es-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><label className="check-label"><input type="checkbox" checked={compare} onChange={e => setCompare(e.target.checked)} /> Compare previous period</label><div><label htmlFor="es-to">To</label><DateInput id="es-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><details><summary>Columns</summary>{sections.map(s => <label key={s.title} className="check-label"><input type="checkbox" checked={shown.includes(s.title)} onChange={e => setShown(e.target.checked ? [...shown, s.title] : shown.filter(v => v !== s.title))} /> {s.title}</label>)}</details><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Executive summary" description="The period against the equal-length period before it, up to 366 days." filters={filters}>
  <p>Tick &quot;Compare previous period&quot; to show the equal-length period before it beside the current one. Cash uses the standard Bank account (1000). Costs, other income, margins, debtor and creditor days and the asset and liability sections come from account classes, so they follow the chart of accounts.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Executive summary</caption>
   <thead><tr><th scope="col">Item</th><th scope="col">{data.current.from} to {data.current.to}</th>{compare && <><th scope="col">{data.previous.from} to {data.previous.to}</th><th scope="col">Variance</th></>}</tr></thead>
   {sections.filter(s => shown.includes(s.title)).map(s => <tbody key={s.title}><tr><th colSpan={4} scope="colgroup">{s.title}</th></tr>{s.rows.map(r => <tr key={r.key}><th scope="row">{r.label}</th><td>{render(data.current[r.key] as number, r.money !== false)}</td>{compare && <><td>{render(data.previous[r.key] as number, r.money !== false)}</td><td>{render((data.current[r.key] as number) - (data.previous[r.key] as number), r.money !== false)}</td></>}</tr>)}</tbody>)}
  </table></div>}
 </ReportFrame>;
}
