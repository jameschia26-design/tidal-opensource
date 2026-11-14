"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import PageHeader from "@/components/PageHeader";
import { api } from "@/lib/api";

export default function Profile() {
  const [me, setMe] = useState<{ name: string; email: string } | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { api<{ name: string; email: string }>("/v1/me").then(m => { setMe(m); setName(m.name); }).catch(() => setError("Could not load your profile. Reload to try again.")); }, []);
  async function save(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(""); setMessage("");
    const value = name.trim();
    if (!value || Array.from(value).length > 100 || /[\u0000-\u001f\u007f-\u009f]/u.test(value)) { setError("Enter a name of 1–100 characters without control characters."); return; }
    setBusy(true);
    try {
      const result = await api<{ name: string }>("/v1/me", { method: "PATCH", body: JSON.stringify({ name: value }) });
      setName(result.name); setMe(current => current && { ...current, name: result.name }); setMessage("Display name saved.");
    } catch { setError("Could not save your display name. Try again."); }
    finally { setBusy(false); }
  }
  return <>
    <PageHeader title="Profile" description="Your personal details, separate from company settings." />
    {error && <p role="alert">{error}</p>}
    {message && <p role="status">{message}</p>}
    {!me ? !error && <p role="status">Loading profile…</p> : <>
      <form onSubmit={save}>
        <label htmlFor="display-name">Display name</label>
        <input id="display-name" value={name} onChange={e => setName(e.target.value)} required disabled={busy} aria-describedby="name-help" />
        <p id="name-help">1–100 characters.</p>
        <button disabled={busy}>{busy ? "Saving…" : "Save display name"}</button>
      </form>
      <dl><dt>Email</dt><dd>{me.email}</dd></dl>
      <p>Email changes are not supported because your verified email identifies and links your account. Your Google account is separate; changing your display name here does not change it.</p>
    </>}
    <p><Link href="/settings/account">Account and sign-in</Link> · <Link href="/settings">Company settings</Link></p>
  </>;
}
