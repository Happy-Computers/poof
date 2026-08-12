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
		bg           = {40,44,52,255},
		panel        = {33,37,43,255},
		panel_border = {62,68,81,255},
		text         = {171,178,191,255},
		text_dim     = {92,99,112,255},
		accent       = {97,175,239,255},
		ok           = {152,195,121,255},
		warn         = {229,192,123,255},
		bad          = {224,108,117,255},
		row_hot      = {44,49,60,255},
		row_sel      = {62,68,81,255},
		font_size    = 18,
		pad          = 12,
		row_h        = 28,
		status_h     = 96,
		detail_h     = 56,
	}
}
