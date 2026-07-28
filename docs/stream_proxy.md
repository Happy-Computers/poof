# stream_proxy — simple map

What this thing is: a tiny local server. VLC asks for *pieces* of a video (byte ranges). We read those pieces from a file on disk, keep a small RAM cache, and send them back. The whole multi‑GB file never has to live on your laptop.

Two fronts share one cache:

```text
VLC  --HTTP Range-->  stream_proxy  --read slices-->  one MP4 file on disk
                           |
space-mount --UDS--------→ +-- RAM block cache (fixed size)
```

HTTP is the step‑A harness. UDS is the step‑B seam for the Go mount.

---

## Files (what each one does)

### `stream_proxy/build.zig`
Build recipe. `zig build` produces `zig-out/bin/stream_proxy`.

### `stream_proxy/build.zig.zon`
Package name / Zig version metadata for the build.

### `stream_proxy/src/limits.zig`
Hard numbers. Nothing grows past these:

| Name | Meaning |
|---|---|
| `BLOCK_SIZE` | Cache pieces are 1 MiB each |
| `CACHE_BLOCKS` | At most 512 pieces in RAM (~512 MiB) |
| `PREFETCH_BLOCKS` | After a read, quietly load ~8 blocks ahead |
| `MAX_RANGE_BYTES` | One client request may ask for at most 8 MiB |
| `LISTEN_BACKLOG` | Kernel accept backlog for HTTP + UDS |
| `MAX_CONNECTIONS` | Cap concurrent HTTP + UDS handlers |
| `MAX_CONCURRENT_ORIGIN_FILLS` | Cap concurrent origin block fills |

TigerStyle idea: put a limit on everything up front.

### `stream_proxy/src/main.zig`
Doorway into the program.

1. Parse `--file PATH` **or** `--origin-url URL`, plus `--name`, `--port`, `--uds`, optional `--no-http`
2. Resolve object size (file stat or HTTP HEAD)
3. Allocate the cache RAM once
4. Serve HTTP and/or UDS on the same `BlockCache`

### `stream_proxy/src/origin.zig`
Pluggable origin for cache fills: `FileOrigin` (local file) or `HttpOrigin` (HTTP Range → e.g. `space-origin` / S3). See [`docs/s3-origin.md`](s3-origin.md).

### `stream_proxy/src/block_cache.zig`
The “smart disk reader.”

- Splits the object into fixed **blocks** (1 MiB)
- Keeps a fixed pool of blocks in RAM
- On a miss: read that block from the **origin** into the pool (mutex not held across origin I/O)
- On a hit: reuse RAM
- When the pool is full: drop the least-recently-used block
- **Prefetch:** after you read near block N, also load N+1…N+8
- Never holds the lock while writing to the client
- `copyRange` (HTTP writer) and `copyRangeToSlice` (UDS buffer)

Also tracks simple counters: bytes from origin, bytes to client, hits, misses.

### `stream_proxy/src/proxy.zig`
The HTTP front door VLC talks to.

| URL | What it does |
|---|---|
| `HEAD /video.mp4` | “How big is the file? Do you support Range?” |
| `GET /video.mp4` **with** `Range:` | Send only those bytes as `206 Partial Content` |
| `GET /video.mp4` **without** Range | Reject (`400`) — we always require Range |
| `GET /metrics` | Print the counters (proof we didn’t download everything) |

### `stream_proxy/src/protocol.zig` + `uds.zig`
Binary Unix-socket protocol for Go (`space-mount`). Ops: SIZE, READ, PREFETCH, METRICS, INFO. See [`docs/space-mount.md`](space-mount.md).

---

## How a play looks (one sentence each)

1. You start the proxy pointing at one progressive MP4.
2. VLC opens `http://127.0.0.1:PORT/video.mp4` **or** a path under the FUSE mount.
3. Client asks for byte slices, not the whole file.
4. Cache serves hot slices from RAM; cold slices come from disk once.
5. Scrub = new offset; old prefetch window is abandoned.

---

## How to run

```bash
export PATH="$HOME/.local/bin:$PATH"
cd stream_proxy
zig build
./zig-out/bin/stream_proxy \
  --file /home/amaan/code/video-storage-engine/data/screencast.mp4 \
  --port 8080 \
  --uds /tmp/space-cache.sock
```

HTTP demo:

```bash
vlc --avcodec-hw=none http://127.0.0.1:8080/video.mp4
```

Mount demo: see [`docs/space-mount.md`](space-mount.md).

(`--avcodec-hw=none` avoids AMD VA-API `get_buffer() failed` / black screen on this machine.)

Proof you didn’t pull the whole library onto disk as a download:

```bash
curl http://127.0.0.1:8080/metrics
```

`bytes_from_origin` should stay in the same ballpark as “what you watched,” not “every file you own,” and cache occupancy is capped.

---

## What this MVP is *not*

- Not multi-user sync
- Not uploads / writes
- Not WebM/ProRes — progressive **MP4 / H.264** with `moov` near the start (`ffmpeg -movflags +faststart`)
- Multi-file Space (still one object per `stream_proxy` process)
