package main

import "core:fmt"
import "core:os"
import "core:path/filepath"
import "core:slice"
import "core:strings"

BrowseEntry :: struct {
	name:   string,
	path:   string,
	is_dir: bool,
	size:   i64,
}

Browse :: struct {
	root:     string,
	cwd:      string,
	entries:  [dynamic]BrowseEntry,
	selected: int,
	scroll:   f32,
	use_mock: bool,
	error:    string,
}

browse_init :: proc(root: string, use_mock: bool) -> Browse {
	b := Browse{
		root     = root,
		cwd      = root,
		selected = -1,
		use_mock = use_mock,
	}
	browse_reload(&b)
	return b
}

browse_destroy :: proc(b: ^Browse) {
	browse_clear_entries(b)
}

browse_clear_entries :: proc(b: ^Browse) {
	for e in b.entries {
		delete(e.name)
		delete(e.path)
	}
	clear(&b.entries)
}

browse_reload :: proc(b: ^Browse) {
	browse_clear_entries(b)
	b.error = ""
	b.selected = -1
	b.scroll = 0

	if b.use_mock {
		browse_load_mock(b)
		return
	}

	if b.cwd == "" || !os.is_dir(b.cwd) {
		b.error = "path not a directory"
		browse_load_mock(b)
		b.use_mock = true
		return
	}

	fis, rerr := os.read_all_directory_by_path(b.cwd, context.allocator)
	if rerr != nil {
		b.error = "read_dir failed"
		browse_load_mock(b)
		b.use_mock = true
		return
	}
	defer os.file_info_slice_delete(fis, context.allocator)

	for fi in fis {
		if fi.name == "." || fi.name == ".." {
			continue
		}
		full, jerr := filepath.join({b.cwd, fi.name})
		if jerr != nil {
			continue
		}
		append(
			&b.entries,
			BrowseEntry{
				name   = strings.clone(fi.name),
				path   = full,
				is_dir = fi.type == .Directory,
				size   = fi.size,
			},
		)
	}

	slice.sort_by(b.entries[:], proc(a, b: BrowseEntry) -> bool {
		if a.is_dir != b.is_dir {
			return a.is_dir
		}
		return a.name < b.name
	})
}

browse_load_mock :: proc(b: ^Browse) {
	mock := []BrowseEntry{
		{name = "movies/", path = "mock:/movies", is_dir = true, size = 0},
		{name = "shows/", path = "mock:/shows", is_dir = true, size = 0},
		{name = "readme.txt", path = "mock:/readme.txt", is_dir = false, size = 128},
		{name = "clip.mp4", path = "mock:/clip.mp4", is_dir = false, size = 1_048_576},
	}
	for m in mock {
		append(
			&b.entries,
			BrowseEntry{
				name   = strings.clone(m.name),
				path   = strings.clone(m.path),
				is_dir = m.is_dir,
				size   = m.size,
			},
		)
	}
	if b.error == "" {
		b.error = "mock listing (mount path unavailable)"
	}
}

browse_selected :: proc(b: Browse) -> (BrowseEntry, bool) {
	if b.selected < 0 || b.selected >= len(b.entries) {
		return {}, false
	}
	return b.entries[b.selected], true
}

browse_format_size :: proc(size: i64, is_dir: bool) -> string {
	if is_dir {
		return "dir"
	}
	if size < 1024 {
		return fmt.tprintf("%d B", size)
	}
	if size < 1024 * 1024 {
		return fmt.tprintf("%.1f KB", f64(size) / 1024)
	}
	return fmt.tprintf("%.1f MB", f64(size) / (1024 * 1024))
}
