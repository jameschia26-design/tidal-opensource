"use client";
import { useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { parseCsv } from "@/lib/csv";

// Imports customers from a CSV file. Each row is posted to the same create endpoint as the New Customer form,
// so every row gets the same validation. Expected header: name,email,company_name,contact_type,phone,payment_terms_days.
// Rows that fail are listed with their reason; the rows before and after still import.
const COLUMNS = ["name", "email", "company_name", "contact_type", "phone", "payment_terms_days"] as const;

export default function ContactImport({ orgId, role, onDone }: { orgId: number; role: "customer" | "supplier"; onDone: () => Promise<void> }) {
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [log, setLog] = useState<string[]>([]);
  const [error, setError] = useState("");

  async function run() {
    if (!file || busy) return;
    setBusy(true); setError(""); setLog([]);
    try {
      const rows = parseCsv(await file.text());
      if (rows.length < 2) { setError("The file needs a header row and at least one data row."); return; }
      const head = rows[0].map(h => h.toLowerCase());
      if (!head.includes("name")) { setError("The header row must include a name column."); return; }
      const out: string[] = [];
      let ok = 0;
      for (const cells of rows.slice(1)) {
        const rec: Record<string, string> = {};
        head.forEach((h, i) => { if ((COLUMNS as readonly string[]).includes(h)) rec[h] = cells[i] ?? ""; });
        const n = rec.name || `row ${out.length + ok + 2}`;
        // Reject bad terms here: Number("x") is NaN and JSON turns NaN into null, which the server would read as 0 days.
        const terms = rec.payment_terms_days ? Number(rec.payment_terms_days) : 0;
        if (!Number.isInteger(terms) || terms < 0 || terms > 365) { out.push(`${n}: payment_terms_days must be a whole number from 0 to 365`); continue; }
        const body = {
          org_id: orgId, role, name: rec.name ?? "", email: rec.email ?? "", company_name: rec.company_name ?? "",
          contact_type: rec.contact_type || "business", phone: rec.phone ?? "", payment_terms_days: terms,
          language: "en", currency: "SGD", portal_enabled: false, owner_user_id: 0, opening_balance_cents: 0,
          billing_address: { attention: "", line1: "", line2: "", city: "Singapore", region: "", postal_code: "", country: "SG", phone: "", fax: "" },
          shipping_address: { attention: "", line1: "", line2: "", city: "Singapore", region: "", postal_code: "", country: "SG", phone: "", fax: "" },
          contact_persons: [], notes: "", internal_remarks: "",
        };
        try {
          await api("/v1/contacts", { method: "POST", body: JSON.stringify(body) });
          ok++;
        } catch (e) {
          const x = e as ApiError;
          out.push(`${n}: ${messageFor(x.error, x.status)}`);
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

  return <section className="record-form" aria-label="Import customers">
    <h2>Import {role === "customer" ? "customers" : "suppliers"} from CSV</h2>
    <p className="section-help">Columns: name (required), email, company_name, contact_type (business or individual), phone, payment_terms_days. Each row is checked on its own; nothing is changed for rows that fail.</p>
    <label htmlFor="contact-import-file">CSV file</label>
    <input id="contact-import-file" type="file" accept=".csv,text/csv" disabled={busy} onChange={e => setFile(e.target.files?.[0] ?? null)} />
    <div className="form-actions"><button type="button" disabled={!file || busy} onClick={() => void run()}>{busy ? "Importing…" : "Import"}</button></div>
    {error && <p role="alert" className="form-error">{error}</p>}
    {log.length > 0 && <ul aria-live="polite">{log.map((l, i) => <li key={i}>{l}</li>)}</ul>}
  </section>;
}
