# Dependency advisories (R23)

Reviewed 2026-11-01.

## Production

`npm audit --omit=dev` reports **0 vulnerabilities**. Nothing that ships in the frontend
bundle has an advisory.

## Development tools

`npm audit` reports 16 moderate advisories, all the same issue:
`minimatch < 11.0.1` (GHSA-g3hv-75wg-m249, glob denial of service).

Reachability: minimatch is pulled in only by the ESLint plugins
(`eslint-plugin-import`, `eslint-plugin-jsx-a11y`, `eslint-plugin-react` through
`eslint-config-next`). It runs on a developer machine or in CI while linting source
files written by us, never on request data and never in the deployed image.
The one fix offered is a forced major upgrade of the ESLint plugin tree; until
`eslint-config-next` allows it, the advisory is recorded here as **not reachable**
rather than forced through a breaking toolchain change.

## Go

`govulncheck ./...` runs in CI with the official tool (`golang.org/x/vuln/cmd/govulncheck`).
It reports **0 reachable vulnerabilities**.

Two findings were fixed for this review:

- `github.com/go-jose/go-jose/v4` 4.0.5 → 4.1.4 (GO-2026-4945, JWS verification).
- The Go toolchain moved to 1.26.10, which carries the `net/http`, `net/textproto`,
  `crypto/tls` and `crypto/x509` fixes from 1.26.9 and 1.26.10.
