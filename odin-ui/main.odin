package main

import rl "vendor:raylib"

main :: proc() {
	rl.SetConfigFlags({.WINDOW_RESIZABLE})
	rl.InitWindow(1100, 700, "Infinity Storage")
	defer rl.CloseWindow()
	rl.SetTargetFPS(60)

	app := app_init()
	defer app_destroy(&app)

	for !rl.WindowShouldClose() {
		rl.BeginDrawing()
		app_frame(&app)
		rl.EndDrawing()
	}
}
