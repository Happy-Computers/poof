# Write → shared library → edit plan

## Why this order

The read path is already proven on Linux and Windows. The next uncertainty is whether a normal
filesystem write can be served immediately from the writer while the same bytes become durable in
S3 in the background. We will prove bounded ingest and peer-range serving first, then put account
ownership and rendezvous around it, then test NLE behavior.

Execution order:

1. **E — bounded ingest, live range origin, and background S3 upload**
2. **F — account-owned rendezvous, relay, and source handoff**
3. **D — edit from the mount**

Cross-device "sync" means every mount sees one live catalog entry and reads requested ranges from
the current source. It does not mean each machine downloads a local copy.

## E — bounded write path

### Demo win

Copy a new file into the Linux or Windows mount. The first accepted write publishes a `streaming`
entry to every other active mount. Observers request preview ranges from the writer while the writer
uploads the same bytes to S3 in the background. After S3 becomes durable, routing switches from the
writer to S3 without changing the visible file.

### Immediate availability promise

- The file appears on other active mounts after its first bytes enter the writer mount.
- The writer keeps a bounded local ingest spool and serves available byte ranges from it.
- A request for a range not written yet waits within a fixed deadline instead of returning zeros.
- Progressive files may preview as soon as their header and requested media ranges are available.
- S3 multipart upload runs concurrently and never gates initial catalog visibility or peer preview.
- After S3 verifies the complete object, new reads switch atomically to the S3 ranged-read path.
- The writer retains its spool for a handoff grace period, then deletes it.
- Observers stream only requested ranges; they never synchronize the complete file.

The file can appear immediately, but no system can serve bytes the drag has not written yet. Format
layout therefore controls how soon preview can begin. Progressive MP4 is the first proof format.

### V1 write contract

- New, flat files only.
- One writer per pathname.
- Writes must be sequential: each offset must equal the accepted byte count.
- Existing files cannot be overwritten, truncated, renamed, or deleted.
- Directories and sparse files are unsupported.
- A `streaming` entry is visible on every active mount before S3 completion.
- Available ranges come from the writer's spool; unwritten ranges wait within a fixed deadline.
- `flush`, `fsync`, and close report local ingest failures where the OS API permits.
- S3 failure does not hide a healthy writer source; durability remains visibly pending.
- A verified S3 object becomes the source of truth and enters the normal Zig read path.
- Out-of-order writes fail explicitly and abort the ingest.

These limits prove product ingest without pretending to support NLE save patterns before step D.

### Upload state machine

```text
reserved → streaming → sealing → durable
               │           │
               ├→ interrupted
               └───────────┴→ aborting → aborted
```

- `reserved`: pathname conflict is rejected before bytes are accepted.
- `streaming`: the writer is the live range origin while S3 parts upload in the background.
- `sealing`: local writing ended; remaining S3 work and checksum verification continue.
- `durable`: S3 is verified and becomes the authoritative range origin.
- `interrupted`: the writer vanished before durability; the file remains known but unavailable.
- `aborting`: an explicit cancellation stops local ingest and the multipart upload.
- `aborted`: no readable source remains and the catalog tombstones the entry.

Catalog publication happens at `streaming`, not `durable`. Source transitions remain monotonic, and
each catalog generation identifies exactly one authoritative origin.

### Initial hard limits

| Resource | Limit |
|---|---:|
| Active write handles | 2 |
| Multipart part size | 16 MiB |
| Buffers per active write | 2 |
| Total write-buffer memory | 64 MiB |
| Local ingest spool | 64 GiB across all active writes |
| Parts per object | 4,096 |
| File size | 64 GiB |
| Concurrent peer range reads | 8 |
| Peer range size | 8 MiB |
| Unwritten-range wait | 30 seconds |
| Source handoff grace | 30 seconds |
| Part upload attempts | 3 |
| Flat files in catalog | 256 |
| UTF-8 basename | 255 bytes |

Arbitrary preview ranges require retaining accepted bytes until S3 is readable, so E uses a bounded
on-disk ingest spool. The bound applies to active writes, never the whole library. Backpressure
stops filesystem writes before the spool limit is exceeded.

### Crash behavior

- A writer crash before S3 durability changes the catalog entry to `interrupted`.
- Observers never receive fabricated zeros or stale bytes for unavailable ranges.
- Restart may resume from the spool and existing multipart upload; otherwise the user aborts it.
- Graceful shutdown rejects new writes and attempts durability within a fixed deadline.
- The bucket aborts abandoned multipart uploads after one day through a lifecycle rule.
- The writer deletes its spool only after verified S3 handoff and the grace period.

### Implementation slices

1. Add a bounded Go ingest spool and multipart uploader with the source state machine.
2. Add an authenticated range origin that serves only ranges present in the spool.
3. Add create/write/flush/release operations to the Linux FUSE backend.
4. Add equivalent operations to the Windows WinFsp backend.
5. Publish `streaming` catalog entries and source generations before S3 completion.
6. Switch observers to S3 after checksum verification without interrupting open reads.
7. Expose streaming, durability, interruption, and failure state through Electron.

The initial E harness may use the same manually configured bucket and AWS credentials as the current
read demo. Permanent credentials are removed from clients during F.

