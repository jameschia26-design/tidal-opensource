// Fixed upstream resolved from server-side env. Never built from request input.
export class UpstreamConfigError extends Error {}

export function backendBase(): URL {
  const raw = process.env.BACKEND_URL;
  if (!raw) throw new UpstreamConfigError("BACKEND_URL is not set");
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new UpstreamConfigError("BACKEND_URL is not a valid URL");
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new UpstreamConfigError("BACKEND_URL must be http or https");
  }
  if (url.search || url.hash || url.username || url.password || (url.pathname !== "/" && url.pathname !== "")) {
    throw new UpstreamConfigError("BACKEND_URL must be origin only");
  }
  return url;
}
