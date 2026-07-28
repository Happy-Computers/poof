# space-mount — read-only Space (step B)

Go FUSE client. Apps see a normal folder; reads go to the Zig block cache over a Unix socket.

```text
VLC / Finder
    │ open / read
    ▼
space-mount (Go, hanwen/go-fuse, DIRECT_IO)
    │ UDS SIZE / READ / INFO
    ▼
stream_proxy (Zig cache + local origin file)
```

## Prerequisites

- `fuse3` installed (`fusermount3`)
- Zig `stream_proxy` built (`cd stream_proxy && zig build`)
- Go 1.21+

## Run

Terminal 1 — Zig data plane (HTTP harness optional; UDS required for mount):

```bash
export PATH="$HOME/.local/bin:$PATH"
cd stream_proxy
./zig-out/bin/stream_proxy \
  --file /home/amaan/code/video-storage-engine/data/screencast.mp4 \
  --port 8080 \
  --uds /tmp/space-cache.sock
```

Terminal 2 — mount:

```bash
mkdir -p /tmp/space
go run ./cmd/space-mount --mount /tmp/space --uds /tmp/space-cache.sock
```

Play:

```bash
vlc --avcodec-hw=none /tmp/space/screencast.mp4
```

Unmount: `Ctrl-C` on `space-mount`, or `fusermount3 -u /tmp/space`.

Proof (cache still bounded; origin bytes ≈ what was touched):

```bash
curl http://127.0.0.1:8080/metrics
```

## UDS protocol (brief)

Little-endian. Request = 24 bytes: `magic=0x53504348`, `version=1`, `op`, `offset`, `length`, `reserved`.

| Op | Meaning |
|---|---|
| 1 SIZE | → u64 object size |
| 2 READ | offset+length → bytes (capped by Zig `MAX_RANGE_BYTES`) |
| 3 PREFETCH | kick prefetch window |
| 4 METRICS | fixed 8×u64 counters |
| 5 INFO | object basename for the mount file name |

Response = `status u32` + `nbytes u32` + payload.

## Limits

Same Zig caps as step A (`limits.zig`): 1 MiB blocks, 512 cache slots, 8 MiB max range, 32 connections, 4 concurrent origin fills.
