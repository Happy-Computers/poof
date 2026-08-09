# Infinity Storage — cloud folder that streams

Mount cloud media as a local folder/drive. Apps open files normally; only touched byte ranges leave object storage. Local use stays a **bounded cache**, not a full library download.

## Docs

| Doc | Contents |
|---|---|
| [`docs/README.md`](docs/README.md) | Doc index |
| [`docs/mvp-plan.md`](docs/mvp-plan.md) | Goal, constraints, build ladder, current position |
| [`docs/write-sync-edit-plan.md`](docs/write-sync-edit-plan.md) | E → D → F implementation and test plan |
| [`docs/architecture.md`](docs/architecture.md) | System map, package layout, Linux vs Windows |
| [`docs/infinity-storage-mount.md`](docs/infinity-storage-mount.md) | How to run the mount (Linux + Windows) |
| [`docs/infinity-storage-desktop.md`](docs/infinity-storage-desktop.md) | Electron auth and mount shell |
| [`docs/infinity-storage-api.md`](docs/infinity-storage-api.md) | Better Auth + Supabase Postgres API |
| [`docs/s3-origin.md`](docs/s3-origin.md) | S3 range origin |
| [`docs/stream_proxy.md`](docs/stream_proxy.md) | Zig cache / HTTP harness |
| [`docs/spch.md`](docs/spch.md) | Go↔Zig binary cache protocol (UDS + TCP) |
| [`TIGERSTYLE.md`](TIGERSTYLE.md) | Design / coding style |

## Quick start (Linux)

```bash
cd stream_proxy && zig build && cd ..
mkdir -p /tmp/infinity-storage
go run ./cmd/infinity-storage-mount \
  --mount /tmp/infinity-storage \
  --bucket YOUR_BUCKET \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy
```

## Quick start (Windows)

Install [WinFsp](https://github.com/winfsp/winfsp/releases). Build `stream_proxy.exe` and `infinity-storage-mount.exe`, then:

```powershell
.\infinity-storage-mount.exe --mount Z: --bucket YOUR_BUCKET --proxy-bin .\stream_proxy.exe
```

Details and checklist: [`docs/infinity-storage-mount.md`](docs/infinity-storage-mount.md).

## Stack

- **Zig** — fixed block cache, prefetch, hard limits (`stream_proxy`)
- **Go** — mount, S3 glue, proxy pool (`cmd/infinity-storage-mount`, `internal/…`)
- **Electron** — thin desktop shell (`desktop/`)
- **TypeScript** — separate Better Auth service (`api/`)
- **Supabase Postgres** — private Better Auth schema
- **Volume backends** — Linux FUSE, Windows WinFsp (cgofuse); macOS later

## Status

Multi-file S3 read mount works on **Linux** and **Windows**.  
Next: bounded local ingest while S3 persists in the background (E), then one-machine
edit-from-mount behavior (D). Yave-authorized sharing and relay follow in F. See mvp-plan.
