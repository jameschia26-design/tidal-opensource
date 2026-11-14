"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type SavedReport = { id: number; title: string; status: string };
type Blank = { title: string; columns: string[]; rows: string[][] };

// A blank report is a saved shell the user fills in: named columns and free-text rows. It is stored
// server-side with the other saved reports, so it survives a browser change and is shared with the company.
export default function BlankReportPage() {
  const { selected } = useOrg();
  const orgId = selected?.id ?? null;
  const writable = selected != null && selected.role !== "viewer";
  const [saved, setSaved] = useState<SavedReport[]>([]);
  const [title, setTitle] = useState("");
  const [columns, setColumns] = useState("Description,Amount");
  const [rows, setRows] = useState("Example line,0.00");
  const [report, setReport] = useState<Blank | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<{ saved_reports: SavedReport[] }>(`/v1/saved-reports?org_id=${orgId}&report_key=blank`)
      .then(r => { if (live) setSaved(r.saved_reports); })
      .catch(() => { /* an empty list is fine */ });
    return () => { live = false; };
  }, [orgId]);

  async function save() {
    if (orgId == null || !writable) return;
    setError(""); setNotice("");
    const cols = columns.split(",").map(c => c.trim()).filter(Boolean);
    const lines = rows.split("\n").map(line => line.split(",").map(v => v.trim()));
    if (!title.trim() || !cols.length || lines.some(l => l.length !== cols.length)) {
      setError("Give the report a title, at least one column, and one value per column on every row.");
      return;
    }
    try {
      const created = await api<{ id: number }>("/v1/saved-reports", { method: "POST", body: JSON.stringify({ org_id: orgId, title: title.trim(), report_key: "blank", layout: { columns: cols, rows: lines } }) });
      setSaved(prev => [...prev, { id: created.id, title: title.trim(), status: "draft" }]);
      setNotice("Blank report saved. Publish it with the report controls when it is ready.");
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }

  async function open(id: string) {
    if (orgId == null || !id) return;
    try { setReport(await api<Blank>(`/v1/reports/blank?org_id=${orgId}&saved_id=${id}`)); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }

  return <>
    <PageHeader title="Blank report" description="A saved layout of named columns and rows you fill in yourself." />
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {selected && <>
      {writable && <form className="record-form" onSubmit={e => { e.preventDefault(); void save(); }}>
        <fieldset><legend>New blank report</legend><div className="form-grid">
          <div><label htmlFor="blank-title">Title</label><input id="blank-title" required maxLength={100} value={title} onChange={e => setTitle(e.target.value)} /></div>
          <div><label htmlFor="blank-columns">Columns (comma separated)</label><input id="blank-columns" required value={columns} onChange={e => setColumns(e.target.value)} /></div>
        </div>
        <label htmlFor="blank-rows">Rows (one per line, comma separated)</label><textarea id="blank-rows" required rows={5} value={rows} onChange={e => setRows(e.target.value)} />
        <p className="form-actions"><button disabled={!title.trim()}>Save blank report</button></p></fieldset>
      </form>}
      <div className="record-toolbar"><label>Open saved<select id="blank-open" value="" onChange={e => void open(e.target.value)}><option value="">Choose…</option>{saved.map(s => <option key={s.id} value={s.id}>{s.title}</option>)}</select></label></div>
      {report && <div className="table-scroll"><table className="doc-table"><caption>{report.title}</caption><thead><tr>{report.columns.map((c, i) => <th key={i}>{c}</th>)}</tr></thead><tbody>{report.rows.map((row, i) => <tr key={i}>{row.map((v, j) => <td key={j}>{v}</td>)}</tr>)}</tbody></table></div>}
    </>}
  </>;
}
