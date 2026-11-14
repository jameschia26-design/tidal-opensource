# Shared sample operator setup

A shared sample is visible to every signed-in user, including its books and attached documents. Every user, including its owner, gets viewer access. Never mark a real company or a company containing private data.

For an existing sample, first back up the database. Inspect the company, all its books, transactions, contacts and attachments; remove sensitive content before proceeding. Find and independently confirm its numeric company ID. No company is marked automatically by name.

With the backend migrations available and `TEST_DATABASE_URL` explicitly set to the intended database, run from `backend`:

```sh
SAMPLE_ORG_ID=<confirmed-company-id> go test -tags sampleseed -run '^TestMarkExistingSample$' -count=1 .
```

This runs migrations and marks that active company. The database permits only one shared sample. To reverse the designation, an operator can set `is_shared_sample=false` for that confirmed ID; existing memberships remain in effect. Check those memberships before removing shared status.

For a new sample, use `go test -tags sampleseed -run '^TestSeedSample$' -count=1 .` with `TEST_DATABASE_URL` set. The seed refuses to run if a shared sample or the original seeded sample name already exists. If a legacy sample was renamed, inspect the database and mark it instead; the seed cannot identify renamed legacy data. Never run the ordinary integration suite against a real database: it deletes the public schema.
