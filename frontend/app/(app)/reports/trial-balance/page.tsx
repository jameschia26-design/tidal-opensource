"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { code: string; name: string; type: string; debit_cents: number; credit_cents: number };
const COLS = [["code", "Code"], ["name", "Account"], ["type", "Type"]] as const;
type Compare = { as_of: string; rows: Row[]; total_debit_cents: number; total_credit_cents: number; balanced: boolean };
type TB = { rows: Row[]; total_debit_cents: number; total_credit_cents: number; balanced: boolean; compare?: Compare };
const DATE = /^\d{4}-\d{2}-\d{2}$/;

export default function TrialBalance() {
  const orgId = useOrgId();
  return <TrialBalanceWorkspace key={orgId ?? "none"} orgId={orgId} />;
}

function TrialBalanceWorkspace({ orgId }: { orgId: number | null }) {
  const [asOf, setAsOf] = useState("");
  const [data, setData] = useState<TB | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [compare, setCompare] = useState(false);
  const [group, setGroup] = useState<"none" | "type">("none");
  const [shown, setShown] = useState<string[]>(["code", "name", "type"]);

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!DATE.test(asOf)) { setError("Choose a date."); return; }
    setBusy(true);
    setError(null);
    try {
      setData(await api<TB>(`/v1/reports/trial-balance?as_of=${asOf}${compare ? "&compare=previous" : ""}${orgId != null ? `&org_id=${orgId}` : ""}`));
    } catch (err) {
      setData(null);
      setError(messageFor((err as ApiError).error, (err as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title="Trial balance" description="Balances from posted journal entries." />
      <PreviewNotice>Draft and unposted documents are not included.</PreviewNotice>
      <form onSubmit={run} className="inline-form" noValidate>
        <div><label htmlFor="asof">As of</label><DateInput id="asof" value={asOf} disabled={busy} onChange={(e) => { setAsOf(e.target.value); setData(null); }}/></div>
        <label className="check"><input type="checkbox" checked={compare} disabled={busy} onChange={(e) => { setCompare(e.target.checked); setData(null); }} /> Compare with previous year</label>
        <label>Group by<select value={group} disabled={busy} onChange={(e) => setGroup(e.target.value as "none" | "type")}><option value="none">None</option><option value="type">Type</option></select></label>
        <details><summary>Columns</summary>{COLS.map(([k, label]) => <label key={k} className="check"><input type="checkbox" checked={shown.includes(k)} disabled={busy} onChange={(e) => setShown(e.target.checked ? [...shown, k] : shown.filter((v) => v !== k))} /> {label}</label>)}</details>
        <button type="submit" disabled={busy || orgId == null}>{busy ? "Running…" : "Run report"}</button>
        <button type="button" disabled={busy || !data} onClick={() => {
          if (!data) return;
          const head = [...COLS.filter(([k]) => shown.includes(k)).map(([, l]) => l), "Debit", "Credit", ...(data.compare ? ["Debit last year", "Credit last year"] : [])];
          const body = data.rows.map((r) => [...COLS.filter(([k]) => shown.includes(k)).map(([k]) => (r as unknown as Record<string, string>)[k]), (r.debit_cents / 100).toFixed(2), (r.credit_cents / 100).toFixed(2), ...(data.compare ? [((data.compare.rows.find((q) => q.code === r.code)?.debit_cents ?? 0) / 100).toFixed(2), ((data.compare.rows.find((q) => q.code === r.code)?.credit_cents ?? 0) / 100).toFixed(2)] : [])]);
          const csv = [head, ...body].map((r) => r.map((v) => `"${String(v).replaceAll('"', '""')}"`).join(",")).join("\n");
          const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" })); const a = document.createElement("a"); a.href = url; a.download = `trial-balance-${asOf}.csv`; a.click(); URL.revokeObjectURL(url);
        }}>Export CSV</button>
      </form>
      {orgId == null && <p className="muted">Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {data && (
        <>
          <table className="doc-table">
            <caption className="sr-only">Trial balance</caption>
            <thead><tr>{shown.includes("code") && <th scope="col">Code</th>}{shown.includes("name") && <th scope="col">Account</th>}{shown.includes("type") && <th scope="col">Type</th>}<th scope="col" className="num">Debit</th><th scope="col" className="num">Credit</th>{data.compare && <><th scope="col" className="num">Debit last year</th><th scope="col" className="num">Credit last year</th></>}</tr></thead>
            <tbody>
              {(group === "type" ? [...new Set(data.rows.map((r) => r.type))] : [""]).flatMap((g) => [
                ...(group === "type" ? [<tr key={`g-${g}`}><th scope="row" colSpan={shown.length + 2 + (data.compare ? 2 : 0)}>{g}</th></tr>] : []),
                ...data.rows.filter((r) => group === "none" || r.type === g).map((r) => {
                const prev = data.compare?.rows.find((p) => p.code === r.code);
                return (
                  <tr key={r.code}>{shown.includes("code") && <td>{r.code}</td>}{shown.includes("name") && <td>{r.name}</td>}{shown.includes("type") && <td>{r.type}</td>}<td className="num">{formatCents(r.debit_cents)}</td><td className="num">{formatCents(r.credit_cents)}</td>{data.compare && <><td className="num">{formatCents(prev?.debit_cents ?? 0)}</td><td className="num">{formatCents(prev?.credit_cents ?? 0)}</td></>}</tr>
                );
                })])}
              <tr><th scope="row" colSpan={shown.length}>Totals</th><td className="num">{formatCents(data.total_debit_cents)}</td><td className="num">{formatCents(data.total_credit_cents)}</td>{data.compare && <><td className="num">{formatCents(data.compare.total_debit_cents)}</td><td className="num">{formatCents(data.compare.total_credit_cents)}</td></>}</tr>
            </tbody>
          </table>
          <p className={data.balanced ? "muted" : "form-error"}>{data.balanced ? "Debits equal credits." : "Debits do not equal credits. Report this to support."}</p>
        </>
      )}
    </>
  );
}
