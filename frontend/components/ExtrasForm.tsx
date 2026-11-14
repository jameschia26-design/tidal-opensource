"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Field = { id: number; label: string; field_type: string; active: boolean };
type Tag = { id: number; name: string; options: string[]; active: boolean };
export type ExtrasValue = { custom: Record<string, string>; tags: Record<string, string> };
export const noExtras = (): ExtrasValue => ({ custom: {}, tags: {} });

// Custom fields and reporting tags on a create form. Values are held here and written once the record exists (see saveExtras).
export default function ExtrasForm({ kind, orgId, value, onChange, tags: withTags = true }: { kind: string; orgId: number; value: ExtrasValue; onChange: (v: ExtrasValue) => void; tags?: boolean }) {
  const [fields, setFields] = useState<Field[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    Promise.all([api<{ custom_fields: Field[] }>(`/v1/custom-fields?org_id=${orgId}&kind=${kind}`), api<{ reporting_tags: Tag[] }>(`/v1/reporting-tags?org_id=${orgId}`)])
      .then(([f, t]) => { if (live) { setFields(f.custom_fields.filter(x => x.active)); setTags(t.reporting_tags.filter(x => x.active)); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [kind, orgId]);
  const shown = withTags ? tags : [];
  if (!error && fields.length === 0 && shown.length === 0) return null;
  return <fieldset><legend>Custom fields and tags</legend>
    {error && <p role="alert" className="form-error">{error}</p>}
    <div className="form-grid">
      {fields.map(f => <div key={f.id}><label htmlFor={`nf-${kind}-${f.id}`}>{f.label}</label>
        {f.field_type === "yesno"
          ? <select id={`nf-${kind}-${f.id}`} value={value.custom[f.id] ?? ""} onChange={e => onChange({ ...value, custom: { ...value.custom, [f.id]: e.target.value } })}><option value="">Not set</option><option value="yes">Yes</option><option value="no">No</option></select>
          : <input id={`nf-${kind}-${f.id}`} type={f.field_type === "date" ? "date" : "text"} inputMode={f.field_type === "number" ? "decimal" : undefined} maxLength={500} value={value.custom[f.id] ?? ""} onChange={e => onChange({ ...value, custom: { ...value.custom, [f.id]: e.target.value } })} />}</div>)}
      {shown.map(t => <div key={t.id}><label htmlFor={`nt-${kind}-${t.id}`}>{t.name}</label>
        <select id={`nt-${kind}-${t.id}`} value={value.tags[t.id] ?? ""} onChange={e => onChange({ ...value, tags: { ...value.tags, [t.id]: e.target.value } })}><option value="">None</option>{t.options.map(o => <option key={o} value={o}>{o}</option>)}</select></div>)}
    </div></fieldset>;
}

// Chosen custom field and tag values, dropping blanks. Sent with the create request so they save in the same transaction.
export function extrasPayload(v: ExtrasValue): { custom: Record<string, string>; tags: Record<string, string> } {
  const custom = Object.fromEntries(Object.entries(v.custom).filter(([, x]) => x !== ""));
  const tags = Object.fromEntries(Object.entries(v.tags).filter(([, x]) => x !== ""));
  return { custom, tags };
}

// Writes the chosen values to an existing record (used by records that do not take extras on create).
export async function saveExtras(kind: string, id: number, orgId: number, v: ExtrasValue): Promise<string> {
  const { custom, tags } = extrasPayload(v);
  if (!Object.keys(custom).length && !Object.keys(tags).length) return "";
  let last: ApiError | undefined;
  for (let attempt = 0; attempt < 3; attempt++) {
    try { await api(`/v1/entities/${kind}/${id}/extras`, { method: "PUT", body: JSON.stringify({ org_id: orgId, custom, tags }) }); return ""; }
    catch (e) {
      last = e as ApiError;
      if (last.status >= 400 && last.status < 500) break;
      await new Promise(r => setTimeout(r, 400 * (attempt + 1)));
    }
  }
  return `Saved, but custom fields were not: ${messageFor(last?.error ?? "", last?.status ?? 0)}. Add them from the details.`;
}
