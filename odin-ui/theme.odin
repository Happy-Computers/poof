package main

import rl "vendor:raylib"

Theme :: struct {
	bg:           rl.Color,
	panel:        rl.Color,
	panel_border: rl.Color,
	text:         rl.Color,
	text_dim:     rl.Color,
	accent:       rl.Color,
	ok:           rl.Color,
	warn:         rl.Color,
	bad:          rl.Color,
	row_hot:      rl.Color,
	row_sel:      rl.Color,
	font_size:    i32,
	pad:          f32,
	row_h:        f32,
	status_h:     f32,
	detail_h:     f32,
}

theme_default :: proc() -> Theme {
	return Theme{
		bg           = {18, 20, 24, 255},
		panel        = {28, 32, 40, 255},
		panel_border = {48, 54, 66, 255},
		text         = {230, 232, 238, 255},
		text_dim     = {140, 146, 160, 255},
		accent       = {90, 160, 220, 255},
		ok           = {80, 180, 120, 255},
		warn         = {210, 170, 70, 255},
		bad          = {210, 90, 90, 255},
		row_hot      = {38, 44, 56, 255},
		row_sel      = {45, 70, 100, 255},
		font_size    = 18,
		pad          = 12,
		row_h        = 28,
		status_h     = 96,
		detail_h     = 56,
	}
}
