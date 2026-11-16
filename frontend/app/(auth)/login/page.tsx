"use client";
import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, type ApiError } from "@/lib/api";
import { authLink } from "@/lib/auth-destination";
import AuthShell from "@/components/AuthShell";

const GOOGLE_ERRORS: Record<string, string> = {
  google_failed: "Google sign-in did not complete. Try again.",
  google_unverified: "Google could not verify this email address.",
  google_account_exists: "This email already has a password account. Sign in with your password.",
  deactivated: "This account has been deactivated.",
};

function LoginForm() {
  const router = useRouter();
  const next = useSearchParams().get("next");
  const googleError = useSearchParams().get("error");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (googleError) setError(GOOGLE_ERRORS[googleError] ?? "Google sign-in did not complete.");
  }, [googleError]);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      // Step 1 always returns the same reply whether or not the account exists.
      await api("/v1/auth/login", { method: "POST", body: JSON.stringify({ email, password }) });
      router.push(authLink(`/verify?email=${encodeURIComponent(email)}`, next));
    } catch (err) {
      const e2 = err as ApiError;
      setError(e2.error === "email_unavailable"
        ? "We cannot send sign-in codes right now. Try again later."
        : e2.error === "backend_unavailable" || e2.status === 0
          ? "Sign-in service is unavailable right now. Try again shortly."
          : "Email or password is not right.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Sign in" subtitle="We will email you a one-time code after your password.">
      <div style={{ background: "rgba(245, 158, 11, 0.08)", border: "1px solid rgba(245, 158, 11, 0.35)", borderRadius: "6px", padding: "0.75rem 1rem", marginBottom: "1.25rem", fontSize: "0.85rem", lineHeight: "1.4" }}>
        <strong>Public Demo Instance:</strong> All accounts and data are automatically purged after 7 days. Need a permanent deployment for your organization? Contact <a href="mailto:admin@goodsoil.cc" style={{ textDecoration: "underline", color: "inherit" }}>admin@goodsoil.cc</a>.
      </div>
      <form onSubmit={submit} noValidate>
        <label htmlFor="email">Work email</label>
        <input id="email" name="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <label htmlFor="password">Password</label>
        <input id="password" name="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? "Checking…" : "Continue"}</button>
      </form>
      <div className="or" aria-hidden="true"><span>or</span></div>
      <a href={authLink("/api/v1/auth/google/start", next)} className="button-secondary">Continue with Google</a>
      <p className="muted">New to Tidal? <Link href={authLink("/signup", next)}>Create an account</Link> · <Link href="/forgot">Forgot password?</Link></p>
    </AuthShell>
  );
}

export default function LoginPage() {
  return <Suspense fallback={null}><LoginForm /></Suspense>;
}
