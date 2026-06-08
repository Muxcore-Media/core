package contracts

// SettingType enumerates the supported setting types.
type SettingType string

const (
	SettingTypeString SettingType = "string"
	SettingTypeInt    SettingType = "int"
	SettingTypeBool   SettingType = "bool"
	SettingTypeSelect SettingType = "select"
	SettingTypeSecret SettingType = "secret" // masked in UI, redacted in logs
)

// SettingDef describes one configuration setting that a module exposes.
type SettingDef struct {
	Key         string   // env var or config key, e.g. "MUXCORE_JACKETT_URL"
	Label       string   // human-readable label, e.g. "Jackett Server URL"
	Type        SettingType
	Default     string   // default value as a string
	Description string   // help text
	Required    bool
	Options     []string // for SettingTypeSelect: allowed values
	Group       string   // settings group, e.g. "Connection", "Downloads"
}

// SettingsProvider is an optional interface modules implement to expose their
// configuration to the admin UI. The admin UI discovers all modules that implement
// this interface and renders their settings automatically.
type SettingsProvider interface {
	Settings() []SettingDef
}
