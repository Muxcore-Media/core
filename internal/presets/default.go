//go:build default

// Package presets provides build-tag-gated module presets.
// Build with -tags default to include the essential modules for a novice user:
//
//	go build -tags default ./cmd/muxcored
//
// Without the tag, core builds with zero modules — just the fabric.
package presets

import (
	_ "github.com/Muxcore-Media/admin-ui"
	_ "github.com/Muxcore-Media/api-rest"
	_ "github.com/Muxcore-Media/cache-memory"
	_ "github.com/Muxcore-Media/health-monitor"
	_ "github.com/Muxcore-Media/ratelimit-tokenbucket"
	_ "github.com/Muxcore-Media/scheduler-cron"
)
