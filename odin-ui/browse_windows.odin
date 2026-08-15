#+build windows
package main

import "core:path/filepath"
import win "core:sys/windows"
import sync_chan "core:sync/chan"

browse_load_windows :: proc(path: string, results: sync_chan.Chan(BrowseEvent)) {
	path_utf16 := win.utf8_to_utf16(path, context.temp_allocator)
	if len(path_utf16) == 0 {
		browse_send(results, BrowseEvent{kind = .Failed, error = "cannot encode mount path"})
		return
	}
	base_length := len(path_utf16) - 1
	has_separator := path_utf16[base_length-1] == '/' || path_utf16[base_length-1] == '\\'
	search_length := base_length + 2
	if !has_separator {
		search_length += 1
	}
	search := make([]u16, search_length, context.temp_allocator)
	copy(search[:base_length], path_utf16[:base_length])
	index := base_length
	if !has_separator {
		search[index] = '\\'
		index += 1
	}
	search[index] = '*'

	data: win.WIN32_FIND_DATAW
	handle := win.FindFirstFileW(cstring16(raw_data(search)), &data)
	if handle == win.INVALID_HANDLE_VALUE {
		browse_send(results, BrowseEvent{kind = .Failed, error = "cannot open mount directory"})
		return
	}
	defer win.FindClose(handle)

	if !browse_send(results, BrowseEvent{kind = .Ready}) {
		return
	}
	for {
		if !browse_load_windows_entry(path, &data, results) {
			return
		}
		if win.FindNextFileW(handle, &data) {
			continue
		}
		if win.GetLastError() != win.ERROR_NO_MORE_FILES {
			browse_send(results, BrowseEvent{kind = .Failed, error = "cannot read mount directory"})
			return
		}
		break
	}
	browse_send(results, BrowseEvent{kind = .Complete})
}

browse_load_windows_entry :: proc(path: string, data: ^win.WIN32_FIND_DATAW, results: sync_chan.Chan(BrowseEvent)) -> bool {
	name_length := 0
	for name_length < len(data.cFileName) {
		if data.cFileName[name_length] == 0 {
			break
		}
		name_length += 1
	}
	if name_length == 0 {
		return true
	}
	name := win.utf16_to_utf8(data.cFileName[:name_length], context.allocator) or_else ""
	if name == "" || name == "." || name == ".." {
		return true
	}
	full, join_err := filepath.join({path, name})
	if join_err != nil {
		delete(name)
		return true
	}
	is_dir := data.dwFileAttributes&win.FILE_ATTRIBUTE_DIRECTORY != 0
	size := i64(data.nFileSizeHigh)<<32 | i64(data.nFileSizeLow)
	return browse_send(results, BrowseEvent{
		kind  = .Entry,
		entry = {name = name, path = full, is_dir = is_dir, size = size},
	})
}
