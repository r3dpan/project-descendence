// Package webdist embeds the built SPA for cmd/api to serve same-origin
// (task 7.4, ARCHITECTURE.md §4.11). go:embed needs "dist" to exist and
// contain at least one file at compile time; web/.gitignore keeps only a
// placeholder index.html tracked so a fresh clone's `go build` never fails
// on a missing directory, while `npm run build` in this directory replaces
// it locally with the real bundle before a production build.
package webdist

import "embed"

//go:embed dist
var Dist embed.FS
