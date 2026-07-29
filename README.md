# Space — cloud folder that streams

Mount cloud media as a local folder/drive. Apps open files normally; only touched byte ranges leave object storage. Local use stays a **bounded cache**, not a full library download.

## Docs

| Doc | Contents |
|---|---|
| [`docs/README.md`](docs/README.md) | Doc index |
| [`docs/mvp-plan.md`](docs/mvp-plan.md) | Goal, constraints, build ladder, current position |
| [`docs/architecture.md`](docs/architecture.md) | System map, package layout, Linux vs Windows |
| [`docs/space-mount.md`](docs/space-mount.md) | How to run the mount (Linux + Windows) |
| [`docs/s3-origin.md`](docs/s3-origin.md) | S3 range origin |
| [`docs/stream_proxy.md`](docs/stream_proxy.md) | Zig cache / HTTP harness |
| [`docs/spch.md`](docs/spch.md) | Go↔Zig binary cache protocol (UDS + TCP) |
| [`TIGERSTYLE.md`](TIGERSTYLE.md) | Design / coding style |

## Quick start (Linux)

```bash
cd stream_proxy && zig build && cd ..
mkdir -p /tmp/space
go run ./cmd/space-mount \
  --mount /tmp/space \
  --bucket YOUR_BUCKET \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy
```

## Quick start (Windows)

Install [WinFsp](https://github.com/winfsp/winfsp/releases). Build `stream_proxy.exe` and `space-mount.exe`, then:

```powershell
.\space-mount.exe --mount Z: --bucket YOUR_BUCKET --proxy-bin .\stream_proxy.exe
```

Details and checklist: [`docs/space-mount.md`](docs/space-mount.md).

## Stack

- **Zig** — fixed block cache, prefetch, hard limits (`stream_proxy`)
- **Go** — mount, S3 glue, proxy pool (`cmd/space-mount`, `internal/…`)
- **Volume backends** — Linux FUSE, Windows WinFsp (cgofuse); macOS later

## Status

Multi-file S3 read mount works on **Linux** and **Windows**.  
Next: native Drive UI (language TBD) → auth + Postgres catalog (Supabase) → dual-device sync. See mvp-plan.
