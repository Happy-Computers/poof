# infinity-storage-mount — Infinity Storage

Go volume client. Apps see a normal folder/drive; reads stream through the Zig block cache.

Related: [`architecture.md`](architecture.md) · [`spch.md`](spch.md) · [`s3-origin.md`](s3-origin.md) · [`mvp-plan.md`](mvp-plan.md)

## Layout

```text
cmd/infinity-storage-mount          CLI flags → mount.Run
internal/mount           portable Config + Prepare + Run entry
  linux/                 FUSE volume backend (go-fuse, fusermount3)
  windows/               WinFsp via cgofuse (SPCH over TCP)
  darwin/                stub (macFUSE / FSKit later)
internal/{catalog,s3origin,proxypool,cacheclient,awsutil}  shared
stream_proxy             Zig byte cache (UDS on Linux, --listen-tcp on Windows)
```

**Product proof:** objects already in the cloud bucket appear under the mount and are previewable via ranged reads, while new flat files use a bounded local spool and multipart S3 upload. `aws s3 cp` remains a dev/test harness for pre-seeding reads.

```text
Apps → infinity-storage-mount --bucket
         ├─ ListObjectsV2 (flat, Delimiter=/)
         ├─ embedded multi-key origin (Range GET)
         └─ ProxyPool → stream_proxy --origin-url
                └─ miss → S3 GetObject(Range=…)
```

On Windows, ProxyPool uses `--listen-tcp 127.0.0.1:PORT` instead of `--uds`. See [`spch.md`](spch.md).

---

## Linux

### Prerequisites

- `fuse3` (`fusermount3`)
- Zig `stream_proxy` built (`cd stream_proxy && zig build`)
- Go 1.22+
- AWS credentials (same as `aws` CLI / `.env`)

### S3 multi-file

```bash
cd stream_proxy && zig build && cd ..

# Dev harness only:
# aws s3 cp data/screencast.mp4 s3://YOUR_BUCKET/

mkdir -p /tmp/infinity-storage
go run ./cmd/infinity-storage-mount \
  --mount /tmp/infinity-storage \
  --bucket YOUR_BUCKET \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy

ls /tmp/infinity-storage
vlc --avcodec-hw=none /tmp/infinity-storage/screencast.mp4
```

Unmount: `Ctrl-C`, or `fusermount3 -u /tmp/infinity-storage`.

Flat bucket root only (`Delimiter=/`). Caps: `MaxFiles=256`, `MaxActiveProxies=4`.

Catalog refreshes live (~2s / on `ls`, min 1s between ListObjects).

Optional: `--prefix`, `--region`, `--profile`, `--endpoint`, `--env-file`, `--spool-dir`.

## Bounded writes

`--bucket` accepts new flat files only. A pathname is reserved before its first byte, writes must be sequential, and existing files, truncation, rename, deletion, directories, and sparse writes are rejected. The writer mount exposes accepted bytes immediately through its live spool while 16 MiB S3 multipart parts upload concurrently. A successful multipart checksum verification makes future opens use the normal S3 range path after the catalog refresh; open live reads retain their spool source.

Hard caps: two active write handles, 64 GiB total spool and per-file size, 4,096 parts, 64 MiB upload-buffer budget, 8 concurrent live reads of up to 8 MiB, and a 30 second unwritten-range wait. Pass `--spool-dir PATH` to control where accepted data remains while durability is pending; the default is `${TMPDIR}/infinity-storage-spool`.

### Local harness (`--dir`)

```bash
go run ./cmd/infinity-storage-mount --mount /tmp/infinity-storage --dir ./data \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy
```

### Single-file UDS

```bash
# terminal 1
go run ./cmd/infinity-storage-origin --bucket B --key screencast.mp4 --listen 127.0.0.1:9090
# terminal 2
./stream_proxy/zig-out/bin/stream_proxy \
  --origin-url http://127.0.0.1:9090/object \
  --name screencast.mp4 --uds /tmp/infinity-storage-cache.sock --no-http
# terminal 3
go run ./cmd/infinity-storage-mount --mount /tmp/infinity-storage --uds /tmp/infinity-storage-cache.sock
```

---

## Windows

### Prerequisites

- [WinFsp](https://github.com/winfsp/winfsp/releases) installed
- Go 1.22+ (Windows)
- Zig cross-build or native Windows build of `stream_proxy`
- AWS credentials

### Build

```powershell
# From repo root (on Windows), or cross-compile from Linux:
cd stream_proxy
zig build -Dtarget=x86_64-windows-gnu
# → zig-out/bin/stream_proxy.exe

cd ..
go build -o infinity-storage-mount.exe ./cmd/infinity-storage-mount
```

Cross-compile mount from Linux:

```bash
CGO_ENABLED=0 GOOS=windows go build -o infinity-storage-mount.exe ./cmd/infinity-storage-mount
```

(`cgofuse` uses the WinFsp DLL at runtime via nocgo; WinFsp must be installed on the Windows machine.)

### S3 multi-file demo checklist

1. Install WinFsp; reboot if the installer asks.
2. Place `infinity-storage-mount.exe` and `stream_proxy.exe` together (or pass `--proxy-bin`).
3. Ensure AWS creds work (`aws s3 ls s3://YOUR_BUCKET`).
4. Seed test objects if needed (`aws s3 cp …`).
5. Mount:

```powershell
.\infinity-storage-mount.exe --mount Z: --bucket YOUR_BUCKET --proxy-bin .\stream_proxy.exe
```

6. In Explorer or `dir Z:\`, confirm object names.
7. Open a progressive MP4 in VLC / Photos; scrub — no full download.
8. `Ctrl-C` in the mount console to unmount (or eject the drive).

Single-file TCP harness:

```powershell
# terminal 1 — origin
go run ./cmd/infinity-storage-origin --bucket B --key clip.mp4 --listen 127.0.0.1:9090
# terminal 2 — cache
.\stream_proxy.exe --origin-url http://127.0.0.1:9090/object --name clip.mp4 --listen-tcp 127.0.0.1:9191 --no-http
# terminal 3 — mount
.\infinity-storage-mount.exe --mount Z: --uds 127.0.0.1:9191
```

---

## Limits

Zig: 1 MiB blocks × 512, 8 MiB max range, 4 concurrent origin fills.  
Go: 256 files, 4 active proxies, S3 8 MiB range / 4 concurrent fetches.

## Not yet (later ladder)

- Account-owned Postgres catalog and cross-device visibility (F)
- NLE random-write, rename, replacement, and lease semantics (D)
- Atomic S3 handoff grace cleanup for live open handles
- Nested directories
- macOS volume backend

Plan: [`write-sync-edit-plan.md`](write-sync-edit-plan.md).

## See also

- [`architecture.md`](architecture.md) — full system map
- [`spch.md`](spch.md) — Go↔Zig cache protocol
- [`stream_proxy.md`](stream_proxy.md) — Zig data plane
- [`s3-origin.md`](s3-origin.md) — S3 Range origin
