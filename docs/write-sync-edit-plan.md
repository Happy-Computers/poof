# Write → shared library → edit plan

## Why this order

The read path is already proven on Linux and Windows. The next uncertainty is whether a normal
filesystem write can become durable in S3 while the writer continues to read it through the mount.
We will prove bounded ingest first, then the filesystem behavior real NLEs require on one machine.
Account ownership, relay, and cross-device source handoff follow once the local storage semantics
are proven.

Execution order:

1. **E — bounded ingest, local live range origin, and background S3 upload**
2. **D — edit from the mount on one machine**
3. **F — Yave-authorized shared library, relay, and source handoff**

Cross-device "sync" in F means every mount sees one live catalog entry and reads requested ranges
from the current source. It does not mean each machine downloads a local copy.

## E — bounded write path

### Demo win

Copy a new file into the Linux or Windows mount. The writer can reopen and read accepted ranges
from its bounded spool while it uploads the same bytes to S3 in the background. After S3 becomes
durable, new reads switch to S3 without changing the visible file.

### Immediate availability promise

- The writer keeps a bounded local ingest spool and serves its own available byte ranges from it.
- A request for a range not written yet waits within a fixed deadline instead of returning zeros.
- Progressive files may preview as soon as their header and requested media ranges are available.
- S3 multipart upload runs concurrently and never gates local catalog visibility or preview.
- After S3 verifies the complete object, new reads switch atomically to the S3 ranged-read path.
- The writer retains its spool for a bounded local handoff grace period, then deletes it.
- F later makes the same available ranges visible to other authorized mounts; it never synchronizes
  the complete file.

The file can appear immediately, but no system can serve bytes the drag has not written yet. Format
layout therefore controls how soon preview can begin. Progressive MP4 is the first proof format.

### V1 write contract

- New, flat files only.
- One writer per pathname.
- Writes must be sequential: each offset must equal the accepted byte count.
- Existing files cannot be overwritten, truncated, renamed, or deleted.
- Directories and sparse files are unsupported.
- A `streaming` entry is visible in the writer mount before S3 completion.
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
2. Add a local range origin that serves only ranges present in the spool.
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
| Linux | progressive MP4 | 1–5 GiB | reopen and preview while S3 is delayed |
| Windows | small text | 1 KiB | close, list, read, SHA-256 match |
| Windows | binary crossing parts | 40 MiB | at least 3 parts, SHA-256 match |
| Windows | progressive MP4 | 1–5 GiB | reopen and preview while S3 is delayed |

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

## D — edit from the mount

D starts after E passes its Linux and Windows durability gates. It proves one mounted library on one
machine; it does not depend on Yave accounts, a relay, or a second device.

Add only the filesystem behaviors observed from target applications:

- Random and overlapping writes.
- Truncate and overwrite semantics.
- Temporary-file plus atomic-rename save patterns.
- Version replacement and cache invalidation.
- One local writer lease per pathname with explicit expiry.
- Explicit failure for a competing local writer.

Test Resolve and Premiere with proxy or compressed media first. Trace actual filesystem operations,
add one missing semantic at a time, and retain the durability and integrity gates. An edit or export
must use bounded cache and bounded staging; it never requires a full local copy, but it cannot claim
zero local bytes. Camera RAW and large ProRes remain outside the first D proof.

## F — Yave-authorized shared library

F starts only after D proves that one mount can safely edit and export. Yave is the identity provider:
it issues short-lived bearer tokens scoped to a user, library, device, expiry, and allowed operation.
The storage engine verifies those tokens; it has no separate account or login service.

1. Go reserves a pathname against the account-owned library catalog.
2. The writer opens an authorized outbound relay channel and publishes a source generation.
3. Observer range requests route to the writer's bounded spool until S3 is verified.
4. Verified S3 completion atomically routes new ranges to object storage.
5. Per-file leases become cross-device and competing writes report an explicit conflict.

Use one Yave account and one library mounted on Linux and Windows. Throttle S3, verify that each
machine can preview the other's in-progress upload, verify handoff during playback, and verify that
same-path writes produce one winner and one explicit conflict. Record discovery latency, peer-preview
latency, durability latency, handoff gap, served bytes, and SHA-256 for every run.

## Stop conditions

Do not begin D until E passes its Linux and Windows durability gates. Do not begin F until D passes
its single-machine NLE open, save, export, reopen, and SHA-256 gates. Any corruption, unbounded
staging, visible partial object, or silent conflict stops the ladder and is fixed before moving
forward.
