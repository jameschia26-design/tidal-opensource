"use client";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { api } from "@/lib/api";

// Organization IDs are JSON numbers (int64) per the frozen backend contract.
export type Org = { id: number; name: string; role: string; company_id: number; company_name: string; is_book: boolean; fiscal_year_end?: string };

type OrgState = {
  orgs: Org[] | null;
  selected: Org | null;
  error: boolean;
  select: (id: Org["id"]) => void;
  refresh: () => Promise<void>;
};

const Ctx = createContext<OrgState | null>(null);
const KEY = "tidal.selectedOrg";

// Selection is a per-viewer convenience only. The server still enforces org access on every request.
function readStored(): string | null {
  try {
    return window.localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

function writeStored(id: Org["id"]) {
  try {
    window.localStorage.setItem(KEY, String(id));
  } catch {
    // Storage blocked: selection lasts for this page only.
  }
}

export function OrgProvider({ children }: { children: ReactNode }) {
  const [orgs, setOrgs] = useState<Org[] | null>(null);
  const [selectedId, setSelectedId] = useState<Org["id"] | null>(null);
  const [error, setError] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const r = await api<{ organizations: Org[] }>("/v1/organizations");
      const stored = readStored();
      setError(false);
      setOrgs(r.organizations);
      setSelectedId((cur) => {
        const want = cur != null ? String(cur) : stored;
        const hit = r.organizations.find((o) => String(o.id) === want);
        return (hit ?? r.organizations[0])?.id ?? null;
      });
    } catch {
      setError(true);
      setOrgs([]);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const select = useCallback((id: Org["id"]) => {
    setSelectedId(id);
    writeStored(id);
  }, []);

  const selected = orgs?.find((o) => selectedId != null && String(o.id) === String(selectedId)) ?? null;

  return <Ctx.Provider value={{ orgs, selected, error, select, refresh }}>{children}</Ctx.Provider>;
}

export function useOrg(): OrgState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useOrg must be used inside OrgProvider");
  return v;
}
