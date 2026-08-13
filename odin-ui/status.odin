package main

import "core:strings"
import rl "vendor:raylib"

ApiState :: enum {
	Unknown,
	Reachable,
	Unreachable,
}

MountState :: enum {
	Unknown,
	Up,
	Down,
}

Status :: struct {
	api:        ApiState,
	mount:      MountState,
	mount_path: string,
	api_url:    string,
	last_error: string,
}

status_init :: proc(mount_path, api_url: string) -> Status {
	return Status{
		api        = .Unknown,
		mount      = .Unknown,
		mount_path = strings.clone(mount_path),
		api_url    = strings.clone(api_url),
	}
}

status_destroy :: proc(s: ^Status) {
	delete(s.mount_path)
	delete(s.api_url)
}

status_set_mount :: proc(s: ^Status, up: bool, error: string) {
	if up {
		s.mount = .Up
		s.last_error = ""
		return
	}
	s.mount = .Down
	s.last_error = error
}

status_api_label :: proc(s: Status) -> (string, rl.Color) {
	t := theme_default()
	switch s.api {
	case .Reachable:
		return "API: up", t.ok
	case .Unreachable:
		return "API: down", t.bad
	case .Unknown:
		return "API: stub", t.warn
	}
	return "API: ?", t.text_dim
}

status_mount_label :: proc(s: Status) -> (string, rl.Color) {
	t := theme_default()
	switch s.mount {
	case .Up:
		return "Mount: up", t.ok
	case .Down:
		return "Mount: down", t.bad
	case .Unknown:
		return "Mount: loading", t.warn
	}
	return "Mount: ?", t.text_dim
}
