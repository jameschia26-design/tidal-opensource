"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, type ApiError } from "@/lib/api";
import { authLink } from "@/lib/auth-destination";
import AuthShell from "@/components/AuthShell";

function SignupForm() {
  const router = useRouter();
  const next = useSearchParams().get("next");
  const [email, setEmail] = useState("");
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
      await api("/v1/auth/signup", { method: "POST", body: JSON.stringify({ email, password }) });
      router.push(authLink(`/verify?email=${encodeURIComponent(email)}`, next));
    } catch (err) {
      const e2 = err as ApiError;
      setError(e2.error === "backend_unavailable"
        ? "Sign-up service is unavailable right now. Try again shortly."
        : "We could not start sign-up. Check the details and try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Create your account" subtitle="Your company is set up after you sign in.">
      <form onSubmit={submit} noValidate>
        <label htmlFor="email">Work email</label>
        <input id="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <label htmlFor="password">Password (at least 12 characters)</label>
        <input id="password" type="password" autoComplete="new-password" minLength={12} maxLength={128} required value={password} onChange={(e) => setPassword(e.target.value)} />
        <label htmlFor="confirm">Confirm password</label>
        <input id="confirm" type="password" autoComplete="new-password" minLength={12} maxLength={128} required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        <p className="muted small">We email a 6-digit code to confirm your address. Signing in later always asks for a code.</p>
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? "Creating…" : "Create account"}</button>
      </form>
      <div className="or" aria-hidden="true"><span>or</span></div>
      <a href={authLink("/api/v1/auth/google/start", next)} className="button-secondary">Continue with Google</a>
      <p className="muted">
        SGD only. InvoiceNow is not supported (<a href="https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/gst-invoicing-requirements/invoicenow" target="_blank" rel="noreferrer">IRAS InvoiceNow requirements</a>). <Link href="/limits">What Tidal does not do</Link>.
      </p>
      <p className="muted">Already have an account? <Link href={authLink("/login", next)}>Sign in</Link></p>
    </AuthShell>
  );
}

export default function SignupPage() {
  return <Suspense fallback={null}><SignupForm /></Suspense>;
}
