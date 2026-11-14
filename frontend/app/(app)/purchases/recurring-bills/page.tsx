"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { todayISO } from "@/lib/dates";

type Profile = { id: number; name: string; repeat_every: number; repeat_unit: string; start_date: string; ends_on: string; next_run: string; active: boolean; generated_count: number; mode: string; auto_generate: boolean };
const today = todayISO;

export default function RecurringBillsPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Profile[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function load() {
    if (orgId == null) return;
    const p = await api<{ profiles: Profile[] }>(`/v1/recurring-bills?org_id=${orgId}`);
    if (live.current) setRows(p.profiles);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  async function call<T>(path: string, body: object, done: (r: T) => string) {
    if (busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<T>(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) });
      if (!live.current) return;
      setNotice(done(r)); await load();
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  return <>
    <PageHeader title="Recurring bills" description="Each due date creates a bill: saved as a draft, or approved and posted, depending on the profile." />
    <p className="notice">Create a profile from <Link href="/purchases">Bills</Link> with New bill, then Make recurring. Approve-and-send is not offered: Tidal sends no email.</p>
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {canWrite && <div className="form-actions"><button disabled={busy} onClick={() => void call<{ created_bill_ids: number[] }>("/v1/recurring-bills/run", { as_of: today() }, r => `${r.created_bill_ids.length} draft bill(s) created.`)}>Run due bills</button></div>}
    {rows?.length === 0 && <p>No recurring bills yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Recurring bill profiles</caption><thead><tr><th scope="col">Profile</th><th scope="col">Repeats</th><th scope="col">On due date</th><th scope="col">Automatic</th><th scope="col">Next due</th><th scope="col">Ends</th><th scope="col" className="num">Bills created</th><th scope="col">Status</th><th scope="col">Action</th></tr></thead><tbody>{rows.map(p => <tr key={p.id}><td>{p.name}</td><td>Every {p.repeat_every} {p.repeat_unit}{p.repeat_every > 1 ? "s" : ""}</td><td>{p.mode === "approve" ? "Approve and post" : "Save as draft"}</td><td>{p.auto_generate ? "Yes" : "No"}</td><td>{p.active ? p.next_run : "—"}</td><td>{p.ends_on || "Never"}</td><td>{p.generated_count}</td><td>{p.active ? "Active" : "Stopped"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void call<{ active: boolean }>("/v1/recurring-bills/active", { id: p.id, active: !p.active }, r => r.active ? "Profile resumed." : "Profile stopped.")}>{p.active ? "Stop" : "Resume"}<span className="sr-only"> {p.name}</span></button>}</td></tr>)}</tbody></table></div>}
  </>;
}
