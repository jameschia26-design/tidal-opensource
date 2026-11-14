"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";

type Project = { id: number; contact_id: number; name: string; active: boolean; estimated_cost_cents: number; estimated_charge_cents: number };
type Contact = { id: number; name: string };

export default function ProjectsPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Project[] | null>(null);
  const [customers, setCustomers] = useState<Contact[]>([]);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [estCost, setEstCost] = useState("");
  const [estCharge, setEstCharge] = useState("");
  const [customer, setCustomer] = useState("");
  const [editing, setEditing] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [picked, setPicked] = useState("");
  const [tasks, setTasks] = useState<{ id: number; name: string; rate_cents: number; active: boolean }[]>([]);
  const [taskName, setTaskName] = useState("");
  const [taskRate, setTaskRate] = useState("");
  const [people, setPeople] = useState<{ user_id: number; name: string; role: string }[]>([]);
  const [staff, setStaff] = useState<{ id: number; email: string; role: string }[]>([]);
  const [pickUser, setPickUser] = useState("");
  const [pickRole, setPickRole] = useState("member");
  async function load() {
    if (orgId == null) return;
    const [p, c] = await Promise.all([api<{ projects: Project[] }>(`/v1/projects?org_id=${orgId}`), api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`)]);
    if (live.current) { setRows(p.projects); setCustomers(c.contacts); }
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  async function call(path: string, body: object, done: string) {
    if (busy || orgId == null) return false;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) });
      if (!live.current) return false;
      setNotice(done); await load(); return true;
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } return false; }
    finally { if (live.current) setBusy(false); }
  }
  function estimates() {
    const cost = estCost.trim() === "" ? 0 : parseCents(estCost);
    const charge = estCharge.trim() === "" ? 0 : parseCents(estCharge);
    if (cost === null || charge === null) { setError("Enter valid non-negative SGD amounts with up to two decimal places."); return null; }
    return { estimated_cost_cents: cost, estimated_charge_cents: charge };
  }
  async function create(e: FormEvent) {
    e.preventDefault();
    const amounts = estimates();
    if (!name.trim() || !customer) { setError("Enter a project name and choose a customer."); return; }
    if (!amounts) return;
    if (await call("/v1/projects", { contact_id: Number(customer), name: name.trim(), ...amounts }, "Project created.")) { setOpen(false); setName(""); setCustomer(""); setEstCost(""); setEstCharge(""); }
  }
  async function saveEstimates(e: FormEvent, id: number) {
    e.preventDefault();
    const amounts = estimates();
    if (!amounts) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/projects/${id}`, { method: "PATCH", body: JSON.stringify({ org_id: orgId, ...amounts }) });
      setEditing(null); setNotice("Project estimates saved."); await load();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  async function openProject(id: string) {
    setPicked(id);
    if (orgId == null || !id) { setTasks([]); setPeople([]); return; }
    try {
      const [t, m, u] = await Promise.all([
        api<{ tasks: typeof tasks }>(`/v1/projects/${id}/tasks?org_id=${orgId}`),
        api<{ members: typeof people }>(`/v1/projects/${id}/members?org_id=${orgId}`),
        api<{ members: typeof staff }>(`/v1/members?org_id=${orgId}`),
      ]);
      setTasks(t.tasks); setPeople(m.members); setStaff(u.members);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }
  async function addTask(e: FormEvent) {
    e.preventDefault();
    if (orgId == null || !picked || !taskName.trim()) return;
    try {
      await api(`/v1/projects/${picked}/tasks`, { method: "POST", body: JSON.stringify({ org_id: orgId, name: taskName.trim(), rate_cents: Number(taskRate || 0) * 100 }) });
      setTaskName(""); setTaskRate(""); await openProject(picked); setNotice("Task added.");
    } catch (e) { const err = e as ApiError; setError(err.error === "task_exists" ? "That task already exists on this project." : messageFor(err.error, err.status)); }
  }
  async function toggleTask(t: { id: number; name: string; active: boolean; rate_cents: number }) {
    if (orgId == null || !picked) return;
    try { await api(`/v1/project-tasks/${t.id}`, { method: "PATCH", body: JSON.stringify({ org_id: orgId, name: t.name, rate_cents: t.rate_cents, active: !t.active }) }); await openProject(picked); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }
  async function addPerson(e: FormEvent) {
    e.preventDefault();
    if (orgId == null || !picked || !pickUser) return;
    try { await api(`/v1/projects/${picked}/members`, { method: "POST", body: JSON.stringify({ org_id: orgId, user_id: Number(pickUser), role: pickRole }) }); setPickUser(""); await openProject(picked); setNotice("Person added to the project."); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }
  async function removePerson(userId: number) {
    if (orgId == null || !picked) return;
    try { await api(`/v1/projects/${picked}/members?org_id=${orgId}&user_id=${userId}`, { method: "DELETE" }); await openProject(picked); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }
  const who = (id: number) => customers.find(c => c.id === id)?.name ?? `Customer ${id}`;
  return <>
    <PageHeader title="Projects" description="Group a customer's quotes, invoices and billable expenses." actions={canWrite ? <button disabled={busy || open} onClick={() => setOpen(true)}>New project</button> : undefined} />
    <p className="notice">Budgets are not built. Each project keeps its own task catalogue and people, and chargeable time is reported on the time page.</p>
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {open && <form className="record-form" onSubmit={e => void create(e)}>
      <h2>New project</h2>
      <fieldset disabled={busy}><legend>Project</legend><div className="form-grid">
        <div><label htmlFor="project-name">Project name</label><input id="project-name" required maxLength={200} value={name} onChange={e => setName(e.target.value)} /></div>
        <div><label htmlFor="project-est-cost">Estimated cost (SGD)</label><input id="project-est-cost" inputMode="decimal" value={estCost} onChange={e => setEstCost(e.target.value)} aria-describedby="project-estimate-help" /></div><div><label htmlFor="project-est-charge">Estimated charge (SGD)</label><input id="project-est-charge" inputMode="decimal" value={estCharge} onChange={e => setEstCharge(e.target.value)} aria-describedby="project-estimate-help" /></div><div><label htmlFor="project-customer">Customer</label><select id="project-customer" required value={customer} onChange={e => setCustomer(e.target.value)}><option value="">Choose customer</option>{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
      </div><p id="project-estimate-help" className="section-help">Optional estimates; enter zero or more. Maximum two decimal places.</p></fieldset>
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save project"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {rows?.length === 0 && <p>No projects yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>All projects</caption><thead><tr><th>Project</th><th>Customer</th><th>Estimate cost / charge</th><th>Status</th><th>Action</th></tr></thead><tbody>{rows.map(p => <tr key={p.id}><td>{p.name}</td><td>{who(p.contact_id)}</td><td>{editing === p.id ? <form className="form-actions" onSubmit={e => void saveEstimates(e, p.id)}><label>Estimated cost (SGD)<input aria-label={`Estimated cost for ${p.name}`} inputMode="decimal" value={estCost} onChange={e => setEstCost(e.target.value)} /></label><label>Estimated charge (SGD)<input aria-label={`Estimated charge for ${p.name}`} inputMode="decimal" value={estCharge} onChange={e => setEstCharge(e.target.value)} /></label><button disabled={busy}>Save estimates</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setEditing(null)}>Cancel</button></form> : <>{formatCents(p.estimated_cost_cents)} / {formatCents(p.estimated_charge_cents)}{canWrite && <button type="button" className="button-secondary" disabled={busy || editing !== null} onClick={() => { setEstCost((p.estimated_cost_cents / 100).toFixed(2)); setEstCharge((p.estimated_charge_cents / 100).toFixed(2)); setEditing(p.id); }}>Edit estimates<span className="sr-only"> for {p.name}</span></button>}</>}</td><td>{p.active ? "Active" : "Inactive"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void call(`/v1/projects/${p.id}/active`, { active: !p.active }, p.active ? "Project switched off." : "Project switched on.")}>{p.active ? "Switch off" : "Switch on"}<span className="sr-only"> {p.name}</span></button>}</td></tr>)}</tbody></table></div>}
    {!!rows?.length && <section className="record-form" aria-label="Project tasks and people">
      <h2>Tasks and people</h2>
      <div className="record-toolbar"><label>Project<select id="project-pick" value={picked} onChange={e => void openProject(e.target.value)}><option value="">Choose a project</option>{rows.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label></div>
      {picked && canWrite && <form onSubmit={addTask}><fieldset disabled={busy}><legend>Add a task</legend><div className="form-grid">
        <div><label htmlFor="project-task">Task name</label><input id="project-task" required maxLength={200} value={taskName} onChange={e => setTaskName(e.target.value)} /></div>
        <div><label htmlFor="project-task-rate">Rate per hour (SGD)</label><input id="project-task-rate" inputMode="decimal" value={taskRate} onChange={e => setTaskRate(e.target.value)} /></div>
      </div><p className="form-actions"><button disabled={busy || !taskName.trim()}>Add task</button></p></fieldset></form>}
      {picked && (tasks.length === 0 ? <p className="muted">No tasks on this project yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>Task catalogue</caption><thead><tr><th>Task</th><th className="num">Rate</th><th>Status</th><th>Action</th></tr></thead><tbody>{tasks.map(t => <tr key={t.id}><th scope="row">{t.name}</th><td className="num">${(t.rate_cents / 100).toFixed(2)}</td><td>{t.active ? "Active" : "Inactive"}</td><td>{canWrite && <button type="button" className="button-secondary" disabled={busy} onClick={() => void toggleTask(t)}>{t.active ? "Switch off" : "Switch on"}</button>}</td></tr>)}</tbody></table></div>)}
      {picked && canWrite && <form onSubmit={addPerson}><fieldset disabled={busy}><legend>Add a person</legend><div className="form-grid">
        <div><label htmlFor="project-person">Person</label><select id="project-person" required value={pickUser} onChange={e => setPickUser(e.target.value)}><option value="">Choose person</option>{staff.map(u => <option key={u.id} value={u.id}>{u.email}</option>)}</select></div>
        <div><label htmlFor="project-role">Project role</label><select id="project-role" value={pickRole} onChange={e => setPickRole(e.target.value)}><option value="member">Member</option><option value="lead">Lead</option></select></div>
      </div><p className="form-actions"><button disabled={busy || !pickUser}>Add person</button></p></fieldset></form>}
      {picked && (people.length === 0 ? <p className="muted">Nobody is assigned to this project yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>People on this project</caption><thead><tr><th>Person</th><th>Project role</th><th>Action</th></tr></thead><tbody>{people.map(p => <tr key={p.user_id}><th scope="row">{p.name}</th><td>{p.role}</td><td>{canWrite && <button type="button" className="button-secondary" disabled={busy} onClick={() => void removePerson(p.user_id)}>Remove</button>}</td></tr>)}</tbody></table></div>)}
    </section>}
  </>;
}
