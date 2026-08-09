//go:build !linux && !windows

package mount

import "fmt"

// Run refuses to mount on platforms without a volume backend yet.
func Run(cfg Config) error {
	_ = cfg
	return fmt.Errorf("%w (need linux FUSE or windows WinFsp; darwin not implemented)", ErrUnsupported)
}
