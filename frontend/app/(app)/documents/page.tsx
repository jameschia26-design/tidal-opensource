"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Doc = { id: number; folder_id: number; filename: string; size: number; added_on: string };
type Folder = { id: number; name: string; documents: number };
// view is "all", "inbox", "trash" or a folder id as a string.
type View = string;

export default function DocumentsPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const generation = useRef(0);
  const [view, setView] = useState<View>("all");
  const [docs, setDocs] = useState<Doc[] | null>(null);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [folderName, setFolderName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const fail = (e: unknown) => { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } };
  const load = useCallback(async () => {
    if (orgId == null) return;
    const request = ++generation.current;
    const q = view === "all" || view === "inbox" || view === "trash" ? `view=${view}` : `view=folder&folder_id=${view}`;
    const [d, f] = await Promise.all([api<{ documents: Doc[] }>(`/v1/documents?org_id=${orgId}&${q}`), api<{ folders: Folder[] }>(`/v1/document-folders?org_id=${orgId}`)]);
    if (live.current && generation.current === request) { setDocs(d.documents); setFolders(f.folders); }
  }, [orgId, view]);
  useEffect(() => {
    live.current = true;
    setDocs(null);
    void load().catch(fail);
    return () => { live.current = false; generation.current++; };
  }, [load]);
  async function run(work: () => Promise<unknown>) {
    if (busy || orgId == null) return;
    setBusy(true); setError("");
    try { await work(); await load(); } catch (e) { fail(e); } finally { if (live.current) setBusy(false); }
  }
  const act = (id: number, action: string, folder = 0) => run(() => api(`/v1/documents/${id}/action`, { method: "POST", body: JSON.stringify({ org_id: orgId, action, folder_id: folder }) }));
  const upload = (file: File) => run(async () => {
    if (file.size === 0 || file.size > 10 * 1024 * 1024) throw { error: "invalid_upload", status: 422 };
    const body = new FormData(); body.append("file", file);
    const folder = /^\d+$/.test(view) ? `&folder_id=${view}` : "";
    const res = await fetch(`/api/v1/documents?org_id=${orgId}${folder}`, { method: "POST", credentials: "same-origin", body });
    if (!res.ok) { const r = await res.json().catch(() => ({})); throw { error: r.error ?? "request_failed", status: res.status }; }
  });
  const tabs: [View, string][] = [["all", "All documents"], ["inbox", "Inbox"], ["trash", "Trash"]];
  const trash = view === "trash";
  return <>
    <PageHeader title="Documents" description="Receipts, contracts and other files kept for the company. Files in no folder sit in the Inbox." />
    <p className="notice">Autoscan (reading receipts into bills automatically) is not available. Add details to a bill by hand.</p>
    <nav aria-label="Document views" className="form-actions">
      {tabs.map(([v, label]) => <button key={v} className={view === v ? "" : "button-secondary"} aria-pressed={view === v} disabled={busy} onClick={() => setView(v)}>{label}</button>)}
      {folders.map(f => <button key={f.id} className={view === String(f.id) ? "" : "button-secondary"} aria-pressed={view === String(f.id)} disabled={busy} onClick={() => setView(String(f.id))}>{f.name} ({f.documents})</button>)}
    </nav>
    {error && <p role="alert" className="form-error">{error}</p>}
    {canWrite && !trash && <div className="form-actions">
      <label htmlFor="doc-upload">Upload file (max 10 MB)</label>
      <input id="doc-upload" type="file" disabled={busy} onChange={e => { const f = e.target.files?.[0]; const el = e.target; if (f) void upload(f).then(() => { el.value = ""; }); }} />
    </div>}
    {canWrite && view === "all" && <form className="form-actions" onSubmit={e => { e.preventDefault(); const name = folderName.trim(); if (name) void run(async () => { await api("/v1/document-folders", { method: "POST", body: JSON.stringify({ org_id: orgId, name }) }); setFolderName(""); }); }}>
      <label htmlFor="folder-name">New folder name</label>
      <input id="folder-name" value={folderName} maxLength={100} onChange={e => setFolderName(e.target.value)} />
      <button disabled={busy || !folderName.trim()}>Create new folder</button>
    </form>}
    {canWrite && /^\d+$/.test(view) && docs?.length === 0 && <div className="form-actions"><button className="button-secondary" disabled={busy} onClick={() => void run(async () => { await api(`/v1/document-folders/${view}?org_id=${orgId}`, { method: "DELETE" }); setView("all"); })}>Delete this empty folder</button></div>}
    {docs?.length === 0 && <p>{trash ? "Trash is empty." : "No documents here."}</p>}
    {!!docs?.length && <div className="table-scroll"><table className="doc-table"><caption>{trash ? "Trash" : "Documents"}</caption><thead><tr><th>File</th><th>Size</th><th>Added</th><th>Folder</th>{canWrite && <th>Action</th>}</tr></thead><tbody>{docs.map(d => <tr key={d.id}>
      <td><a href={`/api/v1/documents/${d.id}?org_id=${orgId}`}>{d.filename}</a></td><td>{Math.ceil(d.size / 1024)} KB</td><td>{d.added_on}</td>
      <td>{canWrite && !trash ? <select aria-label={`Folder for ${d.filename}`} value={d.folder_id} disabled={busy} onChange={e => void act(d.id, "move", Number(e.target.value))}><option value={0}>Inbox</option>{folders.map(f => <option key={f.id} value={f.id}>{f.name}</option>)}</select> : (folders.find(f => f.id === d.folder_id)?.name ?? "Inbox")}</td>
      {canWrite && <td>{trash
        ? <><button className="button-secondary" disabled={busy} onClick={() => void act(d.id, "restore")}>Restore<span className="sr-only"> {d.filename}</span></button> <button className="button-secondary" disabled={busy} onClick={() => { if (window.confirm(`Permanently delete ${d.filename}? This cannot be undone.`)) void act(d.id, "delete"); }}>Delete forever<span className="sr-only"> {d.filename}</span></button></>
        : <button className="button-secondary" disabled={busy} onClick={() => void act(d.id, "trash")}>Move to trash<span className="sr-only"> {d.filename}</span></button>}</td>}
    </tr>)}</tbody></table></div>}
  </>;
}
