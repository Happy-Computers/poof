# Infinity Storage desktop (Electron shell)

Thin native UI over `infinity-storage-mount`. Linux + Windows. No framework — plain HTML/CSS/JS.

## Run

```bash
# build mount + cache first (see docs/infinity-storage-mount.md)
cd desktop
pnpm i
pnpm start
```

On Linux, `pnpm start` passes `--no-sandbox` so Chromium starts without a root-owned `chrome-sandbox`.

Optional env overrides:

| Var | Default |
|---|---|
| `INFINITY_STORAGE_API_URL` | `http://127.0.0.1:3005` |
| `INFINITY_STORAGE_MOUNT_BIN` | `../infinity-storage-mount` (or `.exe` on Windows) |
| `INFINITY_STORAGE_PROXY_BIN` | `../stream_proxy/zig-out/bin/stream_proxy` |

## What it does

- Better Auth email/password sign-up, verification, sign-in, reset, and sign-out
- Mount / Unmount / Open folder after sign-in
- Spawns at most one `infinity-storage-mount` child
- Keeps database credentials and auth cookies out of the renderer

The API setup is documented in [`infinity-storage-api.md`](infinity-storage-api.md).
