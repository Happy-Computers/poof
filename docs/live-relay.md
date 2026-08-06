# Live relay demo

The relay makes an in-progress writer mount visible on another active mount before S3 completes.
It carries catalog updates and requested byte ranges only; accepted file bytes remain on the writer's bounded spool until S3 durability succeeds.

## Start the relay

Run this on a host reachable from both devices. The relay does not need S3 credentials.

```bash
cd /path/to/video-storage-engine
openssl rand -base64 32 > relay.token
chmod 600 relay.token
go build -o infinity-storage-relay ./cmd/infinity-storage-relay
./infinity-storage-relay --listen 0.0.0.0:8080 --token-file ./relay.token
```

Use an HTTPS reverse proxy for a network outside a trusted development LAN. Every mount in this demo
uses the same relay URL, library ID, and token file. The token grants access to that library and must
not be placed in a shell argument or checked into source control.

## Linux writer

```bash
./infinity-storage-mount \
  --mount /tmp/infinity-storage \
  --bucket YOUR_BUCKET \
  --live-relay-url http://RELAY_HOST:8080 \
  --library-id demo-library \
  --relay-token-file ./relay.token \
  --spool-dir "$HOME/.cache/infinity-storage/spool" \
  --proxy-bin ./stream_proxy/zig-out/bin/stream_proxy
```

## Windows observer

```powershell
.\infinity-storage-mount.exe `
  --mount Z: `
  --bucket YOUR_BUCKET `
  --live-relay-url http://RELAY_HOST:8080 `
  --library-id demo-library `
  --relay-token-file C:\path\to\relay.token `
  --spool-dir "$env:LOCALAPPDATA\InfinityStorage\spool" `
  --proxy-bin .\stream_proxy.exe
```

## Expected flow

1. Drag a new flat file into the Linux mount.
2. Its first accepted bytes publish a `streaming` catalog entry to the relay.
3. Windows polls the relay at 250 ms intervals, lists the filename, and requests only selected ranges.
4. The relay hands each range request to the Linux writer through one of eight outbound long-poll workers.
5. The Linux spool serves an accepted range or waits up to 30 seconds for the writer to reach it.
6. S3 multipart upload runs concurrently in the writer.
7. After S3 checksum verification, the relay removes the live entry and both mounts use the existing S3 ranged-read path.

The relay has a global maximum of eight concurrent 8 MiB range requests and never persists a second complete object.

## Scope

This is the demo transport before the account-owned API relay. It uses one preconfigured bearer token
per library and keeps streaming catalog state in relay memory, so restarting the relay removes
in-progress entries. Durable objects remain in S3 and appear through the ordinary S3 catalog.

Electron forwards the relay configuration to the mount when all three environment variables are set:
`INFINITY_STORAGE_LIVE_RELAY_URL`, `INFINITY_STORAGE_LIBRARY_ID`, and
`INFINITY_STORAGE_RELAY_TOKEN_FILE`.
