# MVP plan — Space (mount + stream)

## Goal

Ship a personal **Space**: a folder/drive on the user’s machine that looks local, while file bytes live in our storage.

User opens, scrubs, edits, and exports with **normal apps** (VLC, Resolve, Premiere, Finder). We stream only the bytes the OS asks for. Laptop disk use stays **capped**; library size is not limited by free space.

North-star feel (same category as getspace.so):

> Mount Space → apps work as usual → OS requests byte ranges → we fetch/upload those blocks → cloud is source of truth.

Not Dropbox sync. Not “download then open.” **File streaming.**

---

## Success demo (definition of done for the MVP arc)

1. Space mounts as a normal folder/drive.
2. Space holds far more media than local free disk.
3. Open a large clip → plays in a media player in seconds (no full download).
4. Scrub → playhead moves without a “downloading whole file” wait.
5. (Later in ladder) NLE can open/scrub from the mount.
6. (Later in ladder) Export/save into Space; upload finishes in background.
7. Metrics prove: local cache ≪ library size; origin bytes ≈ what was touched.

---

## Strict constraints

These are hard. Do not “just for now” violate them.

### Product
- **One user, one Space, one machine** until step F.
- **No team sync / locking / version merge** before F.
- **No offline-first promise.** Uncached bytes need network. Pinning is later.
- **Do not claim true zero disk.** Claim **bounded cache**; implement bounded cache.
- **Progressive media for early steps:** MP4 / H.264 with `moov` near start. Camera RAW / huge ProRes is out until streaming is solid.
- **No Aspect/Frame.io feature pile:** no review comments, AI, MAM, web player as MVP scope.

### Architecture
- **Source of truth = remote object store** (local file only as a stand-in before S3).
- **Apps talk to a filesystem mount**, not a custom SDK (after step B). HTTP Range proxy is a test harness / earlier rung, not the end UX.
- **Block cache is fixed-size** (see `stream_proxy` limits). File size never sizes the cache.
- **Every resource has a limit:** block size, cache blocks, prefetch window, max range, max open files, max concurrent origin fetches.
- **GET-style reads are range-oriented.** No unbounded “send whole object” on the hot path.
- **Never hold cache locks across network I/O.**
- **TigerStyle** (`TIGERSTYLE.md`) applies to design and implementation.

### Explicit non-goals (for this MVP arc)
- Multi-tenant billing, web dashboard polish, mobile clients.
- Full POSIX perfection on day one (good enough for media open/scrub/save).
- Competing on GTM copy with getspace.so — we care about the working system.

---

## Languages (Zig + Go)

We are not limited to one language. Each layer uses what it’s best at. Polyglot only at clear seams — not three languages on the hot path.

| Layer | Language | Why |
|---|---|---|
| Range I/O, fixed block cache, prefetch, hard limits (step A core) | **Zig** | Systems / data plane: fixed RAM, no GC on the cache path, TigerStyle caps. Already proven in `stream_proxy`. |
| Space client mount, S3/SDK glue, write-back orchestration, auth + shared metadata (B→F) | **Go** | Product / control plane: mature FUSE ecosystem, boring concurrency and networking, natural fit when step F becomes multi-device. Same shape as JuiceFS / rclone-class clients. |

**Rules of thumb**
- **Zig owns bytes in the cache.** File size never sizes the cache; limits stay explicit.
- **Go owns the mount and everything that talks to users, cloud APIs, and other machines.**
- Do not rewrite the Zig cache into Go “because distributed.” Step F is metadata + auth; the cache problem stays a systems problem.
- Do not put Python (or similar) on the mount/cache hot path. Fine for scripts and harnesses only.
- Rust is a later option only if we need one memory-safe binary for mount+cache and GC becomes a measured problem — not the default now.

**Seam:** Go mount (or client) asks the Zig data plane for ranged bytes (local process boundary / IPC / library boundary — pick the simplest that keeps locks off the network path). HTTP Range proxy remains a test harness from step A, not the end UX.

---

## Build ladder (A → F)

Each step must be **demoable** before starting the next. Do not skip.

### A — Range + cache (done)
**Win:** Play via HTTP without downloading the whole file.

- Zig `stream_proxy`: Range GET, fixed RAM block cache, prefetch, metrics.
- Origin: one local file (stand-in for object storage).
- Proof: VLC (prefer `--avcodec-hw=none` on AMD), `/metrics` moves, bytes ≪ “whole library.”
- Handoff: UDS binary protocol (`--uds`) + hard caps (`MAX_CONNECTIONS`, `MAX_CONCURRENT_ORIGIN_FILLS`).

### B — Mount read-only (done for local-file demo)
**Win:** A real folder/drive appears; double-click opens in normal apps.

- **Go** `cmd/space-mount` presents Space as a FUSE filesystem (`hanwen/go-fuse`, `FOPEN_DIRECT_IO`).
- Read path reuses the **Zig** block cache + origin over UDS (local file first).
- Still read-only. This is when “the OS decides which bytes” becomes true.
- Docs: [`docs/space-mount.md`](space-mount.md).

### C — Scrub feels good
**Win:** Seek/scrub on typical wifi does not feel broken.

- Prefetch + cache tuning (and disk-backed cache if needed).
- Keep hard caps; measure hits/misses and stall rate.

### D — Edit from the mount (proxies OK)
**Win:** Drop media into Resolve/Premiere from Space; scrub works.

- Same read path under NLE I/O (many small reads).
- MVP editing assumes **proxy / compressed** media, not raw camera files.

### E — Export / write into Space
**Win:** Save or export into the Space folder; file shows up; upload completes in background.

- **Go** write-back orchestration → multipart/object upload (cache caps still apply).
- Crash/partial-upload behavior defined and bounded.
- Cloud remains source of truth once durable.

### F — Second device, same Space
**Win:** Another machine mounts the same Space and sees the files.

- **Go** shared metadata + auth.
- Still keep sync semantics simple; no deep merge/locking product yet.

---

## Current position

| Step | Status |
|---|---|
| A | Done (`stream_proxy` HTTP + UDS + docs/stream_proxy.md) |
| B | Done for RO mount (`cmd/space-mount` + docs/space-mount.md) |
| S3 origin (read path) | Done (`cmd/space-origin` + Zig `--origin-url` + docs/s3-origin.md) |
| C–F | Not started (scrub tuning, NLE, writes, multi-device) |

**Next implementation slice when we resume:** **C — scrub feels good** under real S3 latency, and/or multi-file listing before writes (E).

---

## Mental model

```text
[Apps: VLC / NLE / Finder]
        │ normal open / read / write
        ▼
[Space client — Go]
  • mount (FUSE / platform)
  • metadata (names, sizes)
  • write-back orchestration (from E)
  • auth + multi-device (from F)
        │ ranged byte requests
        ▼
[Data plane — Zig]
  • fixed block cache + prefetch
  • hard limits (never sized by file)
        │ range get / put parts
        ▼
[Object storage]
```

Step A proved the Zig **read** path over HTTP.  
Steps B+ put a Go mount in front so every app becomes a client without knowing Space exists.
