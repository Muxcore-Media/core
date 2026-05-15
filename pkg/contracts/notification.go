package contracts

import "context"

// NotificationLevel indicates severity.
type NotificationLevel string

const (
	NotifyInfo  NotificationLevel = "info"
	NotifyWarn  NotificationLevel = "warn"
	NotifyError NotificationLevel = "error"
)

// Notification is a message to be delivered to one or more channels.
type Notification struct {
	Title     string
	Body      string
	Level     NotificationLevel
	EventType string            // the event that triggered this notification, e.g. "download.completed"
	MediaRef  string            // optional media ID reference
	Channel   string            // target channel: "discord", "telegram", "email", etc.
	Metadata  map[string]string
}

// NotificationProvider is implemented by notification modules (notifier-discord, notifier-telegram, etc.)
// Multiple notification modules can coexist. A notification router (core or module) dispatches each
// notification to the appropriate provider(s) based on per-event-type routing rules.
type NotificationProvider interface {
	// Send delivers a notification.
	Send(ctx context.Context, n Notification) error
	// SupportedChannels returns the channel identifiers this provider handles.
	SupportedChannels() []string
	// Validate checks connectivity and configuration.
	Validate(ctx context.Context) error
}
