package mount

import "errors"

// ErrUnsupported means this OS has no volume backend yet.
var ErrUnsupported = errors.New("infinity-storage mount: unsupported on this platform")
