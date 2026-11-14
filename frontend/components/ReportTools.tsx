"use client";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { csvCell } from "@/lib/csv";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { useOrg } from "@/components/OrgProvider";
import { reportDateRange, type ReportDatePreset } from "@/lib/report-presets";

type SavedReport = { id: number; title: string; report_key: string; status: "draft" | "published" | "archived"; params: Record<string, string>; favourite: boolean };
const reportKey = (title: string) => title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 60);

// Every report gets the same controls: print, CSV of what is on screen, column show/hide, and
// grouping. They read the rendered tables instead of duplicating each report's data shape, so a
// report never needs its own export code and the CSV always matches the visible rows.
export default function ReportTools({ title, orgId, children }: { title: string; orgId: number | null; children: ReactNode }) {
  const { selected } = useOrg();
  const orgFiscalYearEnd = selected?.id === orgId ? selected.fiscal_year_end ?? "12-31" : "12-31";
  const body = useRef<HTMLDivElement>(null);
  const [cols, setCols] = useState<string[] | null>(null);
  const [hidden, setHidden] = useState<Set<number>>(new Set());
  const [groupHeads, setGroupHeads] = useState<string[] | null>(null);
  const [groupCol, setGroupCol] = useState<number | null>(null);
  const originals = useRef<Map<HTMLTableSectionElement, string>>(new Map());
  const [note, setNote] = useState("");
  const [presetName, setPresetName] = useState("");
  const [presets, setPresets] = useState<SavedReport[]>([]);
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<{ saved_reports: SavedReport[] }>(`/v1/saved-reports?org_id=${orgId}&report_key=${reportKey(title)}`)
      .then(r => { if (live) setPresets(r.saved_reports); })
      .catch(() => { if (live) setPresets([]); });
    return () => { live = false; };
  }, [orgId, title]);

  function reportValues() {
    const form = body.current?.querySelector("form");
    if (!form) return null;
    const values = Object.fromEntries(Array.from(form.elements).filter((field): field is HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement => field instanceof HTMLInputElement || field instanceof HTMLSelectElement || field instanceof HTMLTextAreaElement).filter(field => field.id).map(field => [field.id, field.value]));
    return Object.keys(values).length ? values : null;
  }
  async function savePreset() {
    const values = reportValues();
    if (orgId == null || !values) { setNote("Run or set report filters before saving."); return; }
    if (!presetName.trim()) { setNote("Enter a preset name."); return; }
    try {
      const saved = await api<{ id: number }>("/v1/saved-reports", { method: "POST", body: JSON.stringify({ org_id: orgId, title: presetName.trim(), report_key: reportKey(title), params: values }) });
      setPresets(prev => [...prev, { id: saved.id, title: presetName.trim(), report_key: reportKey(title), status: "draft", params: values, favourite: false }]);
      setPresetName(""); setNote("Saved report stored for this company. Publish it to keep it after drafts are cleared.");
    } catch (e) { const err = e as ApiError; setNote(messageFor(err.error, err.status)); }
  }
  async function setPresetStatus(id: number, status: "published" | "archived" | "draft") {
    if (orgId == null) return;
    const preset = presets.find(p => p.id === id);
    if (!preset) return;
    try {
      await api(`/v1/saved-reports/${id}`, { method: "PUT", body: JSON.stringify({ org_id: orgId, title: preset.title, report_key: preset.report_key, status, params: preset.params }) });
      setPresets(prev => prev.map(p => p.id === id ? { ...p, status } : p));
      setNote(`Saved report ${status}.`);
    } catch (e) { const err = e as ApiError; setNote(messageFor(err.error, err.status)); }
  }
  async function toggleFavourite(preset: SavedReport) {
    if (orgId == null) return;
    const favourite = !preset.favourite;
    try {
      await api(`/v1/saved-reports/${preset.id}`, { method: "PUT", body: JSON.stringify({ org_id: orgId, title: preset.title, report_key: preset.report_key, status: preset.status, params: preset.params, favourite }) });
      setPresets(prev => prev.map(p => p.id === preset.id ? { ...p, favourite } : p));
      setNote(favourite ? "Report added to favourites." : "Report removed from favourites.");
    } catch (e) { const err = e as ApiError; setNote(messageFor(err.error, err.status)); }
  }

  function applyPreset(id: string) {
    const form = body.current?.querySelector("form"), values = presets.find(p => String(p.id) === id)?.params;
    if (!form || !values) return;
    for (const [key, value] of Object.entries(values)) {
      const field = form.querySelector(`#${CSS.escape(key)}`);
      if (field instanceof HTMLInputElement || field instanceof HTMLSelectElement || field instanceof HTMLTextAreaElement) {
        const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(field), "value")?.set;
        setter?.call(field, value); field.dispatchEvent(new Event("input", { bubbles: true })); field.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
    setNote(`Loaded preset: ${presets.find(p => String(p.id) === id)?.title ?? "saved report"}. Select Update to run report.`);
  }

  // Shared date presets: a report never needs its own preset list. Presets set the report's
  // date fields and fire the same events a person typing would, so the report's own logic runs.
  function presetDates(value: string) {
    const form = body.current?.querySelector("form");
    if (!form) { setNote("This report has no date filter."); return; }
    const preset = value as ReportDatePreset;
    if (!["this_month", "last_month", "month_to_date", "this_quarter", "last_quarter", "quarter_to_date", "ytd", "fytd", "this_fy", "last_fy", "last_year"].includes(preset)) { setNote("Choose a date preset."); return; }
    const { from, to } = reportDateRange(preset, new Date().toISOString().slice(0, 10), orgFiscalYearEnd);
    const fields = Array.from(form.querySelectorAll("input[type=date]")) as HTMLInputElement[];
    if (!fields.length) { setNote("This report has no date filter."); return; }
    const set = (field: HTMLInputElement, date: string) => {
      const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(field), "value")?.set;
      setter?.call(field, date); field.dispatchEvent(new Event("input", { bubbles: true })); field.dispatchEvent(new Event("change", { bubbles: true }));
    };
    set(fields[0], fields.length === 1 ? to : from);
    if (fields[1]) set(fields[1], to);
    setNote(`Dates set from ${from} to ${to}. Select Update or run the report if it does not refresh.`);
  }

  const tables = () => Array.from(body.current?.querySelectorAll("table") ?? []);

  // Some reports stack a summary table above their detail table (aging does). Controls act on the
  // table with the most body rows, so grouping and columns follow the data rather than the summary.
  function mainTable() {
    let best: HTMLTableElement | null = null;
    let bestRows = -1;
    for (const table of tables()) {
      const rows = Array.from(table.tBodies).reduce((n, section) => n + section.rows.length, 0);
      if (rows > bestRows) { best = table; bestRows = rows; }
    }
    return best;
  }

  function cells() {
    return tables().map(t => Array.from(t.querySelectorAll("tr")).map(tr =>
      Array.from(tr.querySelectorAll("th,td")).map(cell => (cell as HTMLElement).innerText.trim())));
  }

  function exportCsv() {
    const rows = cells();
    if (!rows.length) { setNote("Nothing to export yet."); return; }
    const lines: string[] = [];
    for (const table of rows) {
      for (const row of table) lines.push(row.map(csvCell).join(","));
      lines.push("");
    }
    const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `${title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "report"}.csv`;
    link.click();
    URL.revokeObjectURL(url);
    setNote("Exported the rows shown, with group subtotals.");
  }

  function openColumns() {
    const table = mainTable();
    if (!table) { setNote("Run the report first, then choose columns."); return; }
    const head = Array.from(table.querySelectorAll("thead th")).map(th => (th as HTMLElement).innerText.trim());
    setCols(head.length ? head : ["Column 1"]);
    setNote("");
  }

  function openGroups() {
    const table = mainTable();
    if (!table) { setNote("Run the report first, then choose grouping."); return; }
    setGroupHeads(Array.from(table.querySelectorAll("thead th")).map(th => (th as HTMLElement).innerText.trim()));
    setNote("");
  }

  function toggle(i: number) {
    setHidden(prev => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i); else next.add(i);
      const table = mainTable();
      if (!table) return next;
      for (const tr of Array.from(table.querySelectorAll("tr"))) {
        const cell = tr.querySelectorAll("th,td")[i] as HTMLElement | undefined;
        if (cell) cell.style.display = next.has(i) ? "none" : "";
      }
      return next;
    });
  }

  function num(text: string) {
    // Only pure numeric cells (amounts, counts) feed subtotals. Labels like "Invoice 6" contain
    // digits but are not amounts, so any letter disqualifies the cell.
    if (/[A-Za-z]/.test(text)) return null;
    const n = Number(text.replace(/[^0-9.-]/g, ""));
    return text.trim() !== "" && Number.isFinite(n) ? n : null;
  }

  // Grouping is a view over the rendered rows: each distinct value gets a band, its rows, and a
  // subtotal for numeric columns. Saved sections are restored when grouping changes.
  function applyGroup(index: number | null) {
    setGroupCol(index);
    for (const [section, html] of originals.current) section.innerHTML = html;
    if (index == null) { originals.current.clear(); return; }
    const table = mainTable();
    if (!table) { originals.current.clear(); return; }
    for (const section of Array.from(table.tBodies)) {
        if (!originals.current.has(section)) originals.current.set(section, section.innerHTML);
        const rows = Array.from(section.rows).filter(r => !r.classList.contains("group-band") && !r.classList.contains("group-total"));
        const foot = rows.filter(r => /^totals?\b/i.test((r.cells[0]?.innerText ?? "").trim()));
        const bodyRows = rows.filter(r => !foot.includes(r));
        const groups = new Map<string, HTMLTableRowElement[]>();
        for (const row of bodyRows) {
          const key = (row.cells[index]?.innerText ?? "").trim() || "(blank)";
          const list = groups.get(key) ?? [];
          list.push(row);
          groups.set(key, list);
        }
        const width = Math.max(1, table.querySelectorAll("thead th").length);
        section.replaceChildren();
        for (const [key, list] of groups) {
          const band = document.createElement("tr");
          band.className = "group-band";
          const bandCell = document.createElement("td");
          bandCell.colSpan = width;
          bandCell.textContent = `${key} (${list.length})`;
          band.append(bandCell);
          section.append(band, ...list);
          const sums: (number | null)[] = new Array(width).fill(null);
          for (const row of list) for (let i = 0; i < width; i++) {
            const v = num(row.cells[i]?.innerText ?? "");
            if (v != null && row.cells[i]) sums[i] = (sums[i] ?? 0) + v;
          }
          if (sums.some(v => v != null)) {
            const total = document.createElement("tr");
            total.className = "group-total";
            for (let i = 0; i < width; i++) {
              const cell = document.createElement("td");
              cell.textContent = i === 0 ? `Subtotal ${key}` : sums[i] == null ? "" : sums[i]!.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
              total.append(cell);
            }
            section.append(total);
          }
        }
        section.append(...foot);
    }
    setNote("Grouped the rows shown and added a subtotal for numeric columns.");
  }

  return (
    <section aria-label={title} className="report-tools">
      <div className="record-toolbar" role="group" aria-label="Report actions">
        <button type="button" className="button-secondary" onClick={exportCsv}>Export CSV</button>
        <button type="button" className="button-secondary" onClick={openColumns} aria-expanded={cols != null}>Columns</button>
        <button type="button" className="button-secondary" onClick={openGroups} aria-expanded={groupHeads != null}>Group</button>
        <button type="button" className="button-secondary" onClick={() => window.print()}>Print</button>
        <label>Date preset<select aria-label="Date preset" value="" onChange={e => presetDates(e.target.value)}><option value="">Choose…</option><option value="this_month">This month</option><option value="last_month">Last month</option><option value="month_to_date">Month to date</option><option value="this_quarter">This quarter</option><option value="last_quarter">Last quarter</option><option value="quarter_to_date">Quarter to date</option><option value="ytd">Calendar year to date</option><option value="this_fy">Financial year to date</option><option value="last_fy">Last financial year</option><option value="last_year">Last calendar year</option></select></label>
        <label>Saved report name<input value={presetName} maxLength={60} onChange={e => setPresetName(e.target.value)} /></label>
        <button type="button" className="button-secondary" onClick={savePreset}>Save report</button>
        {presets.length > 0 && <label>Load saved report<select value="" onChange={e => applyPreset(e.target.value)}><option value="">Choose…</option>{presets.filter(p => p.status !== "archived").map(p => <option key={p.id} value={p.id}>{p.title}{p.favourite ? " ★" : ""}</option>)}</select></label>}
        {presets.filter(p => p.status !== "archived").map(p => <button key={`fav-${p.id}`} type="button" className="button-secondary" aria-pressed={p.favourite} onClick={() => void toggleFavourite(p)}>{p.favourite ? "★" : "☆"}<span className="sr-only"> {p.favourite ? "Remove from" : "Add to"} favourites: {p.title}</span></button>)}
        {presets.some(p => p.status === "draft") && <span className="saved-report-actions">{presets.filter(p => p.status === "draft").map(p => <span key={p.id}>{p.title} <button type="button" className="button-secondary" onClick={() => void setPresetStatus(p.id, "published")}>Publish</button></span>)}</span>}
        {presets.some(p => p.status === "published") && <label>Published<select value="" onChange={e => { const p = presets.find(x => String(x.id) === e.target.value); if (p) void setPresetStatus(p.id, "archived"); }}><option value="">Archive…</option>{presets.filter(p => p.status === "published").map(p => <option key={p.id} value={p.id}>{p.title}</option>)}</select></label>}
      </div>
      {cols && <fieldset><legend>Columns to show</legend>{cols.map((c, i) => (
        <label key={i}><input type="checkbox" checked={!hidden.has(i)} onChange={() => toggle(i)} /> {c || `Column ${i + 1}`}</label>
      ))}</fieldset>}
      {groupHeads && <fieldset><legend>Group rows by</legend><label>Column<select value={groupCol ?? ""} onChange={e => applyGroup(e.target.value === "" ? null : Number(e.target.value))}><option value="">No grouping</option>{groupHeads.map((h, i) => <option key={i} value={i}>{h || `Column ${i + 1}`}</option>)}</select></label></fieldset>}
      {note && <p className="section-help" aria-live="polite">{note}</p>}
      <div ref={body}>{children}</div>
    </section>
  );
}
