# Infinity Storage API

Better Auth email/password service for the Electron client. It is a separate process because the
Better Auth secret and Supabase Postgres credentials must never ship inside the desktop app.
A deployable API also gives Linux and Windows clients one session and metadata authority.

Only email/password authentication is enabled. There are no OAuth providers or Supabase Auth users.
Supabase supplies Postgres; Better Auth owns authentication.

## Configure

Create a Supabase project, then copy `api/.env.example` to `api/.env`.

- Use the direct Supabase connection for a long-lived IPv6-capable server.
- Use the Supavisor session pooler on port `5432` when the server needs IPv4.
- Generate `BETTER_AUTH_SECRET` with `openssl rand -base64 32`.
- Set a verified Resend sender and API key for verification and password-reset mail.
- Set `BETTER_AUTH_URL` to the public API origin in production.

The database URL stays in the API. It is never exposed through Electron IPC.

## Database

The migrations create a private `infinity_storage_auth` schema. It is outside Supabase's default
Data API surface, and access is revoked from the `anon` and `authenticated` roles.

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
new 12–128 character password and revokes existing sessions.

Then start Electron with the matching API origin:

```bash
cd desktop
INFINITY_STORAGE_API_URL=http://127.0.0.1:3005 pnpm start
```
