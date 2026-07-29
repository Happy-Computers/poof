// Package mount is the Space volume host.
//
// Platform split:
//   - linux: FUSE (go-fuse) — current implementation
//   - darwin: stub (macFUSE / FSKit later)
//   - windows: stub (WinFsp later)
//
// Shared (OS-agnostic) code stays in internal/{spacecatalog,s3origin,proxypool,cacheclient,awsutil}
// and stream_proxy. Only the “appear as a local volume” seam is per-OS.
package mount

// Config selects one origin mode and where to attach the volume.
type Config struct {
	MountPoint string
	Debug      bool

	// Exactly one of Bucket, Dir, UDS must be set.
	Bucket   string
	Prefix   string
	Region   string
	Profile  string
	Endpoint string
	EnvFile  string

	Dir string // local multi-file harness
	UDS string // single-file stream_proxy socket

	ProxyBin string
}
