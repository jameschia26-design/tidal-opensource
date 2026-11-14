"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Field = { id: number; label: string; field_type: string; active: boolean };
type Tag = { id: number; name: string; options: string[]; active: boolean };

// Custom fields and reporting tags for one saved record. Definitions live in Settings → Custom fields and tags.
export default function Extras({ kind, id, orgId, readOnly = false }: { kind: string; id: number; orgId: number; readOnly?: boolean }) {
  const [fields, setFields] = useState<Field[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [custom, setCustom] = useState<Record<string, string>>({});
  const [chosen, setChosen] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    Promise.all([
      api<{ custom_fields: Field[] }>(`/v1/custom-fields?org_id=${orgId}&kind=${kind}`),
      api<{ reporting_tags: Tag[] }>(`/v1/reporting-tags?org_id=${orgId}`),
      api<{ custom: Record<string, string>; tags: Record<string, string> }>(`/v1/entities/${kind}/${id}/extras?org_id=${orgId}`),
    ]).then(([f, t, e]) => { if (live) { setFields(f.custom_fields); setTags(t.reporting_tags); setCustom(e.custom); setChosen(e.tags); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [kind, id, orgId]);
  const shownFields = fields.filter(f => f.active || custom[f.id]);
  const shownTags = tags.filter(t => t.active || chosen[t.id]);
  if (!error && shownFields.length === 0 && shownTags.length === 0) return <section><h3>Custom fields and tags</h3><p className="section-help">None defined. Add them in Settings → Custom fields and tags.</p></section>;
  async function save() {
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/entities/${kind}/${id}/extras`, { method: "PUT", body: JSON.stringify({ org_id: orgId, custom, tags: chosen }) });
      setNotice("Saved.");
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  return <section><h3>Custom fields and tags</h3>
    <fieldset disabled={busy || readOnly}><div className="form-grid">
      {shownFields.map(f => <div key={f.id}><label htmlFor={`cf-${kind}-${id}-${f.id}`}>{f.label}</label>
        {f.field_type === "yesno"
          ? <select id={`cf-${kind}-${id}-${f.id}`} value={custom[f.id] ?? ""} onChange={e => setCustom({ ...custom, [f.id]: e.target.value })}><option value="">Not set</option><option value="yes">Yes</option><option value="no">No</option></select>
          : <input id={`cf-${kind}-${id}-${f.id}`} type={f.field_type === "date" ? "date" : "text"} inputMode={f.field_type === "number" ? "decimal" : undefined} maxLength={500} value={custom[f.id] ?? ""} onChange={e => setCustom({ ...custom, [f.id]: e.target.value })} />}</div>)}
      {shownTags.map(t => <div key={t.id}><label htmlFor={`rt-${kind}-${id}-${t.id}`}>{t.name}</label>
        <select id={`rt-${kind}-${id}-${t.id}`} value={chosen[t.id] ?? ""} onChange={e => setChosen({ ...chosen, [t.id]: e.target.value })}><option value="">None</option>{t.options.map(o => <option key={o} value={o}>{o}</option>)}</select></div>)}
    </div>{!readOnly && <button type="button" onClick={save}>{busy ? "Saving…" : "Save custom fields and tags"}</button>}</fieldset>
    {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="form-error">{error}</p>}
  </section>;
}
