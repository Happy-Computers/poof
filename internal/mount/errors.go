package mount

import "errors"

// ErrUnsupported means this OS has no volume backend yet.
var ErrUnsupported = errors.New("space mount: unsupported on this platform")
