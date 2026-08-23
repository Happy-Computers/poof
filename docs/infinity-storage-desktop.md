# Infinity Storage desktop (Electron shell)

Thin native UI over `infinity-storage-mount`. Linux + Windows. No framework — plain HTML/CSS/JS.

## Run

On Windows, start the API and Electron app together from the repository root:

```powershell
.\scripts\run-desktop.ps1
```

The script loads `.env`, builds the Windows mount, Zig proxy, and Electron renderer, writes API logs to
`.logs`, starts the API,
and closes it when Electron exits. Pass `-SkipBuild` when native binaries and renderer are already
current. Pass `-RelayURL https://relay.example.com` to enable same-account in-progress file
visibility.

On Linux, install `fuse3`, add AWS credentials to `.env`, then run:

```bash
./scripts/run-desktop.sh
```

It builds the Linux FUSE mount and Zig proxy, starts the API, and opens Electron. Linux mounts are
folders below the system temporary directory; Windows mounts remain drive letters.

## Windows ↔ Linux live files

Use the same deployed API and the same reachable relay URL on both machines:

```text
INFINITY_STORAGE_API_URL=https://api.example.com
INFINITY_STORAGE_LIVE_RELAY_URL=https://relay.example.com
```

Start the relay once with `infinity-storage-relay --authority-url https://api.example.com`. Sign in
on both machines, mount the same project, then copy a new file into the Windows drive. Within the
catalog poll interval, the Linux folder shows it and can read every range Windows has accepted; S3
continues uploading in the background. The reverse direction follows the same flow.

On Linux, `pnpm start` passes `--no-sandbox` so Chromium starts without a root-owned `chrome-sandbox`.

Optional env overrides:

| Var | Default |
|---|---|
| `INFINITY_STORAGE_API_URL` | `http://127.0.0.1:3005` |
| `INFINITY_STORAGE_MOUNT_BIN` | `../infinity-storage-mount` (or `.exe` on Windows) |
| `INFINITY_STORAGE_PROXY_BIN` | `../stream_proxy/zig-out/bin/stream_proxy` |
| `INFINITY_STORAGE_LIVE_RELAY_URL` | Empty; direct S3 visibility after upload |

## What it does

- Better Auth email/password sign-up, verification, sign-in, reset, and sign-out
- Mount / Unmount / Open folder after sign-in
- When `INFINITY_STORAGE_LIVE_RELAY_URL` is configured, pass the signed-in session and project ID to the mount for account-authorized in-progress file visibility across devices
- Spawns at most one `infinity-storage-mount` child
- Keeps database credentials and auth cookies out of the renderer

The API setup is documented in [`infinity-storage-api.md`](infinity-storage-api.md).
