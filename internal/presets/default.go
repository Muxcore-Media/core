//go:build default

// Package presets provides build-tag-gated module presets.
// Build with -tags default to include the essential modules for a novice user:
//
//	go build -tags default ./cmd/muxcored
//
// Without the tag, core builds with zero modules — just the fabric.
package presets

import (
	// Essential modules for the default configuration.
	// Each module's init() calls contracts.Register() so it's
	// automatically loaded at bootstrap.
	_ "github.com/Muxcore-Media/admin-ui"
	_ "github.com/Muxcore-Media/api-rest"
	_ "github.com/Muxcore-Media/scheduler-cron"
)
