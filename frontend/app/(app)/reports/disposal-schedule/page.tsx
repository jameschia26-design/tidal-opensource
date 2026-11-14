"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import ReportFrame from "@/components/ReportFrame";
import DateInput from "@/components/DateInput";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";

type Disposal = { id: number; name: string; acquired_on: string; disposed_on: string; cost_cents: number; proceeds_cents: number; gain_loss_cents: number; cost_account: string; accumulated_depreciation_cents: number };
export default function DisposalSchedulePage() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [group, setGroup] = useState(false);
  const [rows, setRows] = useState<Disposal[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    const range = `${from ? `&from=${from}` : ""}${to ? `&to=${to}` : ""}`;
    api<{ rows: Disposal[] }>(`/v1/reports/disposal-schedule?org_id=${orgId}${range}`)
      .then(v => { if (live) { setRows(v.rows); setError(""); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, from, to]);
  const groups = new Map<string, Disposal[]>();
  if (group) for (const row of rows ?? []) groups.set(row.cost_account || "No cost account", [...(groups.get(row.cost_account || "No cost account") ?? []), row]);
  const body = (list: Disposal[]) => list.map(x => <tr key={x.id}><th scope="row">{x.name}</th><td>{x.acquired_on}</td><td>{x.disposed_on}</td><td>{x.cost_account || "—"}</td><td className="num">{formatCents(x.cost_cents)}</td><td className="num">{formatCents(x.accumulated_depreciation_cents)}</td><td className="num">{formatCents(x.proceeds_cents)}</td><td className="num">{formatCents(x.gain_loss_cents)}</td></tr>);
  return <><PageHeader title="Disposal schedule" description="Disposed fixed assets, proceeds and gain or loss." /><ReportFrame title="Disposal schedule" description="Register disposal history." filters={<div className="record-toolbar"><label>From<DateInput id="ds-from" value={from} onChange={e => setFrom(e.target.value)} /></label><label>To<DateInput id="ds-to" value={to} onChange={e => setTo(e.target.value)} /></label><label>Group by<select id="ds-group" value={group ? "account" : "none"} onChange={e => setGroup(e.target.value === "account")}><option value="none">No grouping</option><option value="account">Cost account</option></select></label></div>}>
    {error && <p role="alert" className="form-error">{error}</p>}{rows === null && !error && <p role="status">Loading disposals…</p>}{rows?.length === 0 && <p>No disposed assets in this period.</p>}
    {rows && rows.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Disposed assets</caption><thead><tr><th>Asset</th><th>Acquired</th><th>Disposed</th><th>Cost account</th><th className="num">Cost</th><th className="num">Depreciation</th><th className="num">Proceeds</th><th className="num">Gain / loss</th></tr></thead><tbody>
      {group ? Array.from(groups, ([account, list]) => <><tr key={`g-${account}`}><th scope="colgroup" colSpan={8}>{account}</th></tr>{body(list)}</>) : body(rows)}
    </tbody></table></div>}
  </ReportFrame></>;
}
