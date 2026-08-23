# Live relay test

The relay makes an in-progress flat file visible before its multipart S3 upload completes. A writer
publishes accepted byte ranges, an observer requests only the ranges its player needs, and S3 becomes
the source for new opens after durability.

## Account-authorized relay

Run the relay with the deployed API as its authority:

```bash
infinity-storage-relay --listen :8080 --authority-url https://api.example.com
```

The relay forwards each mount bearer token to `GET /v1/libraries/<project-id>/authorize`.
The API accepts only the account that owns that project, so a file published from one signed-in
machine is visible only to another mount for the same account and project. Configure the desktop
client with `INFINITY_STORAGE_LIVE_RELAY_URL=https://relay.example.com`; it passes the current
session token and selected project ID to each mounted client, then removes the local token file
when the mount exits.

`--token-file` remains available for the local development harness. Do not use its shared static
token for account-backed deployments.

## Same-PC WSL2 ↔ Windows harness

This local harness uses the ignored `scripts/*.local.*` launchers and one private relay-token file.
It requires FUSE3 in WSL, WinFsp on Windows, Go, Zig, AWS credentials with multipart permissions,
and a progressive MP4 test file.

In PowerShell, start the relay and both mounts:

```powershell
& "C:\Users\amaan\WORK\video-storage-engine\scripts\run-entire-live-test.local.ps1"
```

Leave that terminal open. When `Z:` appears, use another PowerShell terminal for the copy and watch:

```powershell
& "C:\Users\amaan\WORK\video-storage-engine\scripts\copy-watch-live-test.local.ps1"
```

The watcher prints a new `Z:\linux-video-<timestamp>.mp4` path. Open that path while the copy runs.
A progressive MP4 can preview once its header and requested media ranges have reached the Linux
spool. After the copy closes, the writer seals the file, completes S3 multipart upload, and records
`state=durable` in `linux-mount.log`.

## Runtime contract

- The relay checks account ownership of the requested library before every catalog, range, or writer request.
- Each mount creates a unique writer ID. Relay range jobs route only to the writer that published the file.
- The relay never stores media bytes; it holds a bounded in-memory catalog and forwards requested ranges.
- Unwritten ranges wait for accepted bytes, then report unavailable instead of returning fabricated data.
- Existing live reads retain their writer source through handoff; later opens use the normal S3 range path.
- A relay restart drops in-progress entries. Durable objects remain discoverable from S3.

## Diagnostics

| Symptom | Check |
|---|---|
| `Z:` does not appear | Keep launcher 1 open; inspect `windows-mount.log`. |
| Video will not begin | Confirm `relay.log` has `GET /v1/live/... 206`; `503` means a requested range is not yet available. |
| Upload does not finish | Inspect `linux-mount.log` for `ingest upload error`; successful completion ends with `state=durable`. |
| File is absent after restart | Confirm the object exists in S3; relay state only covers in-progress files. |

This is a development transport with a preconfigured token. Account-scoped authorization, durable
catalog records, and short-lived mount authority belong to the shared-library work.
