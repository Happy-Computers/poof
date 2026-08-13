package main

import rl "vendor:raylib"

main :: proc() {
	rl.SetConfigFlags({.WINDOW_RESIZABLE})
	rl.InitWindow(1100, 700, "Infinity Storage")
	rl.SetTargetFPS(240)
	defer rl.CloseWindow()

	app := app_init()
	defer app_destroy(&app)

	for !rl.WindowShouldClose() {
		rl.BeginDrawing()
		app_frame(&app)
		rl.EndDrawing()
	}
}
