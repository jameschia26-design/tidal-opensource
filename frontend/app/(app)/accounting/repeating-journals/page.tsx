"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";

type Account = { id: number; code: string; name: string };
type Line = { description: string; account_id: string; debit: string; credit: string };
type Profile = { id: number; narration: string; repeat_every: number; repeat_unit: string; next_run: string; runs_done: number; total_cents: number; active: boolean };
const blank = (): Line => ({ description: "", account_id: "", debit: "", credit: "" });
const today = todayISO;

export default function RepeatingJournals() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} />;
}

function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const [accounts, setAccounts] = useState<Account[]>([]); const [profiles, setProfiles] = useState<Profile[]>([]);
  const [loaded, setLoaded] = useState(false); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const [start, setStart] = useState(today); const [ends, setEnds] = useState(""); const [every, setEvery] = useState("1"); const [unit, setUnit] = useState("month"); const [narration, setNarration] = useState(""); const [lines, setLines] = useState<Line[]>([blank(), blank()]); const [asOf, setAsOf] = useState(today);
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);
  async function reload(id: number) {
    const [a, p] = await Promise.all([api<{ accounts: Account[] }>(`/v1/accounts?org_id=${id}`), api<{ profiles: Profile[] }>(`/v1/repeating-journals?org_id=${id}`)]);
    setAccounts(a.accounts); setProfiles(p.profiles); setLoaded(true);
  }
  useEffect(() => { if (orgId == null) return; let live = true; reload(orgId).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); }); return () => { live = false; }; }, [orgId]);
  const amounts = lines.map(l => ({ description: l.description, account_id: Number(l.account_id), debit_cents: l.debit ? parseCents(l.debit) : 0, credit_cents: l.credit ? parseCents(l.credit) : 0 }));
  const debit = amounts.reduce((n, l) => n + (l.debit_cents ?? 0), 0); const credit = amounts.reduce((n, l) => n + (l.credit_cents ?? 0), 0);
  const balanced = debit > 0 && debit === credit && amounts.every(l => l.account_id > 0 && l.debit_cents !== null && l.credit_cents !== null && ((l.debit_cents > 0) !== (l.credit_cents > 0)));
  async function action(fn: () => Promise<void>) { setBusy(true); setError(""); setNotice(""); try { await fn(); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
  async function save(e: FormEvent) {
    e.preventDefault(); if (!orgId || !balanced || busy) return;
    await action(async () => { await api("/v1/repeating-journals", { method: "POST", body: JSON.stringify({ org_id: orgId, narration, repeat_every: Number(every), repeat_unit: unit, start_date: start, ends_on: ends, lines: amounts }) }); setNarration(""); setLines([blank(), blank()]); setNotice("Repeating journal saved. Run it when due to post journals."); await reload(orgId); });
  }
  return <>
    <PageHeader title="Repeating journals" description="Create a balanced schedule. Journals post only when you run them; nothing runs automatically." actions={<a className="button-secondary" href="/accounting?tab=journals">Manual journals</a>} />
    {!selected && <p className="notice">Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && !loaded && !error && <p role="status">Loading…</p>}
    {selected && loaded && canWrite && <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>New repeating journal</legend><p className="section-help">Only an accountant or bookkeeper should create journals. Tax per line is not supported.</p><div className="form-grid">
      <div><label htmlFor="repeat-start">First journal date</label><DateInput id="repeat-start" required value={start} onChange={e => setStart(e.target.value)}/></div>
      <div><label htmlFor="repeat-every">Repeat every</label><input id="repeat-every" type="number" min="1" max="12" required value={every} onChange={e => setEvery(e.target.value)} /></div>
      <div><label htmlFor="repeat-unit">Repeat unit</label><select id="repeat-unit" value={unit} onChange={e => setUnit(e.target.value)}><option value="week">Weeks</option><option value="month">Months</option><option value="year">Years</option></select></div>
      <div><label htmlFor="repeat-end">End date (optional)</label><DateInput id="repeat-end" min={start} value={ends} onChange={e => setEnds(e.target.value)}/></div>
      <div><label htmlFor="repeat-narration">Narration</label><input id="repeat-narration" required maxLength={500} value={narration} onChange={e => setNarration(e.target.value)} /></div>
    </div>{lines.map((line, i) => <fieldset key={i}><legend>Line {i + 1}</legend><div className="form-grid"><div><label htmlFor={`repeat-desc-${i}`}>Description</label><input id={`repeat-desc-${i}`} maxLength={500} value={line.description} onChange={e => setLines(lines.map((l, j) => j === i ? { ...l, description: e.target.value } : l))} /></div><div><label htmlFor={`repeat-account-${i}`}>Account</label><select id={`repeat-account-${i}`} required value={line.account_id} onChange={e => setLines(lines.map((l, j) => j === i ? { ...l, account_id: e.target.value } : l))}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select></div>{(["debit", "credit"] as const).map(side => <div key={side}><label htmlFor={`repeat-${side}-${i}`}>{side === "debit" ? "Debit" : "Credit"} (SGD)</label><input id={`repeat-${side}-${i}`} inputMode="decimal" pattern="[0-9]+(\.[0-9]{1,2})?" value={line[side]} onChange={e => setLines(lines.map((l, j) => j === i ? { ...l, [side]: e.target.value } : l))} /></div>)}</div>{lines.length > 2 && <button type="button" className="button-secondary" onClick={() => setLines(lines.filter((_, j) => j !== i))}>Remove line {i + 1}</button>}</fieldset>)}<button type="button" className="button-secondary" disabled={lines.length >= 200} onClick={() => setLines([...lines, blank()])}>Add line</button><p role="status">Debit {formatCents(debit)} · Credit {formatCents(credit)} · {balanced ? "Balanced" : "Not balanced"}</p></fieldset><div className="form-actions"><button disabled={busy || !balanced} type="submit">Save schedule</button><button type="button" className="button-secondary" disabled={busy} onClick={() => { setStart(today()); setEnds(""); setEvery("1"); setUnit("month"); setNarration(""); setLines([blank(), blank()]); setError(""); setNotice(""); }}>Cancel</button></div></form>}
    {selected && loaded && !canWrite && <p className="notice">Your role allows viewing only.</p>}
    {selected && loaded && canWrite && <section className="record-form"><h2>Post due journals</h2><p>Posts every active schedule due up to this date. Existing entries are not posted again.</p><label htmlFor="repeat-asof">Run through</label><DateInput id="repeat-asof" value={asOf} onChange={e => setAsOf(e.target.value)}/><button disabled={busy || !asOf} onClick={() => action(async () => { const r = await api<{ created_journal_ids: number[] }>("/v1/repeating-journals/run", { method: "POST", body: JSON.stringify({ org_id: orgId, as_of: asOf }) }); setNotice(`${r.created_journal_ids.length} journal(s) posted.`); await reload(orgId!); })}>Post due journals</button></section>}
    {selected && loaded && (profiles.length ? <div className="table-scroll"><table className="doc-table"><caption>Repeating journal schedules</caption><thead><tr><th>Narration</th><th>Repeat</th><th>Next date</th><th>Posted</th><th>Amount</th><th>Status</th><th>Action</th></tr></thead><tbody>{profiles.map(p => <tr key={p.id}><th scope="row">{p.narration}</th><td>Every {p.repeat_every} {p.repeat_unit}(s)</td><td>{p.next_run}</td><td>{p.runs_done}</td><td>{formatCents(p.total_cents)}</td><td>{p.active ? "Active" : "Stopped"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => action(async () => { await api("/v1/repeating-journals/active", { method: "POST", body: JSON.stringify({ org_id: orgId, id: p.id, active: !p.active }) }); await reload(orgId!); })}>{p.active ? "Stop" : "Resume"}</button>}</td></tr>)}</tbody></table></div> : <p className="notice">No repeating journals yet.</p>)}
  </>;
}
