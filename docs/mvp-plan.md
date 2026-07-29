# MVP plan — Infinity Storage (mount + stream)

## Goal

Ship a personal **Infinity Storage**: a folder/drive on the user’s machine that looks local, while file bytes live in our storage.

User opens, scrubs, edits, and exports with **normal apps** (VLC, Resolve, Premiere, Finder). We stream only the bytes the OS asks for. Laptop disk use stays **capped**; library size is not limited by free space.

North-star feel (same category as getspace.so):

> Mount Infinity Storage → apps work as usual → OS requests byte ranges → we fetch/upload those blocks → cloud is source of truth.

Not Dropbox sync. Not “download then open.” **File streaming.**

---

## Success demo (definition of done for the MVP arc)

1. Infinity Storage mounts as a normal folder/drive.
2. Infinity Storage holds far more media than local free disk.
3. Open a large clip → plays in a media player in seconds (no full download).
4. Scrub → playhead moves without a “downloading whole file” wait.
5. (Later in ladder) NLE can open/scrub from the mount.
6. (Later in ladder) Export/save into Infinity Storage; upload finishes in background.
7. Metrics prove: local cache ≪ library size; origin bytes ≈ what was touched.

---

## Strict constraints

These are hard. Do not “just for now” violate them.

### Product
- **One user, one Infinity Storage library, one machine** until step F.
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
| Infinity Storage client mount, S3/SDK glue, write-back orchestration, auth + shared metadata (B→F) | **Go** | Product / control plane: FUSE on Linux (`internal/mount/linux`), WinFsp/cgofuse on Windows (`internal/mount/windows`), boring concurrency and networking. Same shape as JuiceFS / rclone-class clients. |

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

- **Go** `cmd/infinity-storage-mount` presents Infinity Storage as a FUSE filesystem (`hanwen/go-fuse`, `FOPEN_DIRECT_IO`).
- Read path reuses the **Zig** block cache + origin over SPCH/UDS (local file first).
- Still read-only. This is when “the OS decides which bytes” becomes true.
- Docs: [`infinity-storage-mount.md`](infinity-storage-mount.md).

#### S3 origin (read path)
**Win:** Same mount UX; cold bytes come from object storage, not `--file`.

- **Go** `infinity-storage-origin` + Zig `--origin-url`. Docs: [`docs/s3-origin.md`](s3-origin.md).

#### Multi-file Infinity Storage
**Win:** `/tmp/infinity-storage` looks like a **real folder** — many names, list/open any of them (still read-only). Cloud objects appear and stream for preview (no full download).

- **S3 flat list** + live catalog refresh (`infinity-storage-mount --bucket`). Docs: [`infinity-storage-mount.md`](infinity-storage-mount.md), [`s3-origin.md`](s3-origin.md).
- Local `--dir` remains a harness only. `aws s3 cp` is **test-only** seeding, not product ingest.
- Go UDS/TCP client caps inflight RPCs under Zig `MAX_CONNECTIONS` so media players do not drop on open.
- Docs: [`architecture.md`](architecture.md), [`spch.md`](spch.md).

#### Windows RO mount
**Win:** Same Infinity Storage UX on Windows — drive letter (e.g. `Z:`) lists cloud objects; Explorer/VLC stream ranges via WinFsp + Zig cache over TCP SPCH.

- **Go** `internal/mount/windows` (cgofuse / WinFsp); shared `mount.Prepare`.
- `stream_proxy --listen-tcp` (proxypool default on `GOOS=windows`).
- Docs: [`infinity-storage-mount.md`](infinity-storage-mount.md) Windows section, [`architecture.md`](architecture.md), [`spch.md`](spch.md).

### Next (critical path)

Detailed design and test gates: [`write-sync-edit-plan.md`](write-sync-edit-plan.md).

#### Native app (Electron)
**Win:** User installs Infinity Storage, logs in, clicks Mount / Open Explorer — no terminal.

- Thin Electron shell (`desktop/`) over the same `mount.Run` agent (Linux + Windows).
- Better Auth email/password through the separate `infinity-storage-api` service.
- Plain HTML/CSS/JS — no UI framework.
- Docs: [`infinity-storage-desktop.md`](infinity-storage-desktop.md).

#### E — Bounded write into Infinity Storage
**Win:** Copy a new file into either mount; peers serve it while S3 persists it in the background.

- Go retains accepted bytes in a bounded active-ingest spool and uploads fixed multipart parts.
- The writer serves available ranges before S3 completion.
- Unwritten ranges wait within a deadline and never return fabricated zeros.
- New flat files only; overwrite, rename, random writes, and directories wait for D.
- Linux and Windows pass write, peer-range, reopen, hash, crash, and network-failure gates.
- The first harness uses manually configured credentials and source routing.

#### F — Account-owned shared library + second device
**Win:** As soon as a drag starts writing bytes, the file appears on the other mount and previews
from the writer while S3 upload continues in the background.

- Better Auth runs on Supabase Postgres through `infinity-storage-api`.
- Postgres owns libraries, source state, upload reservations, conflicts, and catalog generations.
- The API gives Go short-lived storage authority; permanent AWS credentials leave the clients.
- An authenticated relay routes observer ranges to the writer through NAT.
- Linux writes one file and Windows previews it before S3 completion, then the direction reverses.
- Verified S3 completion atomically moves range authority from the writer to object storage.
- Discovery p95 targets below 1 second; peer preview targets below 2 seconds when ranges exist.

#### D — Edit from the mount (proxies OK)
**Win:** Resolve or Premiere opens, scrubs, and saves proxy/compressed media through the mount.

- Add observed NLE semantics: random writes, truncate, temp-file rename, replacement, and leases.
- Invalidate cached versions explicitly; never silently accept conflicting writers.
- Camera RAW and large ProRes remain outside the first D proof.

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
| Multi-file Infinity Storage | Done (S3 live catalog) |
| Windows RO mount | Done (WinFsp/cgofuse + TCP SPCH) |
| Native app | Done (Electron email/password + mount shell) |
| E | **Next** — bounded ingest spool, live peer ranges, and background S3 upload |
| F | Auth scaffolded; add account-owned rendezvous, relay, and source generations |
| D | After E durability and F cross-device visibility pass |
| C | Opportunistic |

**Next implementation slice:** build the bounded Go ingest spool plus multipart uploader and live
range origin, then connect Linux FUSE and Windows WinFsp create/write/flush/release operations.

---

## Mental model

```text
[Apps: VLC / NLE / Explorer / Finder]
        │ normal open / read / write
        ▼
[Writer mount — Go]
  • bounded active-ingest spool
  • S3 multipart upload in background
  • live range origin until durable
        │ authenticated range relay
        ▼
[Observer mount — Go → Zig cache]
  • streams only requested available ranges

After verified S3 completion:

[Observer mount — Go → Zig cache] → range GET → [Object storage]
```

Step A proved the Zig **read** path over HTTP.
Steps B+ put a Go mount in front so every app becomes a client without knowing Infinity Storage exists.
Windows uses the same Prepare/proxypool path with WinFsp + TCP SPCH. Full map: [`architecture.md`](architecture.md).
