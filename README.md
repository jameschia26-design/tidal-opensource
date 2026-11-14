# Tidal

Free, open-source, browser-based accounting for small businesses. Tidal is built with Next.js, Go and PostgreSQL, and can be self-hosted.

**Preview software — not yet ready for real company books.** Core database workflows, email-code delivery, Google sign-in, deployment, backups and restore have not all passed end-to-end acceptance. Tax worksheets are not filed returns. See [current limits](frontend/app/limits/page.tsx) and the self-hosting example in `compose.yaml`.

## Features

- Double-entry accounting, invoices, bills, expenses and payments
- Customers, suppliers, items, quotes and inventory
- Financial statements, bank statement CSV import and Singapore GST worksheets
- CSV imports and exports, with deterministic mappings and review
- Responsive browser interface; no desktop client

Tidal contains no AI features and sends no accounting data to AI providers. Imports use explicit mappings and validation.

## Run locally

Requirements: Docker Compose, Go, Node.js and PostgreSQL for database integration tests.

```sh
cp backend/.env.example backend/.env
# Set unique local values for TIDAL_DB_PASSWORD and TIDAL_DATABASE_URL.
# Set TIDAL_PUBLIC_ORIGIN=http://localhost:3000 and TIDAL_ENV=development.
docker compose --env-file backend/.env up --build
```

In `frontend/.env.local`, set `BACKEND_URL=http://127.0.0.1:8080`, then run `npm run dev` from `frontend`.

## Checks

```sh
go -C backend vet ./...
go -C backend test -race ./...
go -C backend build ./...
cd frontend && npm ci && npm run check
```

Database integration tests require a disposable `TEST_DATABASE_URL`; tests erase its schema. Never point it at company data.

## Security and deployment

Do not commit secrets, customer data, database backups or production settings. Review `docker-compose.yml` and `backend/.env.example` before self-hosting. Do not deploy this preview to production accounting workloads until authentication, data integrity, backups and restore have been independently verified.

## License

MIT. See [LICENSE](LICENSE).
