package main

import "core:fmt"
import "core:math"
import "core:os"
import rl "vendor:raylib"

App :: struct {
	theme:  Theme,
	ui:     Ui,
	status: Status,
	browse: Browse,
}

app_default_mount :: proc() -> string {
	v := os.get_env("INFINITY_STORAGE_MOUNT", context.temp_allocator)
	if v != "" {
		return v
	}
	when ODIN_OS == .Windows {
		return "Z:/"
	} else {
		// for linux
		return "/tmp/infinity-storage"
		// need to add a switch instead for darwin
	}
}

app_default_api :: proc() -> string {
	v := os.get_env("INFINITY_STORAGE_API_URL", context.temp_allocator)
	if v != "" {
		return v
	}
	return "http://127.0.0.1:3005"
}

app_init :: proc() -> App {
	mount := app_default_mount()
	api := app_default_api()

	app := App{
		theme  = theme_default(),
		status = status_init(mount, api),
		browse = browse_init(mount),
	}
	theme_load_font(&app.theme)
	return app
}

app_destroy :: proc(app: ^App) {
	theme_unload_font(&app.theme)
	browse_destroy(&app.browse)
	status_destroy(&app.status)
}

app_frame :: proc(app: ^App) {
	t := app.theme
	w := f32(rl.GetScreenWidth())
	h := f32(rl.GetScreenHeight())

	if browse_poll(&app.browse) {
		status_set_mount(&app.status, app.browse.error == "", app.browse.error)
	}

	ui_begin(&app.ui)
	rl.ClearBackground(t.bg)

	pad := t.pad
	status_bounds := rl.Rectangle{pad, pad, w - pad * 2, t.status_h}
	detail_bounds := rl.Rectangle{pad, h - pad - t.detail_h, w - pad * 2, t.detail_h}
	browse_bounds := rl.Rectangle{
		pad,
		status_bounds.y + status_bounds.height + pad,
		w - pad * 2,
		detail_bounds.y - (status_bounds.y + status_bounds.height + pad) - pad,
	}

	app_draw_status(app, status_bounds)
	app_draw_browse(app, browse_bounds)
	app_draw_detail(app, detail_bounds)
}

app_draw_status :: proc(app: ^App, bounds: rl.Rectangle) {
	t := app.theme
	ui_panel(bounds, t)

	x := bounds.x + t.pad
	y := bounds.y + t.pad
	ui_label("Infinity Storage", x, y, t, t.accent)
	y += f32(t.font_size) + 6

	api_text, api_color := status_api_label(app.status)
	mount_text, mount_color := status_mount_label(app.status)
	ui_label(api_text, x, y, t, api_color)
	ui_label(mount_text, x + 140, y, t, mount_color)
	y += f32(t.font_size) + 4

	ui_label(fmt.tprintf("path: %s", app.status.mount_path), x, y, t, t.text_dim)

	btn := rl.Rectangle{bounds.x + bounds.width - 100 - t.pad, bounds.y + t.pad, 100, 32}
	if ui_button(&app.ui, btn, "Refresh", t) {
		browse_reload(&app.browse)
		app.status.mount = .Unknown
	}
}

app_draw_browse :: proc(app: ^App, bounds: rl.Rectangle) {
	t := app.theme
	ui_panel(bounds, t)

	header_y := bounds.y + t.pad
	title := "Browse"
	ui_label(title, bounds.x + t.pad, header_y, t)
	if app.browse.loading {
		ui_label("Loading directory…", bounds.x + 160, header_y, t, t.warn)
	} else if app.browse.error != "" {
		ui_label(app.browse.error, bounds.x + 160, header_y, t, t.warn)
	}

	wheel := rl.GetMouseWheelMove()
	if rl.CheckCollisionPointRec(rl.GetMousePosition(), bounds) {
		app.browse.scroll -= wheel * t.row_h * 2
	}
	max_scroll := math.max(
		f32(0),
		f32(len(app.browse.entries)) * t.row_h - (bounds.height - t.pad * 3 - f32(t.font_size)),
	)
	app.browse.scroll = math.clamp(app.browse.scroll, 0, max_scroll)

	list_top := header_y + f32(t.font_size) + t.pad
	list_h := bounds.height - (list_top - bounds.y) - t.pad
	rl.BeginScissorMode(i32(bounds.x), i32(list_top), i32(bounds.width), i32(list_h))

	for entry, i in app.browse.entries {
		row := rl.Rectangle{
			bounds.x + 1,
			list_top + f32(i) * t.row_h - app.browse.scroll,
			bounds.width - 2,
			t.row_h,
		}
		if row.y + row.height < list_top || row.y > list_top + list_h {
			continue
		}
		prefix := "[D] " if entry.is_dir else "    "
		label := fmt.tprintf("%s%s", prefix, entry.name)
		if ui_row(&app.ui, ui_row_id(i), row, label, app.browse.selected == i, t) {
			app.browse.selected = i
		}
	}

	rl.EndScissorMode()
}

app_draw_detail :: proc(app: ^App, bounds: rl.Rectangle) {
	t := app.theme
	ui_panel(bounds, t)

	x := bounds.x + t.pad
	y := bounds.y + (bounds.height - f32(t.font_size)) * 0.5

	if entry, ok := browse_selected(app.browse); ok {
		ui_label(
			fmt.tprintf(
				"%s  ·  %s  ·  %s",
				entry.path,
				browse_format_size(entry.size, entry.is_dir),
				app.status.last_error if app.status.last_error != "" else "ok",
			),
			x,
			y,
			t,
			t.text_dim,
		)
	} else {
		err := app.status.last_error if app.status.last_error != "" else "select a row"
		ui_label(err, x, y, t, t.text_dim)
	}
}
