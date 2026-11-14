import Link from "next/link";
import type { ReactNode } from "react";

export default function AuthShell({ title, subtitle, children }: { title: string; subtitle: string; children: ReactNode }) {
  return (
    <div className="auth">
      <header className="auth-top">
        <Link href="/" className="brand" aria-label="Tidal home">
          <svg width="28" height="28" viewBox="0 0 40 40" fill="none" aria-hidden="true" style={{ flexShrink: 0 }}>
            <path d="M4 13h12c8 0 12 14 20 14M4 21h9c9 0 12-14 23-14M4 29h9c11 0 13-14 23-14" stroke="currentColor" strokeWidth="3" />
          </svg>
          <span>
            tidal<span className="brand-dot">.</span>
          </span>
        </Link>
      </header>
      <main className="auth-card">
        <h1>{title}</h1>
        <p className="muted">{subtitle}</p>
        {children}
      </main>
    </div>
  );
}
