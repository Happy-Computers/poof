# Architecture — Infinity Storage (mount + stream)

How the pieces fit. Product ladder and demo order: [`mvp-plan.md`](mvp-plan.md).
Operator how-tos: [`infinity-storage-mount.md`](infinity-storage-mount.md), [`s3-origin.md`](s3-origin.md), [`stream_proxy.md`](stream_proxy.md).
Cache IPC wire format: [`spch.md`](spch.md).

---

## Mental model

```text
[Apps: VLC / Explorer / Finder / NLE]
        │ open / read / seek  (OS filesystem API)
        ▼
[Infinity Storage client — Go]
  cmd/infinity-storage-mount → internal/mount.Run
    • Linux:  FUSE (hanwen/go-fuse)     internal/mount/linux
    • Windows: WinFsp (cgofuse)         internal/mount/windows
    • Darwin:  stub                     internal/mount/darwin
  catalog: names + sizes (S3 List today; meta hub later)
  proxypool: ≤4 stream_proxy children
        │ SPCH (SIZE / READ / …)
        │   Linux:  Unix domain socket (--uds)
        │   Windows: TCP 127.0.0.1 (--listen-tcp)
        ▼
[Data plane — Zig stream_proxy]
  fixed block cache + prefetch (limits.zig)
  origin: local --file  OR  HTTP Range --origin-url
        │
        ▼
[Object storage / local file]
  S3 via embedded origin in infinity-storage-mount --bucket
  (or cmd/infinity-storage-origin for debug)
```

**Source of truth for bytes = remote object store** (local `--dir` / `--file` are harnesses).
**Logical file size** (getattr) = object size. **Size on disk** for the virtual inode stays ~0; scrubbing fills a bounded RAM cache, not a full local copy.

---

## Repo layout

```text
desktop/                Electron email/password + mount shell
api/                    Better Auth HTTP service; owns secrets and DB access
supabase/migrations/    Private Better Auth schema

cmd/
  infinity-storage-mount/          CLI → mount.Run (all OSes)
  infinity-storage-origin/         Optional standalone S3 Range HTTP origin

internal/
  mount/
    config.go           MountPoint, Bucket/Dir/UDS, …
    prepare.go          Shared catalog + origin + proxypool wiring
    run_linux.go        //go:build linux
    run_windows.go      //go:build windows
    run_other.go        //go:build !linux && !windows
    linux/              FUSE FS + fusermount3 cleanup
    windows/            cgofuse / WinFsp FS
    darwin/             stub
  catalog/         Flat name → Entry (≤256 files)
  s3origin/             ListFlat + Range GET store/handler
  proxypool/            Spawn/reuse stream_proxy (UDS or TCP)
  cacheclient/          SPCH client (unix or tcp)
  awsutil/              AWS config / identity / S3 client

stream_proxy/           Zig data plane
  src/limits.zig
  src/block_cache.zig
  src/origin.zig
  src/protocol.zig      SPCH framing
  src/spch.zig          Shared request handlers
  src/uds.zig           Unix listen (not built into Windows path)
  src/tcp_spch.zig      TCP listen (--listen-tcp)
  src/proxy.zig         HTTP Range harness
  src/main.zig
```

---

## Platform split

| Concern | Linux | Windows | macOS |
|---|---|---|---|
| Volume API | FUSE3 + go-fuse | WinFsp + cgofuse | Not implemented |
| Mount point | Directory (`/tmp/infinity-storage`) | Drive letter (`Z:`) | — |
| SPCH transport | `--uds /path.sock` | `--listen-tcp 127.0.0.1:PORT` | — |
| Unmount | Ctrl-C / `fusermount3 -u` | Ctrl-C / eject | — |
| Prerequisite | `fuse3` | WinFsp installed | — |

Portable Go packages must not import FUSE or WinFsp. Only `internal/mount/{linux,windows}` and the matching `run_*.go` files do.

`proxypool` defaults: `UseTCP = true` on `GOOS=windows`, else UDS. Override with `Config.UseTCP` in tests.

---

