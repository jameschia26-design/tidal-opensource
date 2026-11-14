// Browser-side client. Same-origin only; credentials ride the HttpOnly cookie.
export type ApiError = { error: string; status: number };

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("content-type")) headers.set("content-type", "application/json");
  let res: Response;
  try {
    res = await fetch(`/api${path}`, { ...init, headers, credentials: "same-origin", cache: "no-store" });
  } catch {
    // Network failure before any reply: report the same code the proxy uses for a down backend.
    throw { error: "backend_unavailable", status: 0 } as ApiError;
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw { error: typeof body.error === "string" ? body.error : "request_failed", status: res.status } as ApiError;
  }
  return (res.status === 204 ? undefined : await res.json()) as T;
}
