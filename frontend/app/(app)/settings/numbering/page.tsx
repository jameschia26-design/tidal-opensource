"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Settings = { invoice_prefix: string; invoice_next: number; bill_prefix: string; bill_next: number; credit_note_prefix: string; credit_note_next: number; purchase_order_prefix: string; purchase_order_next: number; quote_prefix: string; quote_next: number };
const TYPES: { key: string; label: string }[] = [
  { key: "invoice", label: "Invoice" },
  { key: "bill", label: "Bill" },
  { key: "credit_note", label: "Credit note" },
  { key: "purchase_order", label: "Purchase order" },
  { key: "quote", label: "Quote" },
];
const blank: Settings = { invoice_prefix: "", invoice_next: 1, bill_prefix: "", bill_next: 1, credit_note_prefix: "", credit_note_next: 1, purchase_order_prefix: "", purchase_order_next: 1, quote_prefix: "", quote_next: 1 };

export default function NumberingSettings() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && ["owner", "admin", "accountant"].includes(selected.role);
  const [form, setForm] = useState<Settings>(blank);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<Settings>(`/v1/document-numbering?org_id=${orgId}`).then(r => { if (live) { setForm(r); setLoaded(true); } }).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  async function save(e: FormEvent) {
    e.preventDefault();
    if (orgId == null || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const saved = await api<Settings>(`/v1/document-numbering?org_id=${orgId}`, { method: "PUT", body: JSON.stringify(form) });
      setForm(saved); setNotice("Numbering settings saved. New documents use them; posted documents are never renumbered.");
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  return <>
    <PageHeader title="Document numbering" description="Prefixes and next numbers for new documents." />
    <p className="notice">A number is taken when a draft starts and can be edited until it is posted. Posting never changes it. A typed number must be unique for its type.</p>
    {!selected && <p className="notice">Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && !loaded && !error && <p role="status">Loading…</p>}
    {selected && loaded && <form className="record-form" onSubmit={save}><fieldset disabled={busy || !canWrite}><legend>Numbering</legend><div className="table-scroll"><table className="doc-table"><caption className="sr-only">Document numbering settings</caption><thead><tr><th scope="col">Document</th><th scope="col">Prefix</th><th scope="col">Next number</th><th scope="col">Preview</th></tr></thead><tbody>
      {TYPES.map(({ key, label }) => <tr key={key}><th scope="row">{label}</th><td><input aria-label={`${label} prefix`} maxLength={10} value={form[`${key}_prefix` as keyof Settings] as string} onChange={e => setForm({ ...form, [`${key}_prefix`]: e.target.value })} /></td><td><input aria-label={`${label} next number`} type="number" min={1} max={1000000000} value={form[`${key}_next` as keyof Settings] as number} onChange={e => setForm({ ...form, [`${key}_next`]: Number(e.target.value) })} /></td><td>{String(form[`${key}_prefix` as keyof Settings])}{String(form[`${key}_next` as keyof Settings]).padStart(5, "0")}</td></tr>)}
    </tbody></table></div><p className="section-help">Changing the next number does not renumber existing documents. {!canWrite && "Your role allows viewing only."}</p></fieldset><div className="form-actions"><button disabled={busy || !canWrite}>{busy ? "Saving…" : "Save numbering"}</button></div></form>}
  </>;
}
