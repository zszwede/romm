// Package web holds the embedded static frontend. Phase 1 ships a placeholder
// shell; a later phase replaces static/ with the built Vue dist (copied out of
// the existing image) plus the EmulatorJS/Ruffle/js-dos player assets, so the
// single binary serves the whole UI.
package web

import "embed"

//go:embed static
var Static embed.FS
