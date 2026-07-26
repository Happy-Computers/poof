# stream_proxy — simple map

What this thing is: a tiny local server. VLC asks for *pieces* of a video (byte ranges). We read those pieces from a file on disk, keep a small RAM cache, and send them back. The whole multi‑GB file never has to live on your laptop.

```text
VLC  --HTTP Range-->  stream_proxy  --read slices-->  one MP4 file on disk
                           |
                           +-- RAM block cache (fixed size)
```

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

TigerStyle idea: put a limit on everything up front.

### `stream_proxy/src/main.zig`
Doorway into the program.

1. Parse `--file PATH` and `--port N`
2. Open that one file, learn its size
3. Allocate the cache RAM once
4. Hand control to `proxy.zig` to listen on `127.0.0.1`

### `stream_proxy/src/block_cache.zig`
The “smart disk reader.”

- Splits the file into fixed **blocks** (1 MiB)
- Keeps a fixed pool of blocks in RAM
- On a miss: read that block from the file into the pool
- On a hit: reuse RAM (no disk)
- When the pool is full: drop the least-recently-used block
- **Prefetch:** after you read near block N, also load N+1…N+8
- Never holds the lock while writing to the network (avoids freezing other connections)

Also tracks simple counters: bytes from disk, bytes to VLC, hits, misses.

### `stream_proxy/src/proxy.zig`
The HTTP front door VLC talks to.

| URL | What it does |
|---|---|
| `HEAD /video.mp4` | “How big is the file? Do you support Range?” |
| `GET /video.mp4` **with** `Range:` | Send only those bytes as `206 Partial Content` |
| `GET /video.mp4` **without** Range | Reject (`400`) — we always require Range |
| `GET /metrics` | Print the counters (proof we didn’t download everything) |

Flow for a play/scrub:

1. VLC sends something like `Range: bytes=0-` or `Range: bytes=1000000-2000000`
2. We parse that into start/end
3. Cap oversized asks with `MAX_RANGE_BYTES`
4. Ask `block_cache` for those bytes
5. Reply `206` + `Content-Range`
6. Kick prefetch for the next stretch

---

## How a play looks (one sentence each)

1. You start the proxy pointing at one progressive MP4.
2. VLC opens `http://127.0.0.1:PORT/video.mp4`.
3. VLC asks for byte slices, not the whole file.
4. Cache serves hot slices from RAM; cold slices come from disk once.
5. Scrub = new Range near a new offset; old prefetch window is abandoned.

---

## How to run

```bash
export PATH="$HOME/.local/bin:$PATH"
cd stream_proxy
zig build
./zig-out/bin/stream_proxy --file /home/amaan/code/video-storage-engine/data/screencast.mp4 --port 8080
```

Then:

```bash
vlc --avcodec-hw=none http://127.0.0.1:8080/video.mp4
```

(`--avcodec-hw=none` avoids AMD VA-API `get_buffer() failed` / black screen on this machine.)

Proof you didn’t pull the whole library onto disk as a download:

```bash
curl http://127.0.0.1:8080/metrics
```

`bytes_from_origin` should stay in the same ballpark as “what you watched,” not “every file you own,” and cache occupancy is capped.

---

## What this MVP is *not*

- Not a Finder/Explorer mount (no FUSE yet)
- Not multi-user sync
- Not uploads / writes
- Not WebM/ProRes — progressive **MP4 / H.264** with `moov` near the start (`ffmpeg -movflags +faststart`)
