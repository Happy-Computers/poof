# space-mount — read-only Space

Go FUSE client (Linux). Apps see a normal folder; reads stream through the Zig block cache.

## Layout

```text
cmd/space-mount          CLI flags → mount.Run
internal/mount           portable Config + Run entry
  linux/                 FUSE volume backend (go-fuse, fusermount3)
  darwin/                stub (macFUSE / FSKit later)
  windows/               stub (WinFsp later)
internal/{spacecatalog,s3origin,proxypool,cacheclient,awsutil}  shared
stream_proxy             Zig byte cache (shared data plane)
```

**Product proof (this slice):** objects already in the cloud bucket appear under `/tmp/space` and are previewable immediately via ranged reads — no full download first. Putting objects in the bucket with `aws s3 cp` is a **dev/test harness only**, not a product upload path.

```text
Apps → space-mount --bucket
         ├─ ListObjectsV2 (flat, Delimiter=/)
         ├─ embedded multi-key origin (Range GET)
         └─ ProxyPool → stream_proxy --origin-url
                └─ miss → S3 GetObject(Range=…)
```

## Prerequisites

- `fuse3` (`fusermount3`)
- Zig `stream_proxy` built (`cd stream_proxy && zig build`)
- Go 1.22+
- AWS credentials (same as `aws` CLI / `.env`)

## S3 multi-file (cloud Space)

```bash
cd stream_proxy && zig build && cd ..

# Dev harness only — seed the bucket so the mount has something to list:
# aws s3 cp data/screencast.mp4 s3://YOUR_BUCKET/
# aws s3 cp data/demo.bin s3://YOUR_BUCKET/

mkdir -p /tmp/space
go run ./cmd/space-mount \
  --mount /tmp/space \
  --bucket YOUR_BUCKET \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy

ls /tmp/space
vlc --avcodec-hw=none /tmp/space/screencast.mp4
```

Unmount: `Ctrl-C`, or `fusermount3 -u /tmp/space`.

Flat bucket root only (`Delimiter=/`). Nested keys ignored. Caps: `MaxSpaceFiles=256`, `MaxActiveProxies=4`.

Catalog **refreshes live** (every ~2s and on `ls`/lookup, min 1s between ListObjects). New objects in the bucket appear in `/tmp/space` without remounting.

Optional: `--prefix`, `--region`, `--profile`, `--endpoint`, `--env-file`.

## Local harness (`--dir`)

Still available for offline FUSE tests without AWS:

```bash
go run ./cmd/space-mount --mount /tmp/space --dir ./data \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy
```

## Single-file manual UDS

```bash
# terminal 1
go run ./cmd/space-origin --bucket B --key screencast.mp4 --listen 127.0.0.1:9090
# terminal 2
./stream_proxy/zig-out/bin/stream_proxy \
  --origin-url http://127.0.0.1:9090/object \
  --name screencast.mp4 --uds /tmp/space-cache.sock --port 8080
# terminal 3
go run ./cmd/space-mount --mount /tmp/space --uds /tmp/space-cache.sock
```

## Limits

Zig: 1 MiB blocks × 512, 8 MiB max range, 4 concurrent origin fills.  
Go: 256 files, 4 active proxies, S3 8 MiB range / 4 concurrent fetches.

## Not yet (later ladder)

- **F in progress:** shared metadata + auth + second device (see mvp-plan)
- Writes into Space / background upload (E) — the real product ingest path
- Nested directories
- Peer byte serving while uploading
