"use client";
import { useState } from "react";
import { messageFor } from "@/lib/errors";

type Kind = "invoices" | "quotes" | "bills" | "purchase-orders" | "sales-receipts" | "customer-credit-notes";
// Kinds whose PDF has a Detailed template (salesperson, terms, discount, shipping, adjustment; bills add status and supplier reference).
const templated: Kind[] = ["invoices", "quotes", "bills", "purchase-orders", "sales-receipts", "customer-credit-notes"];

// Downloads a server-rendered PDF. Same-origin, cookie-authenticated.
export default function PdfButton({ kind, id, orgId }: { kind: Kind; id: number; orgId: number }) {
  const [template, setTemplate] = useState("standard");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function download() {
    setBusy(true); setError("");
    try {
      const res = await fetch(`/api/v1/documents/${kind}/${id}/pdf?org_id=${orgId}${templated.includes(kind) ? `&template=${template}` : ""}`, { credentials: "same-origin", cache: "no-store" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw { error: typeof body.error === "string" ? body.error : "request_failed", status: res.status };
      }
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `${kind}-${id}.pdf`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      const err = e as { error?: string; status?: number };
      setError(messageFor(err.error, err.status));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      {templated.includes(kind) && <><label htmlFor={`pdf-template-${kind}-${id}`}>PDF template</label><select id={`pdf-template-${kind}-${id}`} value={template} onChange={e => setTemplate(e.target.value)}><option value="standard">Standard</option><option value="detailed">Detailed</option></select></>}
      <button type="button" className="button-secondary" disabled={busy} onClick={download}>{busy ? "Preparing PDF…" : "Download PDF"}</button>
      {error && <p role="alert" className="form-error">{error}</p>}
    </>
  );
}
