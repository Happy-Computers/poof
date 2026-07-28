package s3origin

import (
	"fmt"
	"strconv"
	"strings"
)

// ByteRange is an inclusive byte interval [Start, EndInclusive].
type ByteRange struct {
	Start        uint64
	EndInclusive uint64
}

// ParseBytesRange parses an HTTP Range header value like "bytes=START-END".
// end is inclusive. Multi-range requests are rejected.
func ParseBytesRange(headerValue string, objectSize uint64) (ByteRange, error) {
	if objectSize == 0 {
		return ByteRange{}, fmt.Errorf("invalid range: empty object")
	}
	if !strings.HasPrefix(headerValue, "bytes=") {
		return ByteRange{}, fmt.Errorf("invalid range")
	}
	spec := headerValue[len("bytes="):]
	if strings.Contains(spec, ",") {
		return ByteRange{}, fmt.Errorf("invalid range: multi-range not supported")
	}

	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return ByteRange{}, fmt.Errorf("invalid range")
	}
	startText := spec[:dash]
	endText := spec[dash+1:]

	if startText == "" {
		if endText == "" {
			return ByteRange{}, fmt.Errorf("invalid range")
		}
		suffix, err := strconv.ParseUint(endText, 10, 64)
		if err != nil || suffix == 0 {
			return ByteRange{}, fmt.Errorf("invalid range")
		}
		if suffix >= objectSize {
			return ByteRange{Start: 0, EndInclusive: objectSize - 1}, nil
		}
		return ByteRange{
			Start:        objectSize - suffix,
			EndInclusive: objectSize - 1,
		}, nil
	}

	start, err := strconv.ParseUint(startText, 10, 64)
	if err != nil || start >= objectSize {
		return ByteRange{}, fmt.Errorf("invalid range")
	}

	if endText == "" {
		remaining := objectSize - start
		window := remaining
		if window > MaxRangeBytes {
			window = MaxRangeBytes
		}
		if window == 0 {
			return ByteRange{}, fmt.Errorf("invalid range")
		}
		return ByteRange{
			Start:        start,
			EndInclusive: start + window - 1,
		}, nil
	}

	end, err := strconv.ParseUint(endText, 10, 64)
	if err != nil || end < start {
		return ByteRange{}, fmt.Errorf("invalid range")
	}
	endInclusive := end
	if endInclusive >= objectSize {
		endInclusive = objectSize - 1
	}
	return ByteRange{Start: start, EndInclusive: endInclusive}, nil
}

func (r ByteRange) Length() uint64 {
	return r.EndInclusive - r.Start + 1
}
