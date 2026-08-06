// Package catalog builds a flat name → local-file map for Infinity Storage.
package catalog

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Source interface {
	ReadAt(dest []byte, offset uint64) (int, error)
	Size() uint64
}

// Hard limits. Change only by deliberate redesign.
const (
	MaxFiles     = 256
	MaxNameBytes = 255
)

// Entry is one regular file in the Infinity Storage folder.
type Entry struct {
	Name      string // basename only
	AbsPath   string // local --dir mode
	OriginURL string // S3 / HTTP origin mode (stream_proxy --origin-url)
	Source    Source
	Size      uint64
}

// SlotKey uniquely identifies the cache proxy for this entry.
func (e Entry) SlotKey() string {
	if e.Source != nil {
		return e.Name
	}
	if e.OriginURL != "" {
		return e.OriginURL
	}
	return e.AbsPath
}

// WithOriginBase sets OriginURL to originBase + "/object/" + PathEscape(Name).
// originBase is like "http://127.0.0.1:9090" (no trailing slash).
func WithOriginBase(entries []Entry, originBase string) []Entry {
	base := strings.TrimRight(originBase, "/")
	out := make([]Entry, len(entries))
	for i, e := range entries {
		e.OriginURL = base + "/object/" + url.PathEscape(e.Name)
		out[i] = e
	}
	return out
}

// LoadDir lists regular files in dir (non-recursive). Fail-fast on limit violations.
func LoadDir(dir string) ([]Entry, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("abs: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", abs)
	}

	dents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(dents))
	out := make([]Entry, 0, len(dents))
	for _, de := range dents {
		name := de.Name()
		if name == "." || name == ".." {
			continue
		}
		if len(name) > MaxNameBytes {
			return nil, fmt.Errorf("name too long (%d > %d): %q", len(name), MaxNameBytes, name)
		}
		info, err := de.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("empty file not allowed: %q", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate basename: %q", name)
		}
		if len(out) >= MaxFiles {
			return nil, fmt.Errorf("too many files (>%d) in %s", MaxFiles, abs)
		}
		seen[name] = struct{}{}
		out = append(out, Entry{
			Name:    name,
			AbsPath: filepath.Join(abs, name),
			Size:    uint64(info.Size()),
		})
	}
	return out, nil
}
