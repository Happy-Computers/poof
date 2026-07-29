//go:build !linux

package mount

import "fmt"

// Run refuses to mount outside Linux until darwin/windows backends exist.
func Run(cfg Config) error {
	return fmt.Errorf("%w (need linux FUSE; darwin/windows not implemented)", ErrUnsupported)
}
