"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

// Per-line tracking selectors. Allows two active categories, each with named options, chosen on
// every document line; a line can carry one option from each category. Values are sent as
// {"<category_id>": <option_id>} and are validated again on the server.
export type TrackingOption = { id: number; name: string; archived: boolean };
export type TrackingCategory = { id: number; name: string; active: boolean; options: TrackingOption[] };
export type TrackingValue = Record<string, number>;

// One request per company; every line and every form on the page shares it.
const cache = new Map<number, Promise<TrackingCategory[]>>();
export function loadTrackingCategories(orgId: number): Promise<TrackingCategory[]> {
  let p = cache.get(orgId);
  if (!p) {
    p = api<{ categories: TrackingCategory[] }>(`/v1/tracking/categories?org_id=${orgId}`).then(r => r.categories);
    cache.set(orgId, p);
    p.catch(() => cache.delete(orgId));
  }
  return p;
}
export function clearTrackingCache() { cache.clear(); }

export function useTrackingCategories(orgId: number | null) {
  const [categories, setCategories] = useState<TrackingCategory[]>([]);
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    loadTrackingCategories(orgId).then(c => { if (live) setCategories(c); }).catch(() => { /* no categories: the line shows no selector */ });
    return () => { live = false; };
  }, [orgId]);
  return categories;
}

// Selectors for one line: one real labelled select per active category. Archived options stay in the
// list when the line already carries them, so history is never silently cleared on an edit.
export function TrackingFields({ orgId, idPrefix, lineNumber, value, onChange, disabled }: {
  orgId: number | null; idPrefix: string; lineNumber: number; value: TrackingValue; onChange: (v: TrackingValue) => void; disabled?: boolean;
}) {
  const categories = useTrackingCategories(orgId);
  const active = categories.filter(c => c.active);
  if (active.length === 0) return null;
  return <>
    {active.map(c => {
      const chosen = value[String(c.id)] ?? 0;
      const options = c.options.filter(o => !o.archived || o.id === chosen);
      return <div key={c.id}>
        <label htmlFor={`${idPrefix}-tracking-${c.id}-${lineNumber}`}>{c.name} <span className="field-optional">Optional</span></label>
        <select id={`${idPrefix}-tracking-${c.id}-${lineNumber}`} disabled={disabled} value={chosen || ""}
          onChange={e => {
            const next = { ...value };
            if (e.target.value === "") delete next[String(c.id)];
            else next[String(c.id)] = Number(e.target.value);
            onChange(next);
          }}>
          <option value="">No {c.name.toLowerCase()}</option>
          {options.map(o => <option key={o.id} value={o.id}>{o.name}{o.archived ? " (archived)" : ""}</option>)}
        </select>
      </div>;
    })}
  </>;
}

// Category picker for report columns: one category at a time.
export function TrackingCategoryPicker({ orgId, id, value, onChange, disabled }: {
  orgId: number | null; id: string; value: string; onChange: (v: string) => void; disabled?: boolean;
}) {
  const categories = useTrackingCategories(orgId);
  const active = categories.filter(c => c.active);
  if (active.length === 0) return null;
  return <div>
    <label htmlFor={id}>Tracking category</label>
    <select id={id} disabled={disabled} value={value} onChange={e => onChange(e.target.value)}>
      <option value="">None</option>
      {active.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
    </select>
  </div>;
}

// Turns a line's stored {category: option} map into the request shape, dropping empty entries.
export function trackingPayload(value: TrackingValue | undefined): TrackingValue | undefined {
  if (!value) return undefined;
  const kept = Object.fromEntries(Object.entries(value).filter(([, v]) => Number(v) > 0));
  return Object.keys(kept).length ? kept : undefined;
}
