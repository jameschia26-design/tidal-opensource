"use client";
import { useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Preview = {
  id: number;
  status: string;
  counts: Record<string, number>;
  documents: Record<string, number>;
  exceptions: { table: string; reason: string; ordinal?: number; post_order?: number; reference?: string; kind?: string }[];
  reconciliation: null | {
    period: number;
    actual: Record<string, number>;
    expected: Record<string, number>;
    differences: Record<string, number>;
    matched: boolean;
    acknowledged?: string[];
  };
};

const money = (cents?: number) =>
  cents === undefined ? "—" : (cents / 100).toLocaleString("en-SG", { style: "currency", currency: "SGD" });

/** Upload a Peachtree backup, show what it holds and refuse to commit until the figures agree with
 *  the owner's own reports. Nothing here writes to the books. */
export default function PTBImport() {
  const { selected } = useOrg();
  const [preview, setPreview] = useState<Preview | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [period, setPeriod] = useState("35");
  const [figures, setFigures] = useState({ debit: "", credit: "", receivables: "", payables: "" });
  const [acceptPayables, setAcceptPayables] = useState(false);
  const [outcome, setOutcome] = useState("");
  if (!selected) return <p className="notice">Choose a company to import into.</p>;
  if (selected.role === "viewer") return <p className="notice">Company viewers cannot import.</p>;

  async function upload(file: File | null) {
    if (!file) return;
    setError(""); setPreview(null); setBusy(true);
    try {
      const body = new FormData();
      body.append("file", file);
      const created = await api<{ id: number }>(`/v1/imports/ptb?org_id=${selected!.id}`, { method: "POST", body });
      await reconcile(created.id, figures, period);
    } catch (e) {
      setError(messageFor((e as ApiError).error, (e as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  async function reconcile(id: number, values: typeof figures, at: string) {
    const request: Record<string, number> = { period: Number(at) };
    const keys: Record<keyof typeof figures, string> = {
      debit: "trial_balance_debit", credit: "trial_balance_credit",
      receivables: "receivables", payables: "payables",
    };
    for (const [label, key] of Object.entries(keys)) {
      const raw = values[label as keyof typeof figures].trim();
      if (raw) request[key] = Math.round(Number(raw) * 100);
    }
    if (acceptPayables) request.accept_payables_difference = 1;
    setPreview(await api<Preview>(`/v1/imports/ptb/${id}/preview?org_id=${selected!.id}`, {
      method: "POST", body: JSON.stringify(request),
    }));
  }

  async function recheck() {
    if (!preview) return;
    setBusy(true); setError("");
    try {
      await reconcile(preview.id, figures, period);
    } catch (e) {
      setError(messageFor((e as ApiError).error, (e as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  async function post(dryRun: boolean) {
    if (!preview) return;
    setBusy(true); setError("");
    try {
      const result = await api<{ dry_run?: boolean; would_create?: Record<string, number>; created?: number; skipped?: number; lines?: number }>(
        `/v1/imports/ptb/${preview.id}/commit?org_id=${selected!.id}`,
        { method: "POST", body: JSON.stringify({ dry_run: dryRun }) },
      );
      setOutcome(dryRun
        ? `Dry run: would create ${result.would_create?.journals ?? 0} journals (${result.would_create?.lines ?? 0} lines), ${result.would_create?.accounts ?? 0} accounts and ${result.would_create?.contacts ?? 0} contacts. Nothing written.`
        : `Posted ${result.created ?? 0} journals, ${result.lines ?? 0} lines; ${result.skipped ?? 0} already present.`);
      if (!dryRun) setPreview({ ...preview, status: "posted" });
    } catch (e) {
      setError(messageFor((e as ApiError).error, (e as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  const docLabels: Record<string, string> = {
    invoice: "Sales invoices", credit_note: "Credit notes", receipt: "Customer receipts",
    refund: "Customer refunds", payment: "Supplier payments", bill: "Supplier bills",
    bill_credit: "Supplier credits", journal: "General journals",
  };

  return (
    <section aria-label="Peachtree backup import">
      <h2>Peachtree backup (.ptb)</h2>
      <p className="section-help">
        Upload a Peachtree or Sage 50 backup. The server reads its accounts, customers, suppliers and every
        document, then shows what it found. Nothing is posted until the figures below agree with your reports.
      </p>
      {error && <p role="alert" className="form-error">{error}</p>}
      <section className="record-form">
        <label htmlFor="ptb-file">Backup file</label>
        <input id="ptb-file" type="file" accept=".ptb" disabled={busy}
          onChange={(e) => void upload(e.target.files?.[0] ?? null)} />
        {busy && <p className="muted">Reading the backup…</p>}
      </section>

      {preview && (
        <>
          <h3>What the backup holds</h3>
          <table>
            <caption>Records by entity</caption>
            <thead><tr><th>Entity</th><th>Count</th></tr></thead>
            <tbody>
              {Object.entries(preview.counts).map(([key, count]) => (
                <tr key={key}><td>{key}</td><td>{count}</td></tr>
              ))}
            </tbody>
          </table>
          <table>
            <caption>Documents by type</caption>
            <thead><tr><th>Type</th><th>Count</th></tr></thead>
            <tbody>
              {Object.entries(preview.documents).map(([key, count]) => (
                <tr key={key}><td>{docLabels[key] ?? key}</td><td>{count}</td></tr>
              ))}
            </tbody>
          </table>

          <h3>Rejected rows and tables</h3>
          {preview.exceptions.length === 0 ? (
            <p className="muted">Nothing was rejected.</p>
          ) : (
            <table>
              <caption>Every refused item and why</caption>
              <thead><tr><th>Table</th><th>Record</th><th>Source document</th><th>Reason</th></tr></thead>
              <tbody>
                {preview.exceptions.map((x, i) => <tr key={i}><td>{x.table}</td><td>{x.ordinal ? `#${x.ordinal}` : "—"}</td><td>{x.reference ? `${x.kind ?? "document"} ${x.reference}` : x.post_order ? `${x.kind ?? "document"} #${x.post_order}` : "—"}</td><td>{x.reason}</td></tr>)}
              </tbody>
            </table>
          )}

          <h3>Reconciliation</h3>
          <p className="section-help">
            Enter the closing figures from your own reports for the same period. A difference of more than
            one cent blocks the import.
          </p>
          <section className="record-form">
            <label htmlFor="ptb-period">Balance period (0 is the first period; 35 is the period ending 30 Sep 2026)</label>
            <input id="ptb-period" inputMode="numeric" value={period} onChange={(e) => setPeriod(e.target.value)} />
            {([["debit", "Trial balance debit total"], ["credit", "Trial balance credit total"],
              ["receivables", "Aged receivables total"], ["payables", "Aged payables total"]] as const).map(([key, label]) => (
              <span key={key}>
                <label htmlFor={`ptb-${key}`}>{label}</label>
                <input id={`ptb-${key}`} inputMode="decimal" value={figures[key]}
                  onChange={(e) => setFigures({ ...figures, [key]: e.target.value })} />
              </span>
            ))}
            <label className="check-label">
              <input type="checkbox" checked={acceptPayables} onChange={(e) => setAcceptPayables(e.target.checked)} />
              Accept the supplier ageing difference (the backup has no per-bill payment history)
            </label>
            <button type="button" className="button" onClick={() => void recheck()} disabled={busy}>Check figures</button>
          </section>

          {preview.reconciliation && (
            <table>
              <caption>Imported against your reports</caption>
              <thead><tr><th>Figure</th><th>From backup</th><th>Your report</th><th>Difference</th></tr></thead>
              <tbody>
                {Object.keys(preview.reconciliation.actual).filter((k) => k !== "trial_balance_net").map((key) => (
                  <tr key={key}>
                    <td>{key.replaceAll("_", " ")}</td>
                    <td>{money(preview.reconciliation!.actual[key])}</td>
                    <td>{money(preview.reconciliation!.expected[key])}</td>
                    <td>{money(preview.reconciliation!.differences[key])}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {preview.reconciliation && (
            <p className={preview.reconciliation.matched ? "notice" : "form-error"}>
              {preview.reconciliation.matched
                ? "Figures agree within one cent. Import is unlocked."
                : "Figures do not agree. Import stays locked until they match."}
            </p>
          )}
          {outcome && <p className="notice">{outcome}</p>}
          {preview.status !== "posted" && (
            <>
              <button type="button" className="button" onClick={() => void post(true)} disabled={busy}>
                Dry run
              </button>
              <button type="button" className="button" onClick={() => void post(false)}
                disabled={busy || !preview.reconciliation?.matched}>
                Import into books
              </button>
            </>
          )}
        </>
      )}
    </section>
  );
}
