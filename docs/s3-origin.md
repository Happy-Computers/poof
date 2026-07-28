# S3 origin — stream from object storage

Read path with cloud as source of truth (still one object, still read-only).

```text
VLC → space-mount (Go FUSE)
         │ UDS
         ▼
   stream_proxy (Zig cache)
         │ miss: HTTP Range GET
         ▼
   space-origin (Go) → S3 GetObject(Range=…)
```

## Prerequisites

On this machine AWS CLI lives at `~/.local/bin/aws`. You still need **keys** somewhere the SDK can see them.

```bash
export PATH="$HOME/.local/bin:$PATH"

# ONE-TIME: create ~/.aws/credentials (same as normal CLI)
aws configure
# Access Key ID / Secret / region (e.g. us-east-1) / output json

# prove it:
aws sts get-caller-identity
aws s3 ls s3://YOUR_BUCKET/screencast.mp4
```

Or put keys in a gitignored `.env` (from `.env.example`) in the repo root — `space-origin` loads `.env` / `.env.local` automatically.

Then either rely on default profile, or pass `--profile` / `--region`.

Upload example:

```bash
aws s3 cp data/screencast.mp4 s3://YOUR_BUCKET/screencast.mp4
# MinIO:
# aws --endpoint-url http://127.0.0.1:9000 s3 cp data/screencast.mp4 s3://YOUR_BUCKET/screencast.mp4
```

## Run

Terminal 1 — Go S3 range origin:

```bash
go run ./cmd/space-origin \
  --bucket YOUR_BUCKET \
  --key screencast.mp4 \
  --region us-east-1 \
  --listen 127.0.0.1:9090

# with named profile (same as aws --profile):
# go run ./cmd/space-origin --bucket B --key K --region us-east-1 --profile YOUR_PROFILE

# MinIO:
# go run ./cmd/space-origin --bucket B --key K --endpoint http://127.0.0.1:9000 --region us-east-1
```

Terminal 2 — Zig cache (HTTP origin, no local `--file`):

```bash
export PATH="$HOME/.local/bin:$PATH"
cd stream_proxy && zig build
./zig-out/bin/stream_proxy \
  --origin-url http://127.0.0.1:9090/object \
  --name screencast.mp4 \
  --uds /tmp/space-cache.sock \
  --port 8080
```

Terminal 3 — mount:

```bash
mkdir -p /tmp/space
go run ./cmd/space-mount --mount /tmp/space --uds /tmp/space-cache.sock
vlc --avcodec-hw=none /tmp/space/screencast.mp4
```

Proof:

```bash
curl http://127.0.0.1:8080/metrics
# bytes_from_origin ≈ what you touched; cache_occupancy_blocks capped
# S3 / space-origin should show Range GETs, not a full-object download
```

## Local dry-run (no AWS)

Point `--origin-url` at another `stream_proxy --file` HTTP front (same Range contract as `/object`):

```bash
# origin stand-in
stream_proxy --file data/screencast.mp4 --port 9090
# cache
stream_proxy --origin-url http://127.0.0.1:9090/video.mp4 --name screencast.mp4 --uds /tmp/space-cache.sock
```

## Caps

| Limit | Where |
|---|---|
| 8 MiB max range | Zig `MAX_RANGE_BYTES` + Go `MaxRangeBytes` |
| 4 concurrent origin fills | Zig `MAX_CONCURRENT_ORIGIN_FILLS` + Go S3 semaphore |
| Cache mutex not held across origin HTTP | Zig `ensureBlockPresent` |

## Not yet

- Multi-key listing / multi-file Space
- Writes / multipart upload
- Presigned-URL-only mode
