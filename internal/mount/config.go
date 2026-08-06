// Package mount is the Infinity Storage volume host.
//
// Platform split:
//   - linux: FUSE (hanwen/go-fuse)
//   - windows: WinFsp via cgofuse (SPCH over TCP to stream_proxy)
//   - darwin: stub (macFUSE / FSKit later)
//
// Shared (OS-agnostic) code stays in internal/{catalog,s3origin,proxypool,cacheclient,awsutil}
// and stream_proxy. Only the “appear as a local volume” seam is per-OS.
package mount

// Config selects one origin mode and where to attach the volume.
type Config struct {
	MountPoint string // Linux path (/tmp/infinity-storage) or Windows drive (Z:)
	Debug      bool

	// Exactly one of Bucket, Dir, UDS must be set.
	Bucket   string
	Prefix   string
	Region   string
	Profile  string
	Endpoint string
	EnvFile  string

	Dir string // local multi-file harness
	UDS string // SPCH endpoint: Unix socket path, or host:port for TCP

	ProxyBin string
	SpoolDir string
}
