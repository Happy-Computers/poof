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
	font: rl.Font,
	font_owned: bool,
	font_size:    i32,
	pad:          f32,
	row_h:        f32,
	status_h:     f32,
	detail_h:     f32,
}


theme_load_font :: proc(t: ^Theme){
	t.font = rl.LoadFontEx("C:/Windows/Fonts/arial.ttf", t.font_size * 2, nil, 0)
	if t.font.texture.id != 0 {
		rl.SetTextureFilter(t.font.texture, rl.TextureFilter.BILINEAR)
		t.font_owned = true
		return
	}
	t.font = rl.GetFontDefault()
	t.font_owned = false
}

theme_unload_font::proc(t:^Theme){
	if t.font_owned {
		rl.UnloadFont(t.font)
		t.font_owned = false
	}
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
