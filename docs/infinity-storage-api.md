# Infinity Storage API

Better Auth email/password service for the Electron client. It is a separate process because the
Better Auth secret and Supabase Postgres credentials must never ship inside the desktop app.
A deployable API also gives Linux and Windows clients one session and metadata authority.

Only email/password authentication is enabled. There are no OAuth providers or Supabase Auth users.
Supabase supplies Postgres; Better Auth owns authentication.

## Configure

Create a Supabase project, then copy `api/.env.example` to the repository root as `.env`. Desktop launchers and API scripts load this file.

- Use the direct Supabase connection for a long-lived IPv6-capable server.
- Use the Supavisor session pooler on port `5432` when the server needs IPv4.
- Generate `BETTER_AUTH_SECRET` with `openssl rand -base64 32`.
- Set a verified Resend sender and API key for verification and password-reset mail.
- Set `BETTER_AUTH_URL` to the public API origin in production.

The database URL stays in the API. It is never exposed through Electron IPC. Desktop development may run the API on `127.0.0.1:3005`; that is only the local HTTP process. Database data still goes to Supabase through `DATABASE_URL`. `scripts/local-db.sh` is an optional local-development helper and is not used by desktop launchers.

## Database

The migrations create private `infinity_storage_auth` and `infinity_storage` schemas. They are outside
Supabase's default Data API surface, and access is revoked from the `anon` and `authenticated` roles.
The `infinity_storage.mounts` table stores one shared mount name per account project. Existing projects
receive mount profiles during migration.

When the Supabase project exists:

```bash
pnpm dlx supabase@latest login
pnpm dlx supabase@latest link --project-ref YOUR_PROJECT_REF
pnpm dlx supabase@latest db push
```

## Run

```bash
cd api
cp .env.example .env
pnpm i
pnpm start
```

Better Auth is served under `/api/auth`. The reset email opens `/reset-password`, which accepts a
new 12–128 character password and revokes existing sessions. `GET /v1/mounts` returns account mount
profiles. `POST /v1/mounts` creates or updates one profile for a project. `PATCH /v1/mounts/:id` renames
one profile.

## Relay authority

`GET /v1/libraries/:project-id/authorize` requires a bearer session and returns `204` only when the
project belongs to that account. Configure the live relay with `--authority-url` pointing to this
API. The relay uses this endpoint before serving any in-progress catalog or range request, allowing
another device signed into the same account to see accepted file ranges before S3 upload finishes.

Then start Electron with the matching API origin:

```bash
cd desktop
INFINITY_STORAGE_API_URL=http://127.0.0.1:3005 pnpm start
```
