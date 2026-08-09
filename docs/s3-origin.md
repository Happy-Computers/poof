# S3 origin — stream from object storage

Cloud is source of truth for **reads**. Apps open files in the Infinity Storage mount; only touched byte ranges hit S3.

`aws s3 cp` (or console upload) is a **dev harness** to put objects in the bucket for demos — not how users ingest media in the product.

```text
VLC / Explorer → infinity-storage-mount --bucket
         │ list + SPCH to Zig proxies (UDS Linux / TCP Windows)
         ▼
   stream_proxy (Zig cache) --origin-url
         │ miss: HTTP Range GET
         ▼
   embedded / infinity-storage-origin → S3 GetObject(Range=…)
```

System map: [`architecture.md`](architecture.md). Mount how-to: [`infinity-storage-mount.md`](infinity-storage-mount.md).

## Prerequisites

```bash
export PATH="$HOME/.local/bin:$PATH"
aws configure   # or .env from .env.example
aws sts get-caller-identity
```

## Preferred: one-command mount

See [`infinity-storage-mount.md`](infinity-storage-mount.md) — `infinity-storage-mount --bucket` lists flat objects, embeds the multi-key origin, and pools Zig caches (Linux directory or Windows `Z:`).

## Standalone origin (debug)

Single object:

```bash
go run ./cmd/infinity-storage-origin --bucket YOUR_BUCKET --key screencast.mp4 --listen 127.0.0.1:9090
```

Multi-object (flat list):

```bash
go run ./cmd/infinity-storage-origin --bucket YOUR_BUCKET --list [--prefix P] --listen 127.0.0.1:9090
# HEAD/GET http://127.0.0.1:9090/object/<basename>  (Range required on GET)
```

Then point Zig at `http://127.0.0.1:9090/object/<name>` or use `infinity-storage-mount --bucket`.

## Caps

| Limit | Where |
|---|---|
| 8 MiB max range | Zig + Go |
| 4 concurrent S3 fills | Go semaphore |
| 256 flat objects | catalog |
| Cache mutex not held across origin HTTP | Zig |

## Not yet

- Bounded product write / multipart upload path (E)
- Account-owned metadata and cross-device visibility (F)
- Nested keys as directories
- Presigned-upload-only mode

Plan: [`write-sync-edit-plan.md`](write-sync-edit-plan.md).
