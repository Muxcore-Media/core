package contracts

import "context"

// ConfigWatcher notifies modules when infrastructure services change at
// runtime. Modules subscribe by capability string; when a new module
// registers or an existing one disappears, registered handlers fire.
//
// Modules use this instead of polling the registry. Example:
//
//	watcher.OnChange(ctx, "secrets", func() {
//	    entries := reg.FindByCapability("secrets")
//	    if len(entries) > 0 {
//	        secrets = entries[0].Module.(contracts.SecretsProvider)
//	    }
//	})
//
// ConfigWatcher is discovered via FindByCapability("config.watcher").
// If no module implements it, modules fall back to polling or subscribe
// to module.registered/module.unregistered events directly.
type ConfigWatcher interface {
	// OnChange registers a handler that fires when modules matching the
	// given capability string are added or removed. Returns a cancel
	// function to stop watching. The handler receives no arguments —
	// the module is expected to re-query the registry for current state.
	OnChange(ctx context.Context, capability string, handler func()) (cancel func())
}
