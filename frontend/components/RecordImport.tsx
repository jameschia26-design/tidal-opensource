"use client";
import { useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { csvTemplate, downloadCsv, parseCsv } from "@/lib/csv";

// A row-by-row CSV import. Each row is turned into a request body by `toBody` and posted to the record's own create
// endpoint, so the server's validation applies to every row. Rows that fail are listed; the rest still import.
// Nothing is posted until the user presses Import.
export type ImportSpec = {
  label: string;
  endpoint: string;
  headers: string[];
  help: string;
  toBody: (rec: Record<string, string>, row: number, fileTag: string) => Record<string, unknown> | string;
};

export default function RecordImport({ spec, orgId, onDone, rows: prefill }: { spec: ImportSpec; orgId: number; onDone: () => Promise<void>; rows?: string[][] }) {
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [log, setLog] = useState<string[]>([]);
  const [error, setError] = useState("");

  async function run() {
    if (busy) return;
    if (!file && !prefill) return;
    setBusy(true); setError(""); setLog([]);
    const tag = Date.now().toString(36); // new tag per run: a new file is a new idempotency key
    try {
      const rows = prefill ?? parseCsv(await file!.text());
      if (rows.length < 2) { setError("The file needs a header row and at least one data row."); return; }
      const head = rows[0].map(h => h.toLowerCase());
      const missing = spec.headers.filter(h => !head.includes(h));
      if (missing.length) { setError(`The header row is missing: ${missing.join(", ")}.`); return; }
      const out: string[] = [];
      let ok = 0;
      for (let i = 1; i < rows.length; i++) {
        const rec: Record<string, string> = {};
        head.forEach((h, c) => { rec[h] = rows[i][c] ?? ""; });
        const label = `row ${i + 1}`;
        const body = spec.toBody(rec, i + 1, tag);
        if (typeof body === "string") { out.push(`${label}: ${body}`); continue; }
        try {
          await api(spec.endpoint, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) });
          ok++;
        } catch (e) {
          const x = e as ApiError;
          out.push(`${label}: ${messageFor(x.error, x.status)}`);
        }
      }
      setLog([`${ok} imported, ${out.length} not imported.`, ...out]);
      await onDone();
    } catch {
      setError("The file could not be read. Save it as a plain CSV file and try again.");
    } finally {
      setBusy(false);
    }
  }

  return <section className="record-form" aria-label={`Import ${spec.label}`}>
    <h2>Import {spec.label} from CSV</h2>
    <p className="section-help">Columns: {spec.headers.join(", ")}. {spec.help} Each row is checked on its own.</p>
    {!prefill && <>
      <label htmlFor={`import-${spec.endpoint.replace(/\W+/g, "-")}`}>CSV file</label>
      <input id={`import-${spec.endpoint.replace(/\W+/g, "-")}`} type="file" accept=".csv,text/csv" disabled={busy} onChange={e => setFile(e.target.files?.[0] ?? null)} />
    </>}
    <div className="form-actions">
      <button type="button" className="button-secondary" disabled={busy} onClick={() => downloadCsv(`import-${spec.label.replace(/\s+/g, "-")}.csv`, csvTemplate(spec.headers))}>Download template</button>
      <button type="button" disabled={busy || (!file && !prefill)} onClick={() => void run()}>{busy ? "Importing…" : "Import"}</button>
    </div>
    {error && <p role="alert" className="form-error">{error}</p>}
    {log.length > 0 && <ul aria-live="polite">{log.map((l, i) => <li key={i}>{l}</li>)}</ul>}
  </section>;
}
