"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, type ApiError } from "@/lib/api";
import AuthShell from "@/components/AuthShell";

function ResetForm() {
  const router = useRouter();
  const initial = useSearchParams().get("email") ?? "";
  const [email, setEmail] = useState(initial);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError("The two passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      await api("/v1/auth/password/reset", { method: "POST", body: JSON.stringify({ email, code, new_password: password }) });
      router.push("/login");
    } catch (err) {
      const e2 = err as ApiError;
      setError(e2.status === 429
        ? "Too many attempts. Wait a few minutes and request a new code."
        : e2.status === 410
          ? "That code has expired. Request a new one."
          : "That code is not valid, or the address is wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Choose a new password" subtitle={email ? `Using the code sent to ${email}.` : "Enter the code from your email."}>
      <form onSubmit={submit} noValidate>
        <label htmlFor="email">Work email</label>
        <input id="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <label htmlFor="code">Reset code</label>
        <input id="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
        <label htmlFor="password">New password (at least 12 characters)</label>
        <input id="password" type="password" autoComplete="new-password" minLength={12} maxLength={128} required value={password} onChange={(e) => setPassword(e.target.value)} />
        <label htmlFor="confirm">Confirm new password</label>
        <input id="confirm" type="password" autoComplete="new-password" minLength={12} maxLength={128} required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy || code.length !== 6}>{busy ? "Resetting…" : "Reset password"}</button>
      </form>
      <p className="muted">No code? <Link href="/forgot">Request one</Link></p>
    </AuthShell>
  );
}

export default function ResetPasswordPage() {
  return <Suspense fallback={null}><ResetForm /></Suspense>;
}
