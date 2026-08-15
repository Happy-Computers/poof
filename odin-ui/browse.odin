package main

import "core:fmt"
import "core:os"
import "core:path/filepath"
import "core:slice"
import "core:strings"
import "core:time"
import sync_chan "core:sync/chan"
import thread "core:thread"

BrowseEntry :: struct {
	name:   string,
	path:   string,
	is_dir: bool,
	size:   i64,
}

BrowseEventKind :: enum {
	Ready,
	Entry,
	Complete,
	Failed,
}

BrowseEvent :: struct {
	kind:  BrowseEventKind,
	entry: BrowseEntry,
	error: string,
}

Browse :: struct {
	root:     string,
	cwd:      string,
	entries:  [dynamic]BrowseEntry,
	selected: int,
	scroll:   f32,
	error:    string,
	loading:  bool,
	results:  sync_chan.Chan(BrowseEvent),
	loader:   ^thread.Thread,
}

browse_init :: proc(root: string) -> Browse {
	results, err := sync_chan.create(sync_chan.Chan(BrowseEvent), 512, context.allocator)
	assert(err == .None)

	b := Browse{
		root     = strings.clone(root),
		cwd      = strings.clone(root),
		selected = -1,
		results  = results,
	}
	browse_reload(&b)
	return b
}

browse_destroy :: proc(b: ^Browse) {
	for b.loader != nil {
		browse_discard_events(b)
		if thread.is_done(b.loader) {
			thread.join(b.loader)
			thread.destroy(b.loader)
			b.loader = nil
			break
		}
		time.sleep(time.Millisecond)
	}
	browse_discard_events(b)
	sync_chan.destroy(b.results)
	browse_clear_entries(b)
	delete(b.root)
	delete(b.cwd)
}

browse_discard_events :: proc(b: ^Browse) {
	for event, ok := sync_chan.try_recv(b.results); ok; event, ok = sync_chan.try_recv(b.results) {
		if event.kind == .Entry {
			browse_delete_entry(event.entry)
		}
	}
}

browse_delete_entry :: proc(entry: BrowseEntry) {
	delete(entry.name)
	delete(entry.path)
}

browse_send :: proc(results: sync_chan.Chan(BrowseEvent), event: BrowseEvent) -> bool {
	sent := sync_chan.send(results, event)
	if !sent && event.kind == .Entry {
		browse_delete_entry(event.entry)
	}
	return sent
}

browse_clear_entries :: proc(b: ^Browse) {
	for entry in b.entries {
		browse_delete_entry(entry)
	}
	clear(&b.entries)
}

browse_reload :: proc(b: ^Browse) {
	if b.loading {
		return
	}
	browse_clear_entries(b)
	b.error = ""
	b.selected = -1
	b.scroll = 0
	b.loading = true
	b.loader = thread.create_and_start_with_poly_data2(
		b.cwd,
		b.results,
		browse_load_worker,
		name = "directory-load",
	)
	if b.loader == nil {
		b.loading = false
		b.error = "cannot start directory load"
	}
}

browse_poll :: proc(b: ^Browse) -> bool {
	changed := false
	for event_count := 0; event_count < 32; event_count += 1 {
		event, ok := sync_chan.try_recv(b.results)
		if !ok {
			break
		}
		changed = true
		switch event.kind {
		case .Ready:
		case .Entry:
			append(&b.entries, event.entry)
		case .Complete:
			slice.sort_by(b.entries[:], proc(a, c: BrowseEntry) -> bool {
				if a.is_dir != c.is_dir {
					return a.is_dir
				}
				return a.name < c.name
			})
			b.loading = false
			if b.loader != nil {
				thread.join(b.loader)
				thread.destroy(b.loader)
				b.loader = nil
			}
		case .Failed:
			b.error = event.error
			b.loading = false
			if b.loader != nil {
				thread.join(b.loader)
				thread.destroy(b.loader)
				b.loader = nil
			}
		}
	}
	return changed
}

browse_load_worker :: proc(path: string, results: sync_chan.Chan(BrowseEvent)) {
	when ODIN_OS == .Windows {
		browse_load_windows(path, results)
	} else {
		browse_load_os(path, results)
	}
}

browse_load_os :: proc(path: string, results: sync_chan.Chan(BrowseEvent)) {
	directory, err := os.open(path)
	if err != nil {
		browse_send(results, BrowseEvent{kind = .Failed, error = "cannot open mount directory"})
		return
	}
	defer os.close(directory)

	if !browse_send(results, BrowseEvent{kind = .Ready}) {
		return
	}
	iterator := os.read_directory_iterator_create(directory)
	defer os.read_directory_iterator_destroy(&iterator)
	for fi in os.read_directory_iterator(&iterator) {
		if fi.name == "." || fi.name == ".." {
			continue
		}
		full, join_err := filepath.join({path, fi.name})
		if join_err != nil {
			continue
		}
		if !browse_send(results, BrowseEvent{
			kind  = .Entry,
			entry = {
				name   = strings.clone(fi.name),
				path   = full,
				is_dir = fi.type == .Directory,
				size   = fi.size,
			},
		}) {
			return
		}
	}
	_, iter_err := os.read_directory_iterator_error(&iterator)
	if iter_err != nil {
		browse_send(results, BrowseEvent{kind = .Failed, error = "cannot read mount directory"})
		return
	}
	browse_send(results, BrowseEvent{kind = .Complete})
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
