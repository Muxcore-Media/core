//go:build default

// Package presets provides build-tag-gated module presets.
// Build with -tags default to include the essential modules.
// Module imports are uncommented as their repos are updated to match
// the stripped core contracts.
package presets

// Note: module repos will be updated to implement the new stripped contracts.
// Uncomment imports once modules are updated.
//
// import (
// 	_ "github.com/Muxcore-Media/admin-ui"
// 	_ "github.com/Muxcore-Media/api-rest"
// 	_ "github.com/Muxcore-Media/cache-memory"
// 	_ "github.com/Muxcore-Media/health-monitor"
// 	_ "github.com/Muxcore-Media/ratelimit-tokenbucket"
// 	_ "github.com/Muxcore-Media/scheduler-cron"
// )