## E test gate

E is complete only when all checks pass on both operating systems.

### Single-device matrix

| Writer | File | Size | Required result |
|---|---|---:|---|
| Linux | small text | 1 KiB | close, list, read, SHA-256 match |
| Linux | binary crossing parts | 40 MiB | at least 3 parts, SHA-256 match |
| Linux | progressive MP4 | 1–5 GiB | peer-preview while S3 is delayed |
| Windows | small text | 1 KiB | close, list, read, SHA-256 match |
| Windows | binary crossing parts | 40 MiB | at least 3 parts, SHA-256 match |
| Windows | progressive MP4 | 1–5 GiB | peer-preview while S3 is delayed |

### Failure matrix

- Write with a non-sequential offset.
- Create a name that already exists.
- Exceed the active-write limit.
- Lose the network during `UploadPart`.
- Kill the mount before completion.
- Fail `CompleteMultipartUpload`.
- Unmount with active writes.

A streaming file may remain visible as `interrupted`, but reads must return only accepted bytes or a
truthful unavailable error. Fabricated zeros, stale ranges, silent truncation, and hash mismatch
fail the gate.

## F — account-owned shared library

### User flow

```text
create account → verify email → sign in → choose mount location
       → choose/create library → mount → files appear
```

The installed Electron application launches compiled Go and Zig binaries. It never creates a Go
module. The user chooses only a local mount location; bucket details and permanent cloud credentials
stay behind `infinity-storage-api`.

### Storage model

Start with one managed bucket and one immutable prefix per library:

```text
s3://infinity-storage-data/libraries/<library_id>/objects/<object_id>
```

Postgres maps display names to object IDs. S3 keys do not depend on mutable display names.

### Live source authority

1. Go asks the API to reserve a pathname for the authenticated library.
2. Postgres creates a `reserved` entry and rejects conflicts atomically.
3. The writer opens an authenticated outbound relay channel and starts its bounded spool.
4. The first accepted bytes move the entry to `streaming` and publish a source generation.
5. Observer range requests route through the relay to the writer's spool.
6. Go uploads the same accepted bytes to S3 concurrently.
7. After close, the API verifies size and checksum, then atomically routes new reads to S3.
8. The writer keeps serving existing reads through the handoff grace period.

The outbound relay allows devices behind NAT to communicate without exposing an unauthenticated
listener. The API relays bounded requested ranges but does not persist a second full object. A
direct authenticated peer path may optimize same-LAN traffic later without changing source
semantics.
Retries use an idempotency key so reconnects cannot publish duplicate files.

### Desktop mount flow

1. Better Auth restores the session in Electron's main process.
2. Electron lists the user's libraries through the API.
3. The user selects an empty Linux directory or free Windows drive letter.
4. Electron starts `infinity-storage-mount` with the API origin, library ID, and session handoff.
5. Go retrieves catalog metadata and short-lived storage authority from the API.
6. FUSE or WinFsp attaches the virtual filesystem.
7. Reads use Zig ranged caching; writes use Go multipart upload.

The renderer never receives database credentials, permanent AWS credentials, or raw auth cookies.

## Cross-device visibility gate

Use one account and one library mounted simultaneously on Linux and Windows.

1. Throttle S3, then drag `linux-video.mp4` into Linux; Windows must list and preview from Linux.
2. Reverse the test with `windows-audio.wav`; Linux must read available ranges from Windows.
3. Finish S3 upload during playback; the source handoff must not interrupt or corrupt the read.
4. Write different 40 MiB files concurrently; both catalogs must converge and hashes must match.
5. Disconnect the writer before S3 durability; the observer must show `interrupted`, never zeros.
6. Attempt the same pathname concurrently; exactly one reservation must win.

Measure four intervals separately:

- **Discovery latency:** first accepted write to the observer listing the `streaming` file.
- **Peer-preview latency:** first accepted write to playback beginning from writer-served ranges.
- **Durability latency:** first accepted write to verified S3 completion.
- **Handoff gap:** longest stalled read while authority moves from the writer to S3.

Initial targets: discovery p95 below 1 second and progressive peer preview below 2 seconds once
required ranges exist. Require no measurable handoff error. Record file size, platform, network,
source selected, latencies, served bytes, and SHA-256 result for every run.

## D — edit from the mount

D starts after E durability and F cross-device visibility pass.

Add only the filesystem behaviors observed from target applications:

- Random and overlapping writes.
- Truncate and overwrite semantics.
- Temporary-file plus atomic-rename save patterns.
- Version replacement and cache invalidation.
- Per-file writer leases with explicit expiry.
- Conflict reporting instead of silent last-writer-wins.

Test Resolve and Premiere with proxy or compressed media first. Trace actual filesystem operations,
add one missing semantic at a time, and retain the same durability and cross-device integrity gates.
Camera RAW and large ProRes remain outside the first D proof.

## Stop conditions

Do not begin F catalog wiring until E passes its Linux and Windows durability gates. Do not begin D
until the same authenticated library passes the cross-device visibility gate. Any corruption,
unbounded staging, visible partial object, or silent conflict stops the ladder and is fixed before
moving forward.
