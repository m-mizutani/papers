package frontend

import "embed"

// StaticFiles holds the built single page application. `pnpm build` writes
// it to dist/; the .gitkeep keeps the embed pattern valid before a build.
//
//go:embed all:dist
var StaticFiles embed.FS
