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
| Space client mount, S3/SDK glue, write-back orchestration, auth + shared metadata (B→F) | **Go** | Product / control plane: FUSE on Linux (`internal/mount/linux`), WinFsp/cgofuse on Windows (`internal/mount/windows`), boring concurrency and networking. Same shape as JuiceFS / rclone-class clients. |

**Rules of thumb**
- **Zig owns bytes in the cache.** File size never sizes the cache; limits stay explicit.
- **Go owns the mount and everything that talks to users, cloud APIs, and other machines.**
- Do not rewrite the Zig cache into Go “because distributed.” Step F is metadata + auth; the cache problem stays a systems problem.
- Do not put Python (or similar) on the mount/cache hot path. Fine for scripts and harnesses only.
- Rust is a later option only if we need one memory-safe binary for mount+cache and GC becomes a measured problem — not the default now.

**Seam:** Go mount asks Zig for ranged bytes over **SPCH** ([`spch.md`](spch.md)) — UDS on Linux, TCP localhost on Windows. HTTP Range proxy remains a test harness from step A, not the end UX. Map: [`architecture.md`](architecture.md).

---

## Build ladder

Each step must be **demoable** before starting the next on the critical path. Letter labels are historical; **execution order is below**.

### Done

#### A — Range + cache
**Win:** Play via HTTP without downloading the whole file.

- Zig `stream_proxy`: Range GET, fixed RAM block cache, prefetch, metrics.
- Origin: one local file (stand-in for object storage).
- Proof: VLC (prefer `--avcodec-hw=none` on AMD), `/metrics` moves, bytes ≪ “whole library.”
- Handoff: SPCH binary protocol (`--uds` / later `--listen-tcp`) + hard caps (`MAX_CONNECTIONS`, `MAX_CONCURRENT_ORIGIN_FILLS`). Docs: [`spch.md`](spch.md).

#### B — Mount read-only
**Win:** A real folder/drive appears; double-click opens in normal apps.

- **Go** `cmd/space-mount` presents Space as a FUSE filesystem (`hanwen/go-fuse`, `FOPEN_DIRECT_IO`).
- Read path reuses the **Zig** block cache + origin over SPCH/UDS (local file first).
- Still read-only. This is when “the OS decides which bytes” becomes true.
- Docs: [`space-mount.md`](space-mount.md).

#### S3 origin (read path)
**Win:** Same mount UX; cold bytes come from object storage, not `--file`.

- **Go** `space-origin` + Zig `--origin-url`. Docs: [`docs/s3-origin.md`](s3-origin.md).

#### Multi-file Space
**Win:** `/tmp/space` looks like a **real folder** — many names, list/open any of them (still read-only). Cloud objects appear and stream for preview (no full download).

- **S3 flat list** + live catalog refresh (`space-mount --bucket`). Docs: [`space-mount.md`](space-mount.md), [`s3-origin.md`](s3-origin.md).
- Local `--dir` remains a harness only. `aws s3 cp` is **test-only** seeding, not product ingest.
- Go UDS/TCP client caps inflight RPCs under Zig `MAX_CONNECTIONS` so media players do not drop on open.
- Docs: [`architecture.md`](architecture.md), [`spch.md`](spch.md).

#### Windows RO mount
**Win:** Same Space UX on Windows — drive letter (e.g. `Z:`) lists cloud objects; Explorer/VLC stream ranges via WinFsp + Zig cache over TCP SPCH.

- **Go** `internal/mount/windows` (cgofuse / WinFsp); shared `mount.Prepare`.
- `stream_proxy --listen-tcp` (proxypool default on `GOOS=windows`).
- Docs: [`space-mount.md`](space-mount.md) Windows section, [`architecture.md`](architecture.md), [`spch.md`](spch.md).

### Next (critical path)

#### Native app (language TBD)
**Win:** User installs Space, logs in later, clicks Mount / Open Explorer — no terminal.

- Thin shell over the same `mount.Run` agent (Linux + Windows).
- Language chosen when UI work starts.

#### F — Auth + shared metadata + second device
**Win:** Linux + Windows mounts share one catalog within ~1s; bytes still stream from S3.

- Supabase Auth + Postgres catalog (public meta API).
- Mount polls/subscribes meta (listing no longer polls `ListObjects` on the hot path).
- Dual-device proof: one Linux + one Windows against the same Space.
- Still keep sync semantics simple; no deep merge/locking. Writes not required for this demo.

#### E — Export / write into Space
**Win:** Drag or save into the Space folder; file shows up; upload finishes in background.

- **Go** write-back orchestration → multipart/object upload (cache caps still apply).
- Crash/partial-upload behavior defined and bounded.
- Cloud remains source of truth once durable.
- Real product ingest (users never upload to S3 directly). After F’s metadata hub, register files on write.

#### D — Edit from the mount (proxies OK)
**Win:** Drop media into Resolve/Premiere from Space; scrub/edit works.

- Same read (and later write) path under NLE I/O (many small reads).
- MVP editing assumes **proxy / compressed** media, not raw camera files.

### Parallel / as needed

#### C — Scrub feels good
**Win:** Seek/scrub on typical wifi does not feel broken.

- Prefetch + cache tuning (and disk-backed cache if needed).
- Already **somewhat works** on progressive media; tune when seeks feel bad under real S3 latency.

---

## Current position

| Step | Status |
|---|---|
| A | Done |
| B | Done (RO mount, Linux) |
| S3 origin (read) | Done |
| Multi-file Space | Done (S3 live catalog) |
| Windows RO mount | Done (WinFsp/cgofuse + TCP SPCH) |
| Native app | **Next** (language TBD) |
| F | After native shell — Supabase auth + Postgres catalog; Linux+Windows dual-device |
| E | After F (writes / bg upload) |
| D | After E |
| C | Opportunistic |

**Next implementation slice:** native app shell (language TBD), then **F** (Supabase auth + Postgres catalog) and Linux+Windows dual-device test.

---

## Mental model

```text
[Apps: VLC / NLE / Explorer / Finder]
        │ normal open / read / write
        ▼
[Space client — Go]
  • mount (FUSE Linux / WinFsp Windows)
  • metadata (names, sizes)
  • write-back orchestration (from E)
  • auth + multi-device (from F)
        │ SPCH ranged byte requests (UDS or TCP)
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
Windows uses the same Prepare/proxypool path with WinFsp + TCP SPCH. Full map: [`architecture.md`](architecture.md).
