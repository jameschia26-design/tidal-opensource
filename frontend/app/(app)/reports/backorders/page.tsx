"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatDate } from "@/lib/dates";
import DateInput from "@/components/DateInput";

type Backorder = { id: number; item: string; sku: string; contact: string; ordered_on: string; qty: number; fulfilled_qty: number; open_qty: number; invoice_number: string; total_ordered: number };
export default function BackordersPage() {
  const { selected } = useOrg();
  const orgId = selected?.id ?? null;
  const [status, setStatus] = useState<"open" | "all">("open");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [group, setGroup] = useState<"none" | "invoice">("none");
  const [rows, setRows] = useState<Backorder[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    const range = `${from ? `&from=${from}` : ""}${to ? `&to=${to}` : ""}`;
    api<{ backorders: Backorder[] }>(`/v1/backorders?org_id=${orgId}&status=${status}${range}`)
      .then(r => { if (live) { setRows(r.backorders); setError(""); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, status, from, to]);
  const groups = new Map<string, Backorder[]>();
  if (group === "invoice") for (const row of rows) groups.set(row.invoice_number || "No invoice", [...(groups.get(row.invoice_number || "No invoice") ?? []), row]);
  const body = (list: Backorder[]) => list.map(b => <tr key={b.id}><td>{b.invoice_number || "—"}</td><td>{formatDate(b.ordered_on)}</td><th scope="row">{b.item}</th><td>{b.sku || "—"}</td><td>{b.contact}</td><td className="num">{b.total_ordered}</td><td className="num">{b.qty}</td><td className="num">{b.fulfilled_qty}</td><td className="num">{b.open_qty}</td></tr>);
  return <>
    <PageHeader title="Backorders" description="Goods promised on an invoice that stock could not cover." />
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {selected && <>
      <div className="record-toolbar">
        <label>Show<select id="backorder-status" value={status} onChange={e => setStatus(e.target.value as "open" | "all")}><option value="open">Open promises</option><option value="all">All promises</option></select></label>
        <label>From<DateInput id="backorder-from" value={from} onChange={e => setFrom(e.target.value)}/></label>
        <label>To<DateInput id="backorder-to" value={to} onChange={e => setTo(e.target.value)}/></label>
        <label>Group by<select id="backorder-group" value={group} onChange={e => setGroup(e.target.value as "none" | "invoice")}><option value="none">No grouping</option><option value="invoice">Invoice number</option></select></label>
      </div>
      {rows.length === 0 ? <p className="muted">No backorders to show.</p> : <div className="table-scroll"><table className="doc-table"><caption>Backorders</caption><thead><tr><th>Invoice number</th><th>Ordered</th><th>Item</th><th>Item code</th><th>Customer</th><th className="num">Total on order</th><th className="num">Ordered qty</th><th className="num">Fulfilled</th><th className="num">Open</th></tr></thead><tbody>
        {group === "invoice" ? Array.from(groups, ([invoice, list]) => <><tr key={`group-${invoice}`}><th scope="colgroup" colSpan={9}>{invoice}</th></tr>{body(list)}</>) : body(rows)}
      </tbody></table></div>}
    </>}
  </>;
}
