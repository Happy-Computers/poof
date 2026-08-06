# Linux ↔ Windows live relay demo

The relay publishes a `streaming` catalog entry after the writer accepts its first bytes and relays only requested ranges to the writer spool. S3 uploads concurrently; verified S3 objects become the source for new opens.

## Shared demo configuration

| Value | Demo value | Rule |
|---|---|---|
| Relay URL | `http://RELAY_HOST:8080` | Reachable from both devices. Use HTTPS outside a trusted LAN. |
| Library ID | `demo-library` | Identical on both devices. |
| Token file | `docs/relay-token.local.md` | Ignored by Git; copy the same file to relay host and Windows. |
| Bucket/prefix | Your existing S3 location | Identical on both devices. |

`docs/relay-token.local.md` contains the preconfigured development bearer token. It is intentionally ignored and must never be committed, pasted into a shell argument, or reused outside this demo.

## 1. Start the relay

Run on a host reachable from Linux and Windows:

```bash
cd /path/to/video-storage-engine
go build -o infinity-storage-relay ./cmd/infinity-storage-relay
./infinity-storage-relay \
  --listen 0.0.0.0:8080 \
  --token-file docs/relay-token.local.md \
  --debug 2>&1 | tee relay.log
```

## 2. Build the clients

| Linux | Windows PowerShell |
|---|---|
| `cd stream_proxy && zig build && cd ..` | `cd stream_proxy; zig build -Dtarget=x86_64-windows-gnu; cd ..` |
| `go build -o infinity-storage-mount ./cmd/infinity-storage-mount` | `go build -o infinity-storage-mount.exe ./cmd/infinity-storage-mount` |

Install FUSE3 on Linux and WinFsp on Windows before mounting. Verify AWS credentials on each device with `aws sts get-caller-identity`.

## 3. Start Linux writer mount

```bash
mkdir -p /tmp/infinity-storage "$HOME/.cache/infinity-storage/spool"
./infinity-storage-mount \
  --mount /tmp/infinity-storage \
  --bucket YOUR_BUCKET \
  --prefix YOUR_PREFIX \
  --live-relay-url http://RELAY_HOST:8080 \
  --library-id demo-library \
  --relay-token-file docs/relay-token.local.md \
  --spool-dir "$HOME/.cache/infinity-storage/spool" \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy \
  --debug \
  --metrics-log-interval 1s 2>&1 | tee linux-mount.log
```

## 4. Start Windows observer mount

Copy `docs/relay-token.local.md` from Linux to a private path such as `C:\InfinityStorage\relay-token.local.md`, then run:

```powershell
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\InfinityStorage\spool"
.\infinity-storage-mount.exe `
  --mount Z: `
  --bucket YOUR_BUCKET `
  --prefix YOUR_PREFIX `
  --live-relay-url http://RELAY_HOST:8080 `
  --library-id demo-library `
  --relay-token-file C:\InfinityStorage\relay-token.local.md `
  --spool-dir "$env:LOCALAPPDATA\InfinityStorage\spool" `
  --proxy-bin .\stream_proxy.exe `
  --debug `
  --metrics-log-interval 1s 2>&1 | Tee-Object -FilePath windows-mount.log
```

## 5. Exercise and verify

| Step | Linux | Windows | Expected result |
|---:|---|---|---|
| 1 | `cp SOURCE.mp4 /tmp/infinity-storage/linux-video.mp4` | `Get-ChildItem Z:\` | Windows lists `linux-video.mp4` within about 250–500 ms after accepted bytes. |
| 2 | Keep the copy running. | Open `Z:\linux-video.mp4` in VLC and seek. | Reads route through relay to Linux spool. A request beyond accepted bytes waits up to 30 s and then fails truthfully. |
| 3 | Wait for writer close and S3 verification. | Keep playback open; then reopen the file. | Existing live read remains on spool; new open uses S3 ranged reads after the next S3 catalog refresh. |
| 4 | `sha256sum SOURCE.mp4 /tmp/infinity-storage/linux-video.mp4` | `Get-FileHash Z:\linux-video.mp4 -Algorithm SHA256` | Hashes match after durability. |
| 5 | Repeat with a unique filename copied into `Z:\`. | Verify on Linux. | Windows writer follows the same relay/S3 handoff. |

Use progressive MP4 first. A format whose metadata or needed ranges sit near the end cannot begin preview until those bytes have been accepted.

## Logs and performance capture

| Signal | Where | Meaning |
|---|---|---|
| `ingest state` | Writer mount log | `streaming`, `sealing`, `durable`, interruption, or abort transition. |
| `ingest write` | Writer mount log | Accepted offset, byte count, and running spool size. |
| `ingest upload queued/started/part` | Writer mount log | Multipart backpressure and completed S3 part sizes. |
| `ingest upload error` / `live relay publish` | Writer mount log | S3 or relay failure. |
| `relay request` | Relay log | Method, path, HTTP status, and request latency. Long writer polls naturally last up to 25 s. |
| `metrics` | Each mount log | Go heap allocation/system reservation, GC cycles, and goroutine count. |

Run these alongside the mount logs when diagnosing host or network pressure:

| Linux | Windows PowerShell |
|---|---|
| `pidstat -rud -p $(pgrep -n infinity-storage-mount) 1` | `Get-Process infinity-storage-mount | Select-Object CPU,PM,WS,Handles` |
| `ss -ti '( sport = :8080 or dport = :8080 )'` | `Get-NetTCPConnection -RemotePort 8080` |
| `sar -n DEV 1` | `Get-Counter '\Network Interface(*)\Bytes Total/sec' -SampleInterval 1` |

Record discovery latency, first-preview latency, S3 durability latency, handoff errors, file size, source selected, relay request status/latency, hash result, heap, and network counters for every run.

## Scope

This relay is a preconfigured-token demo transport. Its streaming catalog is in memory, so restarting it removes in-progress entries. Durable S3 objects continue to appear through the ordinary S3 catalog. Better Auth, account-scoped library ownership, durable catalog records, and short-lived mount authority remain the next phase.