## Read path (multi-file S3)

1. `infinity-storage-mount --bucket B` loads AWS config, `ListObjectsV2` (flat, `Delimiter=/`).
2. Embeds `s3origin` HTTP server on `127.0.0.1:0`.
3. Builds `catalog.Entry` list with `OriginURL = http://127.0.0.1:PORT/object/<name>`.
4. Volume backend exposes names; on **Open**, `proxypool.Acquire` starts (or reuses) one `stream_proxy` per object:
   - `--origin-url … --name … --no-http` plus `--uds` or `--listen-tcp`.
5. **Read** → SPCH READ → Zig cache → on miss, HTTP Range to embedded origin → S3 `GetObject(Range=…)`.
6. Catalog refresh ~2s / on lookup (min 1s); listing is still ListObjects today (moves to meta hub in F).

Caps: 256 files, 4 active proxies, 16 inflight SPCH RPCs per client, Zig
`MAX_CONNECTIONS=32`, 1 MiB × 512 cache, 8 MiB max range.

## Planned live write path (E → F)

1. FUSE or WinFsp reserves a flat pathname before accepting bytes.
2. Go writes accepted bytes to a bounded local ingest spool.
3. The first bytes publish a `streaming` catalog entry and writer source generation.
4. Go uploads fixed multipart parts to S3 in the background.
5. Observer range reads route through an authenticated relay to the writer's available spool ranges.
6. Requests for unwritten ranges wait within a deadline; they never return fabricated zeros.
7. Close seals local ingest while background S3 completion continues.
8. Verified S3 completion atomically routes new ranges to object storage.
9. The writer retains its spool for a handoff grace period, then deletes it.

The API relay carries bounded requested ranges over outbound client connections, allowing devices
behind NAT to communicate. It does not persist another full object. A direct authenticated LAN path
may optimize the same source contract later.

Zig remains the observer read cache. Go owns the ingest spool, live origin, multipart upload,
backpressure, source handoff, and abort. S3 durability controls long-term authority but never gates
initial cross-device visibility or preview.

Full state machine and gates: [`write-sync-edit-plan.md`](write-sync-edit-plan.md).

---

## Package responsibilities

| Package | Owns | Does not own |
|---|---|---|
| `mount` | OS volume attach, Prepare() | Byte cache |
| `catalog` | Name/size/origin URL map | S3 API |
| `s3origin` | List + Range HTTP to S3 | FUSE |
| `proxypool` | Child process lifecycle | Protocol framing |
| `cacheclient` | SPCH dial + round-trip | Spawning Zig |
| `stream_proxy` | Cache, origin I/O, SPCH/HTTP serve | Catalog / auth |

---

## Control plane

Electron sends email/password operations through a narrow main-process IPC bridge. The main process
calls `infinity-storage-api`; auth cookies and database credentials never enter the renderer.
Better Auth owns users, credential accounts, sessions, verification, reset tokens, and rate limits
inside the private `infinity_storage_auth` schema on Supabase Postgres.

## Critical path (after multi-file)

1. **Windows RO mount** — done.
2. **Native app + email/password auth** — scaffolded; database migration pending.
3. **E writes** — bounded sequential multipart upload on Linux and Windows.
4. **F shared library** — account-owned catalog and bidirectional cross-device visibility.
5. **D edit** — NLE-specific random-write, rename, replacement, and lease behavior.

---

## Build cheat sheet

```bash
# Zig (Linux host)
cd stream_proxy && zig build

# Zig → Windows
cd stream_proxy && zig build -Dtarget=x86_64-windows-gnu

# Go mount (Linux)
go build -o infinity-storage-mount ./cmd/infinity-storage-mount

# Go mount (Windows binary from Linux)
CGO_ENABLED=0 GOOS=windows go build -o infinity-storage-mount.exe ./cmd/infinity-storage-mount
```

Tests: `go test ./...` (Linux). Windows FS unit tests compile with `GOOS=windows go test -c ./internal/mount/windows`. TCP SPCH: `proxypool.TestAcquireOverTCP`.
