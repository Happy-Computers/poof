package main

import "core:fmt"
import rl "vendor:raylib"

Ui :: struct {
	hot:    i32,
	active: i32,
	id_seq: i32,
}

ui_begin :: proc(ui: ^Ui) {
	ui.hot = 0
	ui.id_seq = 1
	if !rl.IsMouseButtonDown(.LEFT) {
		ui.active = 0
	}
}

ui_next_id :: proc(ui: ^Ui) -> i32 {
	id := ui.id_seq
	ui.id_seq += 1
	return id
}

ui_panel :: proc(bounds: rl.Rectangle, t: Theme) {
	rl.DrawRectangleRec(bounds, t.panel)
	rl.DrawRectangleLinesEx(bounds, 1, t.panel_border)
}

ui_label :: proc(text: string, x, y: f32, t: Theme, color: rl.Color = {}) {
	c := t.text if color.a == 0 else color
	rl.DrawText(fmt.ctprintf("%s", text), i32(x), i32(y), t.font_size, c)
}

ui_button :: proc(ui: ^Ui, bounds: rl.Rectangle, label: string, t: Theme) -> bool {
	id := ui_next_id(ui)
	mouse := rl.GetMousePosition()
	hover := rl.CheckCollisionPointRec(mouse, bounds)

	if hover {
		ui.hot = id
		if rl.IsMouseButtonPressed(.LEFT) {
			ui.active = id
		}
	}

	bg := t.panel_border
	if ui.active == id {
		bg = t.accent
	} else if hover {
		bg = t.row_hot
	}
	rl.DrawRectangleRec(bounds, bg)
	rl.DrawRectangleLinesEx(bounds, 1, t.panel_border)

	tw := rl.MeasureText(fmt.ctprintf("%s", label), t.font_size)
	tx := bounds.x + (bounds.width - f32(tw)) * 0.5
	ty := bounds.y + (bounds.height - f32(t.font_size)) * 0.5
	ui_label(label, tx, ty, t)

	return hover && ui.active == id && rl.IsMouseButtonReleased(.LEFT)
}

ui_row :: proc(
	ui: ^Ui,
	bounds: rl.Rectangle,
	label: string,
	selected: bool,
	t: Theme,
) -> bool {
	id := ui_next_id(ui)
	mouse := rl.GetMousePosition()
	hover := rl.CheckCollisionPointRec(mouse, bounds)

	if hover {
		ui.hot = id
		if rl.IsMouseButtonPressed(.LEFT) {
			ui.active = id
		}
	}

	bg := t.panel
	if selected {
		bg = t.row_sel
	} else if hover {
		bg = t.row_hot
	}
	rl.DrawRectangleRec(bounds, bg)
	ui_label(label, bounds.x + t.pad, bounds.y + (bounds.height - f32(t.font_size)) * 0.5, t)

	return hover && ui.active == id && rl.IsMouseButtonReleased(.LEFT)
}
