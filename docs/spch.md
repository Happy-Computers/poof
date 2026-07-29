# SPCH — Space Cache Protocol

Binary protocol between **Go** (`internal/cacheclient`, `proxypool`) and **Zig** (`stream_proxy`).  
Same framing on Unix domain sockets and TCP. Not HTTP.

Magic ASCII: `SPCH` (`0x53504348`). Version: `1`.

---

## Transports

| OS | Flag on `stream_proxy` | Go dial |
|---|---|---|
| Linux (default) | `--uds /path/to.sock` | `cacheclient.New(path)` → `unix` |
| Windows (default pool) | `--listen-tcp 127.0.0.1:PORT` | `cacheclient.NewTCP("127.0.0.1:PORT")` |
| Either (manual) | either flag | `New` / `NewTCP` / `NewNetwork` |

`--uds` and `--listen-tcp` are mutually exclusive.  
`--uds` is rejected on Windows builds.  
`--no-http` disables the HTTP Range front door (normal for proxypool children).

Proxypool on Windows: bind `127.0.0.1:0`, close, pass that port to Zig, poll `Size()` until ready.

Zig sources: [`protocol.zig`](../stream_proxy/src/protocol.zig), [`spch.zig`](../stream_proxy/src/spch.zig), [`uds.zig`](../stream_proxy/src/uds.zig), [`tcp_spch.zig`](../stream_proxy/src/tcp_spch.zig).

---

## Request (24 bytes, little-endian)

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | magic `SPCH` |
| 4 | 2 | version `1` |
| 6 | 2 | op |
| 8 | 8 | offset (READ) |
| 16 | 4 | length (READ) |
| 20 | 4 | reserved `0` |

### Ops

| Code | Name | Request body | Response payload |
|---|---|---|---|
| 1 | SIZE | — | 8-byte `u64` object size |
| 2 | READ | offset + length | raw bytes (≤ `MAX_RANGE_BYTES` = 8 MiB) |
| 3 | PREFETCH | — | empty |
| 4 | METRICS | — | 64-byte metrics blob |
| 5 | INFO | — | object basename (UTF-8) |

One connection = one RPC today (client dials per call). Inflight dials per `Client` capped at `MaxInflight=16` (must stay &lt; Zig `MAX_CONNECTIONS=32`).

---

## Response header (8 bytes, little-endian)

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | status |
| 4 | 4 | nbytes |
| 8… | nbytes | payload |

### Status codes

| Code | Meaning |
|---|---|
| 0 | OK |
| 1 | invalid request |
| 2 | range not satisfiable |
| 3 | range too large |
| 4 | origin I/O error |
| 5 | busy |

### Metrics payload (64 bytes)

Eight `u64` LE fields in order:

`bytes_from_origin`, `bytes_to_client`, `cache_hits`, `cache_misses`, `cache_occupancy_blocks`, `prefetch_bytes`, `prefetch_cancelled`, `object_size`.

---

## Examples

Linux child (proxypool):

```bash
stream_proxy --file ./clip.mp4 --name clip.mp4 --uds /tmp/p.sock --no-http
```

Windows / TCP:

```bash
stream_proxy --origin-url http://127.0.0.1:9090/object/clip.mp4 \
  --name clip.mp4 --listen-tcp 127.0.0.1:9191 --no-http
```

Go:

```go
c := cacheclient.NewTCP("127.0.0.1:9191")
size, err := c.Size()
n, err := c.ReadAt(buf, offset)
```

Manual mount against TCP SPCH:

```bash
space-mount --mount Z: --uds 127.0.0.1:9191   # Windows
space-mount --mount /tmp/space --uds /tmp/p.sock  # Linux UDS
```

(`--uds` CLI flag accepts a Unix path **or** `host:port`; Prepare() picks TCP when `net.SplitHostPort` succeeds.)
