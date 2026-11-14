"use client";
import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, type ApiError } from "@/lib/api";
import { authDestination } from "@/lib/auth-destination";
import AuthShell from "@/components/AuthShell";

function VerifyForm() {
  const router = useRouter();
  const params = useSearchParams();
  const email = params.get("email") ?? "";
  const next = params.get("next");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      // Signup verification lands here too; a signup code lives under signup/verify, a sign-in code
      // under login/verify. Trying both keeps one form for both steps without saying which one fit.
      for (const path of ["/v1/auth/signup/verify", "/v1/auth/login/verify"]) {
        try {
          await api(path, { method: "POST", body: JSON.stringify({ email, code }) });
          router.push(authDestination(next));
          return;
        } catch (inner) {
          const e3 = inner as ApiError;
          if (e3.status === 401) continue;
          throw inner;
        }
      }
      setError("That code is not valid.");
    } catch (err) {
      const e2 = err as ApiError;
      setError(e2.status === 429
        ? "Too many attempts. Wait a few minutes and sign in again."
        : e2.status === 410
          ? "That code has expired. Sign in again to get a new one."
          : "That code is not valid.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Enter your code" subtitle={email ? `We sent a 6-digit code to ${email}.` : "Enter the 6-digit code from your email."}>
      <form onSubmit={submit} noValidate>
        <label htmlFor="code">One-time code</label>
        <input id="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy || code.length !== 6}>{busy ? "Checking…" : "Verify and sign in"}</button>
      </form>
    </AuthShell>
  );
}

export default function VerifyPage() {
  return <Suspense fallback={null}><VerifyForm /></Suspense>;
}
