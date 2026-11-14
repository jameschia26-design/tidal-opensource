"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Attachment = { id: number; filename: string; size: number };
// Image files get a small preview; the first one is the primary image. Other files stay plain download links.
const isImage = (r: Attachment) => /\.(png|jpe?g|gif|webp)$/i.test(r.filename);
export default function Attachments({ kind, id, orgId, readOnly = false, limit = 10 }: { kind: string; id: number; orgId: number; readOnly?: boolean; limit?: number }) {
  const [rows, setRows] = useState<Attachment[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const path = `/v1/entities/${kind}/${id}/attachments?org_id=${orgId}`;
  useEffect(() => { let live = true; api<{ attachments: Attachment[] }>(path).then(r => { if (live) setRows(r.attachments); }).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); }); return () => { live = false; }; }, [path]);
  return <section><h3>Documents</h3><p className="section-help">Up to {limit} files, 10 MB each. Downloads are private to company members.</p>
    {rows.some(isImage) && <div className="thumb-row">{rows.filter(isImage).map((row, i) => <figure key={row.id}><img src={`/api/v1/attachments/${row.id}?org_id=${orgId}&preview=1`} alt={row.filename} width={64} height={64} loading="lazy" /><figcaption>{i === 0 ? "Primary" : row.filename}</figcaption></figure>)}</div>}
    {rows.map(row => <p key={row.id}><a href={`/api/v1/attachments/${row.id}?org_id=${orgId}`}>{row.filename}</a> ({Math.ceil(row.size / 1024)} KB)</p>)}
    {!readOnly && <><label htmlFor={`attachments-${kind}-${id}`}>Upload document</label><input id={`attachments-${kind}-${id}`} type="file" disabled={busy || rows.length >= limit} onChange={async e => {
      const file = e.target.files?.[0]; const input = e.target; if (!file) return;
      if (file.size === 0 || file.size > 10 * 1024 * 1024) { setError("Choose a nonempty file no larger than 10 MB."); return; }
      setBusy(true); setError("");
      try { const body = new FormData(); body.append("file", file); const response = await fetch(`/api${path}`, { method: "POST", credentials: "same-origin", body }); const result = await response.json(); if (!response.ok) throw { error: result.error, status: response.status }; setRows(prev => [...prev, result]); input.value = ""; }
      catch (e) { const err = e as ApiError; setError(err.error === "attachment_limit" ? "Document limit reached." : messageFor(err.error, err.status)); }
      finally { setBusy(false); }
    }} /></>}
    {busy && <p role="status">Uploading…</p>}{error && <p role="alert" className="form-error">{error}</p>}
  </section>;
}
