"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { clearTrackingCache, type TrackingCategory } from "@/components/TrackingSelect";

// Tracking categories: two active per company, each with named options. Renaming keeps history,
// archiving stops an option or a category being chosen on a new line without touching old ones.
export default function TrackingSettings() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id ?? null;
  const canWrite = selected != null && selected.role !== "viewer";
  const [categories, setCategories] = useState<TrackingCategory[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState({ name: "", options: "" });
  const [rename, setRename] = useState<Record<number, string>>({});
  const [optionDraft, setOptionDraft] = useState<Record<number, string>>({});
  const [optionName, setOptionName] = useState<Record<number, string>>({});

  async function load() {
    if (orgId == null) return;
    try {
      const r = await api<{ categories: TrackingCategory[] }>(`/v1/tracking/categories?org_id=${orgId}`);
      setCategories(r.categories);
      clearTrackingCache();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }
  useEffect(() => { void load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [orgId]);

  async function send(method: string, path: string, body: unknown, message: string) {
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(path, { method, body: JSON.stringify(body) });
      setNotice(message);
      await load();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function addCategory(e: FormEvent) {
    e.preventDefault();
    if (orgId == null || draft.name.trim() === "") { setError("Enter a category name."); return; }
    const options = draft.options.split(",").map(o => o.trim()).filter(Boolean);
    await send("POST", "/v1/tracking/categories", { org_id: orgId, name: draft.name.trim(), options }, "Tracking category added.");
    setDraft({ name: "", options: "" });
  }

  const activeCount = categories.filter(c => c.active).length;

  return <>
    <PageHeader title="Tracking categories" description="Tag document lines with a category and option, then report on them." />
    <p className="notice">A company can have two active categories. Values are chosen on each line of an invoice, bill, credit note, purchase order or journal, so one document can split across options. Archiving keeps every existing value on the records that already carry it.</p>
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {orgId == null && <p>Choose a company first.</p>}
    {orgId != null && <>
      <section aria-label="Existing tracking categories">
        {categories.length === 0 && <p>No tracking categories yet.</p>}
        {categories.map(c => <section key={c.id} className="record-form">
          <h2>{c.name} {c.active ? "" : "(archived)"}</h2>
          <div className="form-grid">
            <div>
              <label htmlFor={`tc-name-${c.id}`}>Category name</label>
              <input id={`tc-name-${c.id}`} maxLength={100} value={rename[c.id] ?? c.name} disabled={!canWrite || busy}
                onChange={e => setRename({ ...rename, [c.id]: e.target.value })} />
            </div>
            <div className="form-actions">
              <button type="button" disabled={!canWrite || busy} onClick={() => void send("PUT", `/v1/tracking/categories/${c.id}`, { org_id: orgId, name: (rename[c.id] ?? c.name).trim(), active: c.active }, "Category renamed.")}>Save name</button>
              <button type="button" className="button-secondary" disabled={!canWrite || busy}
                onClick={() => void send("PUT", `/v1/tracking/categories/${c.id}`, { org_id: orgId, name: (rename[c.id] ?? c.name).trim(), active: !c.active }, c.active ? "Category archived. Existing values are kept." : "Category restored.")}>
                {c.active ? "Archive category" : "Restore category"}
              </button>
            </div>
          </div>
          <table className="doc-table"><caption>{c.name} options</caption>
            <thead><tr><th scope="col">Option</th><th scope="col">State</th><th scope="col">Actions</th></tr></thead>
            <tbody>
              {c.options.length === 0 && <tr><td colSpan={3}>No options yet.</td></tr>}
              {c.options.map(o => <tr key={o.id}>
                <td><label htmlFor={`to-name-${o.id}`} className="sr-only">Option name</label>
                  <input id={`to-name-${o.id}`} maxLength={100} value={optionName[o.id] ?? o.name} disabled={!canWrite || busy}
                    onChange={e => setOptionName({ ...optionName, [o.id]: e.target.value })} /></td>
                <td>{o.archived ? "Archived" : "Active"}</td>
                <td className="form-actions">
                  <button type="button" disabled={!canWrite || busy}
                    onClick={() => void send("PUT", `/v1/tracking/options/${o.id}`, { org_id: orgId, name: (optionName[o.id] ?? o.name).trim(), archived: o.archived }, "Option renamed.")}>Save</button>
                  <button type="button" className="button-secondary" disabled={!canWrite || busy}
                    onClick={() => void send("PUT", `/v1/tracking/options/${o.id}`, { org_id: orgId, name: (optionName[o.id] ?? o.name).trim(), archived: !o.archived }, o.archived ? "Option restored." : "Option archived. Existing values are kept.")}>
                    {o.archived ? "Restore" : "Archive"}
                  </button>
                </td>
              </tr>)}
            </tbody>
          </table>
          <form className="inline-form" onSubmit={e => {
            e.preventDefault();
            const name = (optionDraft[c.id] ?? "").trim();
            if (name === "") { setError("Enter an option name."); return; }
            void send("POST", "/v1/tracking/options", { org_id: orgId, category_id: c.id, name }, "Option added.");
            setOptionDraft({ ...optionDraft, [c.id]: "" });
          }}>
            <div><label htmlFor={`to-add-${c.id}`}>New option in {c.name}</label>
              <input id={`to-add-${c.id}`} maxLength={100} disabled={!canWrite || busy} value={optionDraft[c.id] ?? ""}
                onChange={e => setOptionDraft({ ...optionDraft, [c.id]: e.target.value })} /></div>
            <button disabled={!canWrite || busy}>Add option</button>
          </form>
        </section>)}
      </section>
      <form className="record-form" onSubmit={addCategory}>
        <h2>Add a tracking category</h2>
        {activeCount >= 2 && <p className="notice">Two categories are already active. Archive one first: a company can have two.</p>}
        <div className="form-grid">
          <div><label htmlFor="tc-new-name">Category name</label>
            <input id="tc-new-name" maxLength={100} disabled={!canWrite || busy || activeCount >= 2} value={draft.name}
              onChange={e => setDraft({ ...draft, name: e.target.value })} /></div>
          <div><label htmlFor="tc-new-options">Options <span className="field-optional">Comma separated</span></label>
            <input id="tc-new-options" maxLength={500} disabled={!canWrite || busy || activeCount >= 2} value={draft.options}
              onChange={e => setDraft({ ...draft, options: e.target.value })} /></div>
        </div>
        <button disabled={!canWrite || busy || activeCount >= 2}>Add category</button>
      </form>
    </>}
  </>;
}
