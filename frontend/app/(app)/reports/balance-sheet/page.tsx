"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import ReportBlocks from "@/components/ReportBlocks";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { bsBlocks, type BalanceSheetData } from "@/lib/report-shape";
import { formatDate } from "@/lib/dates";
import DateInput from "@/components/DateInput";

type BS = BalanceSheetData & { compare?: BalanceSheetData };
const DATE = /^\d{4}-\d{2}-\d{2}$/;

export default function BalanceSheet() {
  const orgId = useOrgId();
  return <BalanceSheetWorkspace key={orgId ?? "none"} orgId={orgId} />;
}

function BalanceSheetWorkspace({ orgId }: { orgId: number | null }) {
  const [asOf, setAsOf] = useState("");
  const [data, setData] = useState<BS | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [compare, setCompare] = useState(false);

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!DATE.test(asOf)) { setError("Choose a date."); return; }
    setBusy(true);
    setError(null);
    try {
      setData(await api<BS>(`/v1/reports/balance-sheet?as_of=${asOf}${compare ? "&compare=previous" : ""}${orgId != null ? `&org_id=${orgId}` : ""}`));
    } catch (err) {
      setData(null);
      setError(messageFor((err as ApiError).error, (err as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title="Balance sheet" description="Bank, current, fixed, inventory and prepayment assets, liabilities and equity as at a date." period={data ? `As at ${formatDate(asOf)}` : undefined} />
      <PreviewNotice>Equity shows retained earnings as its own line and the profit not yet closed to it as a separate calculated line.</PreviewNotice>
      <form onSubmit={run} className="inline-form" noValidate>
        <div><label htmlFor="bs-asof">As at</label><DateInput id="bs-asof" value={asOf} disabled={busy} onChange={(e) => { setAsOf(e.target.value); setData(null); }}/></div>
        <label className="check"><input type="checkbox" checked={compare} disabled={busy} onChange={(e) => { setCompare(e.target.checked); setData(null); }} /> Compare with previous year</label>
        <button type="submit" disabled={busy || orgId == null}>{busy ? "Running…" : "Run report"}</button>
      </form>
      {orgId == null && <p className="muted">Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {data && (
        <>
          <ReportBlocks blocks={bsBlocks(data, data.compare)} showPrevious={!!data.compare} />
          <p className={data.balanced ? "muted" : "form-error"}>{data.balanced ? "Net assets equal assets less liabilities, and equity agrees with net assets." : "Balance sheet does not balance. Report this to support."}</p>
        </>
      )}
    </>
  );
}
