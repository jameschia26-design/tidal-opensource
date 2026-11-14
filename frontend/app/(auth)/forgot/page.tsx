"use client";
import { useState } from "react";
import Link from "next/link";
import { api, type ApiError } from "@/lib/api";
import AuthShell from "@/components/AuthShell";

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api("/v1/auth/password/forgot", { method: "POST", body: JSON.stringify({ email }) });
      setSent(true);
    } catch (err) {
      const e2 = err as ApiError;
      setError(e2.error === "backend_unavailable" || e2.status === 0
        ? "Password reset is unavailable right now. Try again shortly."
        : "We could not start a password reset. Check the address and try again.");
    } finally {
      setBusy(false);
    }
  }

  if (sent) {
    return (
      <AuthShell title="Check your email" subtitle="If that address has an account, we sent a 6-digit reset code.">
        <p className="muted">The code expires in 10 minutes and works once.</p>
        <Link href={`/reset?email=${encodeURIComponent(email)}`} className="button-secondary">Enter the code</Link>
        <p className="muted">Remembered it? <Link href="/login">Sign in</Link></p>
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Reset your password" subtitle="We will email a one-time code if the address has an account.">
      <form onSubmit={submit} noValidate>
        <label htmlFor="email">Work email</label>
        <input id="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? "Sending…" : "Send reset code"}</button>
      </form>
      <p className="muted">Remembered it? <Link href="/login">Sign in</Link></p>
    </AuthShell>
  );
}
