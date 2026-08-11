package main

import "core:os"
import "core:time"
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
	last_poll:  time.Time,
}

status_init :: proc(mount_path, api_url: string) -> Status {
	return Status{
		api        = .Unknown,
		mount      = .Unknown,
		mount_path = mount_path,
		api_url    = api_url,
		last_error = "",
	}
}

status_refresh :: proc(s: ^Status) {
	s.last_poll = time.now()
	s.last_error = ""

	if s.mount_path == "" {
		s.mount = .Down
		s.last_error = "mount path empty"
		return
	}

	if os.is_dir(s.mount_path) {
		s.mount = .Up
	} else {
		s.mount = .Down
		s.last_error = "mount path missing (start infinity-storage-mount)"
	}

	// TODO: HTTP GET api_url/health when client wired
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
		return "Mount: ?", t.warn
	}
	return "Mount: ?", t.text_dim
}
