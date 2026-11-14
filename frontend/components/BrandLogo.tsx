"use client";
import { useOrg } from "@/components/OrgProvider";

// Company logo in the top bar. The logo endpoint is an authenticated API image, which next/image
// cannot optimise, so this is a plain img; a 404 means no logo and the brand mark stays.
export default function BrandLogo() {
  const { selected } = useOrg();
  return (
    <>
      {/* eslint-disable-next-line @next/next/no-img-element -- authenticated API image; next/image cannot optimise it. */}
      {selected && (
        <img
          key={selected.id}
          src={`/api/v1/organizations/${selected.id}/logo`}
          alt=""
          width={128}
          height={32}
          style={{ height: "1.9rem", width: "auto", maxWidth: "8rem", objectFit: "contain", display: "block" }}
          onError={(e) => { e.currentTarget.style.display = "none"; }}
          onLoad={(e) => { e.currentTarget.style.display = "block"; }}
        />
      )}
      <span style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem" }}>
        <svg width="24" height="24" viewBox="0 0 40 40" fill="none" aria-hidden="true" style={{ flexShrink: 0 }}>
          <path d="M4 13h12c8 0 12 14 20 14M4 21h9c9 0 12-14 23-14M4 29h9c11 0 13-14 23-14" stroke="currentColor" strokeWidth="3" />
        </svg>
        <span style={{ fontWeight: 700, fontSize: "1.3rem", letterSpacing: "-0.02em" }}>
          tidal<span style={{ color: "#d2f26b" }}>.</span>
        </span>
      </span>
    </>
  );
}
