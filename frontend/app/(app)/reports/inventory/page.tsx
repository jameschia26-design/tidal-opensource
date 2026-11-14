"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { item_id: number; sku: string; name: string; unit: string; item_type: string; unit_cost_cents: number; sale_price_cents: number; purchase_description: string; qty: number; value_cents: number };
type SummaryRow = Row & { opening_qty: number; opening_value_cents: number; in_qty: number; in_value_cents: number; out_qty: number; out_value_cents: number; closing_qty: number; closing_value_cents: number; purchase_qty: number; purchase_value_cents: number; sales_qty: number; cogs_value_cents: number; adjustment_value_cents: number };
type Detail = { item_id: number; sku: string; name: string; movement_date: string; source_kind: string; reference: string; description: string; contact: string; qty_delta: number; unit_cost_cents: number; value_cents: number; running_qty: number };
const sourceLabel = (kind: string) => kind === "invoice" ? "Sale" : kind === "bill" ? "Purchase" : kind === "stocktake" ? "Stocktake" : "Adjustment";
export default function Inventory() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [mode, setMode] = useState("summary");
 const [from, setFrom] = useState(""), [to, setTo] = useState(""), [asOf, setAsOf] = useState("");
 const [list, setList] = useState<{ as_of: string; rows: Row[]; total_value_cents: number } | null>(null);
 const [summary, setSummary] = useState<{ rows: SummaryRow[] } | null>(null);
 const [details, setDetails] = useState<{ rows: Detail[]; truncated: boolean } | null>(null);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 function reset() { setList(null); setSummary(null); setDetails(null); }
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; reset(); setError(""); setBusy(true);
  try {
   if (mode === "list") setList(await api(`/v1/reports/inventory-items?org_id=${orgId}&as_of=${asOf}`));
   else if (mode === "summary") setSummary(await api(`/v1/reports/inventory-summary?org_id=${orgId}&from=${from}&to=${to}`));
   else setDetails(await api(`/v1/reports/inventory-details?org_id=${orgId}&from=${from}&to=${to}`));
  } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 const filters = <form onSubmit={run} className="inline-form">
  <div><label htmlFor="inv-mode">Report</label><select id="inv-mode" disabled={busy} value={mode} onChange={e => { setMode(e.target.value); reset(); }}>
   <option value="list">Item list</option><option value="summary">Item summary</option><option value="details">Item details</option>
  </select></div>
  {mode === "list"
   ? <div><label htmlFor="inv-asof">As of</label><DateInput id="inv-asof" required disabled={busy} value={asOf} onChange={e => { setAsOf(e.target.value); reset(); }}/></div>
   : <><div><label htmlFor="inv-from">From</label><DateInput id="inv-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); reset(); }}/></div>
      <div><label htmlFor="inv-to">To</label><DateInput id="inv-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); reset(); }}/></div></>}
  <button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Inventory" description="Tracked stock only. Post supplier bills to bring stock in and sales invoices to take it out." filters={filters}>
  <p>Item list shows stock on hand at a date; item summary opens, in, out and closes for a period; item details shows every movement with its document and running balance. Untracked items never appear. Overselling a tracked item is refused at posting. Purchases come from supplier bills, sales and cost of goods sold from invoices, both at the movement cost; an invoice line carries no item link, so a sale valued at the item’s selling price is not reported. Adjustments cover stock adjustments and stocktakes.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {list && <div className="table-scroll"><table className="doc-table"><caption>Item list as at {list.as_of}</caption><thead><tr><th>SKU</th><th>Item</th><th>Inventory type</th><th className="num">Unit cost</th><th className="num">Sale price</th><th>Purchase description</th><th>On hand</th><th>Value</th></tr></thead><tbody>
   {list.rows.map(r => <tr key={r.item_id}><td>{r.sku || "—"}</td><th scope="row">{r.name}</th><td>{r.item_type === "goods" ? "Goods" : "Service"}</td><td className="num">{formatCents(r.unit_cost_cents)}</td><td className="num">{formatCents(r.sale_price_cents)}</td><td>{r.purchase_description || "—"}</td><td>{r.qty}{r.unit ? ` ${r.unit}` : ""}</td><td>{formatCents(r.value_cents)}</td></tr>)}</tbody>
   <tfoot><tr><th scope="row" colSpan={7}>Total value</th><td>{formatCents(list.total_value_cents)}</td></tr></tfoot></table></div>}
  {summary && <div className="table-scroll"><table className="doc-table"><caption>Item summary</caption><thead><tr><th>SKU</th><th>Item</th><th>Opening</th><th>In</th><th>Out</th><th>Purchases</th><th className="num">Purchases value</th><th>Sales</th><th className="num">COGS</th><th className="num">Adjustments</th><th>Closing</th><th>Closing value</th></tr></thead><tbody>
   {summary.rows.map(r => <tr key={r.item_id}><td>{r.sku || "—"}</td><th scope="row">{r.name}</th><td>{r.opening_qty}</td><td>{r.in_qty}</td><td>{r.out_qty}</td><td>{r.purchase_qty}</td><td className="num">{formatCents(r.purchase_value_cents)}</td><td>{r.sales_qty}</td><td className="num">{formatCents(r.cogs_value_cents)}</td><td className="num">{formatCents(r.adjustment_value_cents)}</td><td>{r.closing_qty}</td><td>{formatCents(r.closing_value_cents)}</td></tr>)}</tbody></table>
   {summary.rows.length === 0 && <p className="notice">No tracked items yet.</p>}</div>}
  {details && <div className="table-scroll"><table className="doc-table"><caption>Item details</caption><thead><tr><th>Date</th><th>Item</th><th>Source</th><th>Reference</th><th>Description</th><th>Contact</th><th>Movement</th><th>Cost</th><th className="num">Value movement</th><th>Running</th></tr></thead><tbody>
   {details.rows.map((r, i) => <tr key={i}><td>{r.movement_date}</td><th scope="row">{r.name}</th><td>{sourceLabel(r.source_kind)}</td><td>{r.reference || "—"}</td><td>{r.description || "—"}</td><td>{r.contact || "—"}</td><td>{r.qty_delta > 0 ? `+${r.qty_delta}` : r.qty_delta}</td><td>{formatCents(r.unit_cost_cents)}</td><td className="num">{formatCents(r.value_cents)}</td><td>{r.running_qty}</td></tr>)}</tbody></table>
   {details.truncated && <p className="notice">Showing the first 5,000 movements.</p>}</div>}
 </ReportFrame>;
}
