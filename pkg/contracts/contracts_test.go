package contracts

import (
	"context"
	"testing"
	"time"
)

func TestErrNotFound(t *testing.T) {
	if ErrNotFound == nil {
		t.Fatal("ErrNotFound should not be nil")
	}
	if ErrNotFound.Error() == "" {
		t.Fatal("ErrNotFound should have an error message")
	}
}

func TestErrCircuitOpen(t *testing.T) {
	if ErrCircuitOpen == nil {
		t.Fatal("ErrCircuitOpen should not be nil")
	}
	if ErrCircuitOpen.Error() == "" {
		t.Fatal("ErrCircuitOpen should have an error message")
	}
}

func TestErrLockHeld(t *testing.T) {
	if ErrLockHeld == nil {
		t.Fatal("ErrLockHeld should not be nil")
	}
	if ErrLockHeld.Error() == "" {
		t.Fatal("ErrLockHeld should have an error message")
	}
}

func TestErrCallDenied(t *testing.T) {
	if ErrCallDenied == nil {
		t.Fatal("ErrCallDenied should not be nil")
	}
	if ErrCallDenied.Error() == "" {
		t.Fatal("ErrCallDenied should have an error message")
	}
}

func TestErrCallNoPolicy(t *testing.T) {
	if ErrCallNoPolicy == nil {
		t.Fatal("ErrCallNoPolicy should not be nil")
	}
	if ErrCallNoPolicy.Error() == "" {
		t.Fatal("ErrCallNoPolicy should have an error message")
	}
}

func TestErrDatabaseCredentialExposure(t *testing.T) {
	if ErrDatabaseCredentialExposure == nil {
		t.Fatal("ErrDatabaseCredentialExposure should not be nil")
	}
	if ErrDatabaseCredentialExposure.Error() == "" {
		t.Fatal("ErrDatabaseCredentialExposure should have an error message")
	}
}

func TestErrRollbackNotSupported(t *testing.T) {
	if ErrRollbackNotSupported == nil {
		t.Fatal("ErrRollbackNotSupported should not be nil")
	}
	if ErrRollbackNotSupported.Error() == "" {
		t.Fatal("ErrRollbackNotSupported should have an error message")
	}
}

func TestErrMigrationTargetNotFound(t *testing.T) {
	if ErrMigrationTargetNotFound == nil {
		t.Fatal("ErrMigrationTargetNotFound should not be nil")
	}
	if ErrMigrationTargetNotFound.Error() == "" {
		t.Fatal("ErrMigrationTargetNotFound should have an error message")
	}
}

func TestEventZeroValue(t *testing.T) {
	var e Event
	if e.ID != "" {
		t.Errorf("zero Event.ID = %q, want empty", e.ID)
	}
	if e.Type != "" {
		t.Errorf("zero Event.Type = %q, want empty", e.Type)
	}
	if e.Source != "" {
		t.Errorf("zero Event.Source = %q, want empty", e.Source)
	}
	if e.TraceID != "" {
		t.Errorf("zero Event.TraceID = %q, want empty", e.TraceID)
	}
	if e.Payload != nil {
		t.Errorf("zero Event.Payload = %v, want nil", e.Payload)
	}
	if e.Metadata != nil {
		t.Errorf("zero Event.Metadata = %v, want nil", e.Metadata)
	}
	if !e.Timestamp.IsZero() {
		t.Errorf("zero Event.Timestamp = %v, want zero", e.Timestamp)
	}
}

func TestEventConstruction(t *testing.T) {
	now := time.Now()
	payload := []byte(`{"key":"value"}`)
	meta := map[string]string{"trace_id": "abc123"}

	e := Event{
		ID:        "evt-001",
		Type:      "test.event",
		Source:    "tester",
		TraceID:   "trace-xyz",
		Payload:   payload,
		Metadata:  meta,
		Timestamp: now,
	}

	if e.ID != "evt-001" {
		t.Errorf("Event.ID = %q, want %q", e.ID, "evt-001")
	}
	if e.Type != "test.event" {
		t.Errorf("Event.Type = %q, want %q", e.Type, "test.event")
	}
	if e.Source != "tester" {
		t.Errorf("Event.Source = %q, want %q", e.Source, "tester")
	}
	if e.TraceID != "trace-xyz" {
		t.Errorf("Event.TraceID = %q, want %q", e.TraceID, "trace-xyz")
	}
	if string(e.Payload) != string(payload) {
		t.Errorf("Event.Payload = %q, want %q", e.Payload, payload)
	}
	if e.Metadata["trace_id"] != "abc123" {
		t.Errorf("Event.Metadata[\"trace_id\"] = %q, want %q", e.Metadata["trace_id"], "abc123")
	}
	if !e.Timestamp.Equal(now) {
		t.Errorf("Event.Timestamp = %v, want %v", e.Timestamp, now)
	}
}

func TestEventMetadataMutability(t *testing.T) {
	m := map[string]string{"key": "original"}
	e := Event{Type: "test", Source: "src", Metadata: m}

	m["key"] = "mutated"
	if e.Metadata["key"] != "mutated" {
		t.Error("Event.Metadata should share reference with assigned map")
	}
}

func TestEventPayloadMutability(t *testing.T) {
	p := []byte("original")
	e := Event{Type: "test", Source: "src", Payload: p}

	p[0] = 'm'
	if string(e.Payload) != "mriginal" {
		t.Error("Event.Payload should share reference with assigned slice")
	}
}

func TestReplyEventType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"media.requested", "media.requested.reply"},
		{"module.registered", "module.registered.reply"},
		{"", ".reply"},
	}
	for _, tt := range tests {
		got := ReplyEventType(tt.input)
		if got != tt.expected {
			t.Errorf("ReplyEventType(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEventTypeConstants(t *testing.T) {
	if EventModuleRegistered != "module.registered" {
		t.Errorf("EventModuleRegistered = %q, want %q", EventModuleRegistered, "module.registered")
	}
	if EventModuleUnregistered != "module.unregistered" {
		t.Errorf("EventModuleUnregistered = %q, want %q", EventModuleUnregistered, "module.unregistered")
	}
	if EventModuleDegraded != "module.degraded" {
		t.Errorf("EventModuleDegraded = %q, want %q", EventModuleDegraded, "module.degraded")
	}
}

func TestSessionZeroValue(t *testing.T) {
	var s Session
	if s.UserID != "" {
		t.Errorf("zero Session.UserID = %q, want empty", s.UserID)
	}
	if s.Username != "" {
		t.Errorf("zero Session.Username = %q, want empty", s.Username)
	}
	if s.Roles != nil {
		t.Errorf("zero Session.Roles = %v, want nil", s.Roles)
	}
	if s.Permissions != nil {
		t.Errorf("zero Session.Permissions = %v, want nil", s.Permissions)
	}
	if s.Token != "" {
		t.Errorf("zero Session.Token = %q, want empty", s.Token)
	}
}

func TestSessionSafe(t *testing.T) {
	s := Session{
		UserID:      "user-1",
		Username:    "alice",
		Roles:       []string{"admin", "operator"},
		Permissions: []string{"storage.read", "storage.write"},
		Token:       "super-secret-token-12345",
	}

	safe := s.Safe()

	if safe.UserID != s.UserID {
		t.Errorf("Safe().UserID = %q, want %q", safe.UserID, s.UserID)
	}
	if safe.Username != s.Username {
		t.Errorf("Safe().Username = %q, want %q", safe.Username, s.Username)
	}
	if safe.Token != "" {
		t.Errorf("Safe().Token = %q, want empty (redacted)", safe.Token)
	}
	if len(safe.Roles) != len(s.Roles) {
		t.Errorf("Safe().Roles length = %d, want %d", len(safe.Roles), len(s.Roles))
	}
	for i, role := range s.Roles {
		if safe.Roles[i] != role {
			t.Errorf("Safe().Roles[%d] = %q, want %q", i, safe.Roles[i], role)
		}
	}
	if len(safe.Permissions) != len(s.Permissions) {
		t.Errorf("Safe().Permissions length = %d, want %d", len(safe.Permissions), len(s.Permissions))
	}
	for i, perm := range s.Permissions {
		if safe.Permissions[i] != perm {
			t.Errorf("Safe().Permissions[%d] = %q, want %q", i, safe.Permissions[i], perm)
		}
	}
}

func TestSessionSafeDoesNotMutateOriginal(t *testing.T) {
	s := Session{Token: "secret-token"}
	_ = s.Safe()
	if s.Token != "secret-token" {
		t.Error("Safe() should not modify the original session's Token")
	}
}

func TestSessionSafeRolesIsCopy(t *testing.T) {
	s := Session{Roles: []string{"admin"}}
	safe := s.Safe()
	safe.Roles[0] = "hacker"
	if s.Roles[0] != "admin" {
		t.Error("Safe() should return a copy of Roles, not a reference")
	}
}

func TestSessionSafePermissionsIsCopy(t *testing.T) {
	s := Session{Permissions: []string{"read"}}
	safe := s.Safe()
	safe.Permissions[0] = "write"
	if s.Permissions[0] != "read" {
		t.Error("Safe() should return a copy of Permissions, not a reference")
	}
}

func TestPermissionType(t *testing.T) {
	var p Permission
	if string(p) != "" {
		t.Errorf("zero Permission = %q, want empty", string(p))
	}

	p = Permission("storage.read")
	if string(p) != "storage.read" {
		t.Errorf("Permission = %q, want %q", string(p), "storage.read")
	}
}

func TestActionConstants(t *testing.T) {
	tests := map[Action]string{
		ActionCreate:  "create",
		ActionRead:    "read",
		ActionUpdate:  "update",
		ActionDelete:  "delete",
		ActionList:    "list",
		ActionExecute: "execute",
	}
	for action, expected := range tests {
		if string(action) != expected {
			t.Errorf("Action %s = %q, want %q", expected, string(action), expected)
		}
	}
}

func TestCredentialTypeConstants(t *testing.T) {
	tests := map[CredentialType]string{
		CredentialTypePassword: "password",
		CredentialTypeToken:    "token",
		CredentialTypeCert:     "cert",
		CredentialTypeAPIKey:   "api-key",
	}
	for ct, expected := range tests {
		if string(ct) != expected {
			t.Errorf("CredentialType %s = %q, want %q", expected, string(ct), expected)
		}
	}
}

func TestCredentialsZeroValue(t *testing.T) {
	var c Credentials
	if c.Type != "" {
		t.Errorf("zero Credentials.Type = %q, want empty", c.Type)
	}
	if c.Data != nil {
		t.Errorf("zero Credentials.Data = %v, want nil", c.Data)
	}
}

func TestCredentialsConstruction(t *testing.T) {
	c := Credentials{
		Type: "token",
		Data: []byte("bearer-token"),
	}
	if c.Type != "token" {
		t.Errorf("Credentials.Type = %q, want %q", c.Type, "token")
	}
	if string(c.Data) != "bearer-token" {
		t.Errorf("Credentials.Data = %q, want %q", string(c.Data), "bearer-token")
	}
}

func TestModuleStateConstants(t *testing.T) {
	tests := map[ModuleState]string{
		ModuleStateRegistered: "registered",
		ModuleStateStarting:   "starting",
		ModuleStateRunning:    "running",
		ModuleStateDegraded:   "degraded",
		ModuleStateStopping:   "stopping",
		ModuleStateStopped:    "stopped",
	}
	for state, expected := range tests {
		if string(state) != expected {
			t.Errorf("ModuleState %s = %q, want %q", expected, string(state), expected)
		}
	}
}

func TestModuleInfoZeroValue(t *testing.T) {
	var mi ModuleInfo
	if mi.ID != "" {
		t.Errorf("zero ModuleInfo.ID = %q, want empty", mi.ID)
	}
	if mi.Name != "" {
		t.Errorf("zero ModuleInfo.Name = %q, want empty", mi.Name)
	}
	if mi.Version != "" {
		t.Errorf("zero ModuleInfo.Version = %q, want empty", mi.Version)
	}
	if mi.Roles != nil {
		t.Errorf("zero ModuleInfo.Roles = %v, want nil", mi.Roles)
	}
	if mi.Description != "" {
		t.Errorf("zero ModuleInfo.Description = %q, want empty", mi.Description)
	}
	if mi.Author != "" {
		t.Errorf("zero ModuleInfo.Author = %q, want empty", mi.Author)
	}
	if mi.Capabilities != nil {
		t.Errorf("zero ModuleInfo.Capabilities = %v, want nil", mi.Capabilities)
	}
	if mi.Contracts != nil {
		t.Errorf("zero ModuleInfo.Contracts = %v, want nil", mi.Contracts)
	}
	if mi.DependsOn != nil {
		t.Errorf("zero ModuleInfo.DependsOn = %v, want nil", mi.DependsOn)
	}
	if mi.MinCoreVersion != "" {
		t.Errorf("zero ModuleInfo.MinCoreVersion = %q, want empty", mi.MinCoreVersion)
	}
	if mi.HTTPAddr != "" {
		t.Errorf("zero ModuleInfo.HTTPAddr = %q, want empty", mi.HTTPAddr)
	}
}

func TestModuleInfoConstruction(t *testing.T) {
	mi := ModuleInfo{
		ID:             "test-module",
		Name:           "Test Module",
		Version:        "1.0.0",
		Roles:          []string{"worker"},
		Description:    "A test module",
		Author:         "tester",
		Capabilities:   []string{"storage", "auth"},
		Contracts:      []ContractDeclaration{{Repo: "test", Version: "v1.0.0", Interface: "TestInterface"}},
		DependsOn:      []string{"core-db"},
		MinCoreVersion: "1.0.0",
		HTTPAddr:       ":8085",
	}
	if mi.ID != "test-module" {
		t.Errorf("ModuleInfo.ID = %q, want %q", mi.ID, "test-module")
	}
	if mi.Name != "Test Module" {
		t.Errorf("ModuleInfo.Name = %q, want %q", mi.Name, "Test Module")
	}
	if mi.HTTPAddr != ":8085" {
		t.Errorf("ModuleInfo.HTTPAddr = %q, want %q", mi.HTTPAddr, ":8085")
	}
}

func TestContractDeclarationZeroValue(t *testing.T) {
	var cd ContractDeclaration
	if cd.Repo != "" {
		t.Errorf("zero ContractDeclaration.Repo = %q, want empty", cd.Repo)
	}
	if cd.Version != "" {
		t.Errorf("zero ContractDeclaration.Version = %q, want empty", cd.Version)
	}
	if cd.Interface != "" {
		t.Errorf("zero ContractDeclaration.Interface = %q, want empty", cd.Interface)
	}
}

func TestModuleEntryZeroValue(t *testing.T) {
	var me ModuleEntry
	if me.Module != nil {
		t.Errorf("zero ModuleEntry.Module = %v, want nil", me.Module)
	}
	if me.State != "" {
		t.Errorf("zero ModuleEntry.State = %q, want empty", me.State)
	}
}

func TestObjectInfoZeroValue(t *testing.T) {
	var oi ObjectInfo
	if oi.Key != "" {
		t.Errorf("zero ObjectInfo.Key = %q, want empty", oi.Key)
	}
	if oi.Size != 0 {
		t.Errorf("zero ObjectInfo.Size = %d, want 0", oi.Size)
	}
	if oi.ContentType != "" {
		t.Errorf("zero ObjectInfo.ContentType = %q, want empty", oi.ContentType)
	}
	if oi.ETag != "" {
		t.Errorf("zero ObjectInfo.ETag = %q, want empty", oi.ETag)
	}
	if !oi.LastModified.IsZero() {
		t.Errorf("zero ObjectInfo.LastModified = %v, want zero", oi.LastModified)
	}
	if oi.Metadata != nil {
		t.Errorf("zero ObjectInfo.Metadata = %v, want nil", oi.Metadata)
	}
}

func TestObjectInfoConstruction(t *testing.T) {
	now := time.Now()
	meta := map[string]string{"checksum": "abc123"}
	oi := ObjectInfo{
		Key:          "test/path/file.txt",
		Size:         1024,
		ContentType:  "text/plain",
		ETag:         "\"etag-1\"",
		LastModified: now,
		Metadata:     meta,
	}
	if oi.Key != "test/path/file.txt" {
		t.Errorf("ObjectInfo.Key = %q, want %q", oi.Key, "test/path/file.txt")
	}
	if oi.Size != 1024 {
		t.Errorf("ObjectInfo.Size = %d, want %d", oi.Size, 1024)
	}
	if oi.ContentType != "text/plain" {
		t.Errorf("ObjectInfo.ContentType = %q, want %q", oi.ContentType, "text/plain")
	}
	if oi.ETag != "\"etag-1\"" {
		t.Errorf("ObjectInfo.ETag = %q, want %q", oi.ETag, "\"etag-1\"")
	}
	if !oi.LastModified.Equal(now) {
		t.Errorf("ObjectInfo.LastModified = %v, want %v", oi.LastModified, now)
	}
	if oi.Metadata["checksum"] != "abc123" {
		t.Errorf("ObjectInfo.Metadata = %v, want %v", oi.Metadata, meta)
	}
}

func TestStorageEventTypeConstants(t *testing.T) {
	tests := map[StorageEventType]string{
		StorageEventCreated:  "created",
		StorageEventDeleted:  "deleted",
		StorageEventModified: "modified",
	}
	for st, expected := range tests {
		if string(st) != expected {
			t.Errorf("StorageEventType %s = %q, want %q", expected, string(st), expected)
		}
	}
}

func TestStorageEventZeroValue(t *testing.T) {
	var se StorageEvent
	if se.Type != "" {
		t.Errorf("zero StorageEvent.Type = %q, want empty", se.Type)
	}
	if se.Key != "" {
		t.Errorf("zero StorageEvent.Key = %q, want empty", se.Key)
	}
}

func TestStorageTierConstants(t *testing.T) {
	tests := map[StorageTier]string{
		StorageTierHot:     "hot",
		StorageTierWarm:    "warm",
		StorageTierCold:    "cold",
		StorageTierArchive: "archive",
	}
	for tier, expected := range tests {
		if string(tier) != expected {
			t.Errorf("StorageTier %s = %q, want %q", expected, string(tier), expected)
		}
	}
}

func TestEventStorageTierTransition(t *testing.T) {
	if EventStorageTierTransition != "storage.tier.transition" {
		t.Errorf("EventStorageTierTransition = %q, want %q", EventStorageTierTransition, "storage.tier.transition")
	}
}

func TestTierTransitionPayloadZeroValue(t *testing.T) {
	var p TierTransitionPayload
	if p.Key != "" {
		t.Errorf("zero TierTransitionPayload.Key = %q, want empty", p.Key)
	}
	if p.FromTier != "" {
		t.Errorf("zero TierTransitionPayload.FromTier = %q, want empty", p.FromTier)
	}
	if p.ToTier != "" {
		t.Errorf("zero TierTransitionPayload.ToTier = %q, want empty", p.ToTier)
	}
	if p.Reason != "" {
		t.Errorf("zero TierTransitionPayload.Reason = %q, want empty", p.Reason)
	}
}

func TestCapabilitiesNonEmpty(t *testing.T) {
	caps := []string{
		CapabilitySecrets,
		CapabilityDatabase,
		CapabilityCache,
		CapabilityCacheLocal,
		CapabilityMetrics,
		CapabilityTracing,
		CapabilityCircuitBreaker,
		CapabilityConfigWatcher,
		CapabilityDeadLetter,
		CapabilityCallPolicy,
		CapabilityPublishPolicy,
		CapabilityIdentity,
		CapabilityLogging,
		CapabilityRetry,
		CapabilityIdempotency,
		CapabilityFeatureFlags,
		CapabilitySerialization,
		CapabilityEncryption,
		CapabilityDistributedLock,
		CapabilityDataRedaction,
		CapabilityEventStore,
		CapabilityInputValidate,
		CapabilityWorkflowEngine,
		CapabilitySpoolResolver,
		CapabilityScheduler,
		CapabilityHealthMonitor,
		CapabilityBackup,
		CapabilityAuth,
		CapabilityAuthorizer,
		CapabilityRateLimiter,
	}
	for _, cap := range caps {
		if cap == "" {
			t.Error("Capability constant should not be empty")
		}
	}
}

func TestIdentityZeroValue(t *testing.T) {
	var id Identity
	if id.ID != "" {
		t.Errorf("zero Identity.ID = %q, want empty", id.ID)
	}
	if id.Kind != "" {
		t.Errorf("zero Identity.Kind = %q, want empty", id.Kind)
	}
	if id.Roles != nil {
		t.Errorf("zero Identity.Roles = %v, want nil", id.Roles)
	}
	if id.Claims != nil {
		t.Errorf("zero Identity.Claims = %v, want nil", id.Claims)
	}
	if id.Extra != nil {
		t.Errorf("zero Identity.Extra = %v, want nil", id.Extra)
	}
}

func TestIdentitySafeInfo(t *testing.T) {
	id := Identity{
		ID:     "user-1",
		Kind:   "user",
		Roles:  []string{"admin"},
		Claims: map[string]any{"sub": "user-1", "email": "alice@example.com"},
		Extra:  map[string]any{"department": "eng"},
	}

	safe := id.SafeInfo()

	if safe.ID != id.ID {
		t.Errorf("SafeInfo().ID = %q, want %q", safe.ID, id.ID)
	}
	if safe.Kind != id.Kind {
		t.Errorf("SafeInfo().Kind = %q, want %q", safe.Kind, id.Kind)
	}
	if safe.Claims != nil {
		t.Errorf("SafeInfo().Claims = %v, want nil (stripped)", safe.Claims)
	}
	if safe.Extra != nil {
		t.Errorf("SafeInfo().Extra = %v, want nil (stripped)", safe.Extra)
	}
	if len(safe.Roles) != 1 || safe.Roles[0] != "admin" {
		t.Errorf("SafeInfo().Roles = %v, want [admin]", safe.Roles)
	}
}

func TestIdentitySafeInfoRolesIsCopy(t *testing.T) {
	id := Identity{Roles: []string{"admin"}}
	safe := id.SafeInfo()
	safe.Roles[0] = "hacker"
	if id.Roles[0] != "admin" {
		t.Error("SafeInfo() should return a copy of Roles, not reference")
	}
}

func TestIdentitySafeInfoDoesNotMutateOriginal(t *testing.T) {
	id := Identity{
		ID:    "user-1",
		Kind:  "user",
		Roles: []string{"admin"},
	}
	_ = id.SafeInfo()
	if id.ID != "user-1" || id.Kind != "user" || id.Roles[0] != "admin" {
		t.Error("SafeInfo() should not modify the original identity")
	}
}

func TestLogLevelConstants(t *testing.T) {
	tests := map[LogLevel]string{
		LogLevelDebug: "debug",
		LogLevelInfo:  "info",
		LogLevelWarn:  "warn",
		LogLevelError: "error",
	}
	for level, expected := range tests {
		if string(level) != expected {
			t.Errorf("LogLevel %s = %q, want %q", expected, string(level), expected)
		}
	}
}

func TestSensitiveLogFieldNames(t *testing.T) {
	names := SensitiveLogFieldNames()
	if len(names) == 0 {
		t.Fatal("SensitiveLogFieldNames() should return non-empty list")
	}
	seen := make(map[string]bool)
	for _, name := range names {
		if name == "" {
			t.Error("SensitiveLogFieldNames() contains empty string")
		}
		if seen[name] {
			t.Errorf("SensitiveLogFieldNames() contains duplicate %q", name)
		}
		seen[name] = true
	}
	if names[0] != "password" {
		t.Errorf("First sensitive field = %q, want %q", names[0], "password")
	}
}

func TestAuditEntryZeroValue(t *testing.T) {
	var ae AuditEntry
	if ae.ID != "" {
		t.Errorf("zero AuditEntry.ID = %q, want empty", ae.ID)
	}
	if !ae.Timestamp.IsZero() {
		t.Errorf("zero AuditEntry.Timestamp = %v, want zero", ae.Timestamp)
	}
	if ae.Actor != "" {
		t.Errorf("zero AuditEntry.Actor = %q, want empty", ae.Actor)
	}
	if ae.Action != "" {
		t.Errorf("zero AuditEntry.Action = %q, want empty", ae.Action)
	}
	if ae.Resource != "" {
		t.Errorf("zero AuditEntry.Resource = %q, want empty", ae.Resource)
	}
	if ae.ResourceID != "" {
		t.Errorf("zero AuditEntry.ResourceID = %q, want empty", ae.ResourceID)
	}
	if ae.Details != nil {
		t.Errorf("zero AuditEntry.Details = %v, want nil", ae.Details)
	}
	if ae.TraceID != "" {
		t.Errorf("zero AuditEntry.TraceID = %q, want empty", ae.TraceID)
	}
	if ae.NodeID != "" {
		t.Errorf("zero AuditEntry.NodeID = %q, want empty", ae.NodeID)
	}
	if ae.PrevEntryHash != "" {
		t.Errorf("zero AuditEntry.PrevEntryHash = %q, want empty", ae.PrevEntryHash)
	}
	if ae.Signature != "" {
		t.Errorf("zero AuditEntry.Signature = %q, want empty", ae.Signature)
	}
}

func TestChainVerificationResultZeroValue(t *testing.T) {
	var cvr ChainVerificationResult
	if cvr.Valid {
		t.Error("zero ChainVerificationResult.Valid should be false")
	}
	if cvr.TotalEntries != 0 {
		t.Errorf("zero ChainVerificationResult.TotalEntries = %d, want 0", cvr.TotalEntries)
	}
	if cvr.BrokenLinks != nil {
		t.Errorf("zero ChainVerificationResult.BrokenLinks = %v, want nil", cvr.BrokenLinks)
	}
	if !cvr.FirstBrokenAt.IsZero() {
		t.Errorf("zero ChainVerificationResult.FirstBrokenAt = %v, want zero", cvr.FirstBrokenAt)
	}
}

func TestAuditFilterZeroValue(t *testing.T) {
	var af AuditFilter
	if af.Actor != "" {
		t.Errorf("zero AuditFilter.Actor = %q, want empty", af.Actor)
	}
	if af.Action != "" {
		t.Errorf("zero AuditFilter.Action = %q, want empty", af.Action)
	}
	if af.Resource != "" {
		t.Errorf("zero AuditFilter.Resource = %q, want empty", af.Resource)
	}
	if !af.From.IsZero() {
		t.Errorf("zero AuditFilter.From = %v, want zero", af.From)
	}
	if !af.To.IsZero() {
		t.Errorf("zero AuditFilter.To = %v, want zero", af.To)
	}
	if af.TraceID != "" {
		t.Errorf("zero AuditFilter.TraceID = %q, want empty", af.TraceID)
	}
	if af.VerifyChain {
		t.Error("zero AuditFilter.VerifyChain should be false")
	}
}

func TestNodeInfoZeroValue(t *testing.T) {
	var ni NodeInfo
	if ni.ID != "" {
		t.Errorf("zero NodeInfo.ID = %q, want empty", ni.ID)
	}
	if ni.GRPCAddr != "" {
		t.Errorf("zero NodeInfo.GRPCAddr = %q, want empty", ni.GRPCAddr)
	}
	if ni.HTTPAddr != "" {
		t.Errorf("zero NodeInfo.HTTPAddr = %q, want empty", ni.HTTPAddr)
	}
	if ni.Labels != nil {
		t.Errorf("zero NodeInfo.Labels = %v, want nil", ni.Labels)
	}
	if ni.ModuleIDs != nil {
		t.Errorf("zero NodeInfo.ModuleIDs = %v, want nil", ni.ModuleIDs)
	}
}

func TestClusterEventTypeConstants(t *testing.T) {
	tests := map[ClusterEventType]string{
		ClusterNodeJoined:    "node.joined",
		ClusterNodeLeft:      "node.left",
		ClusterNodeDegraded:  "node.degraded",
		ClusterLeaderChanged: "leader.changed",
	}
	for ct, expected := range tests {
		if string(ct) != expected {
			t.Errorf("ClusterEventType %s = %q, want %q", expected, string(ct), expected)
		}
	}
}

func TestClusterEventConstants(t *testing.T) {
	tests := map[string]string{
		"EventClusterNodeJoined":    EventClusterNodeJoined,
		"EventClusterNodeLeft":      EventClusterNodeLeft,
		"EventClusterNodeDegraded":  EventClusterNodeDegraded,
		"EventClusterLeaderChanged": EventClusterLeaderChanged,
	}
	expected := map[string]string{
		"EventClusterNodeJoined":    "cluster.node.joined",
		"EventClusterNodeLeft":      "cluster.node.left",
		"EventClusterNodeDegraded":  "cluster.node.degraded",
		"EventClusterLeaderChanged": "cluster.leader.changed",
	}
	for name, val := range tests {
		if val != expected[name] {
			t.Errorf("%s = %q, want %q", name, val, expected[name])
		}
	}
}

func TestClusterEventZeroValue(t *testing.T) {
	var ce ClusterEvent
	if ce.Type != "" {
		t.Errorf("zero ClusterEvent.Type = %q, want empty", ce.Type)
	}
	if ce.LeaderID != "" {
		t.Errorf("zero ClusterEvent.LeaderID = %q, want empty", ce.LeaderID)
	}
}

func TestNodeJoinedPayloadZeroValue(t *testing.T) {
	var p NodeJoinedPayload
	if p.NodeID != "" {
		t.Errorf("zero NodeJoinedPayload.NodeID = %q, want empty", p.NodeID)
	}
	if p.GRPCAddr != "" {
		t.Errorf("zero NodeJoinedPayload.GRPCAddr = %q, want empty", p.GRPCAddr)
	}
	if p.HTTPAddr != "" {
		t.Errorf("zero NodeJoinedPayload.HTTPAddr = %q, want empty", p.HTTPAddr)
	}
}

func TestNodeLeftPayloadZeroValue(t *testing.T) {
	var p NodeLeftPayload
	if p.NodeID != "" {
		t.Errorf("zero NodeLeftPayload.NodeID = %q, want empty", p.NodeID)
	}
}

func TestLeaderChangedPayloadZeroValue(t *testing.T) {
	var p LeaderChangedPayload
	if p.PreviousLeader != "" {
		t.Errorf("zero LeaderChangedPayload.PreviousLeader = %q, want empty", p.PreviousLeader)
	}
	if p.NewLeader != "" {
		t.Errorf("zero LeaderChangedPayload.NewLeader = %q, want empty", p.NewLeader)
	}
}

func TestCircuitStateConstants(t *testing.T) {
	tests := map[CircuitState]string{
		CircuitClosed:   "closed",
		CircuitOpen:     "open",
		CircuitHalfOpen: "half_open",
	}
	for cs, expected := range tests {
		if string(cs) != expected {
			t.Errorf("CircuitState %s = %q, want %q", expected, string(cs), expected)
		}
	}
}

func TestDatabaseParamsZeroValue(t *testing.T) {
	var dp DatabaseParams
	if dp.Driver != "" {
		t.Errorf("zero DatabaseParams.Driver = %q, want empty", dp.Driver)
	}
	if dp.Host != "" {
		t.Errorf("zero DatabaseParams.Host = %q, want empty", dp.Host)
	}
	if dp.Database != "" {
		t.Errorf("zero DatabaseParams.Database = %q, want empty", dp.Database)
	}
	if dp.User != "" {
		t.Errorf("zero DatabaseParams.User = %q, want empty", dp.User)
	}
	if dp.Password != "" {
		t.Errorf("zero DatabaseParams.Password = %q, want empty", dp.Password)
	}
	if dp.Extra != nil {
		t.Errorf("zero DatabaseParams.Extra = %v, want nil", dp.Extra)
	}
}

func TestDatabaseParamsConstruction(t *testing.T) {
	dp := DatabaseParams{
		Driver:   "postgres",
		Host:     "localhost:5432",
		Database: "muxcore",
		User:     "app",
		Password: "s3cret",
		Extra:    map[string]string{"sslmode": "require"},
	}
	if dp.Driver != "postgres" {
		t.Errorf("DatabaseParams.Driver = %q, want %q", dp.Driver, "postgres")
	}
	if dp.Extra["sslmode"] != "require" {
		t.Errorf("DatabaseParams.Extra = %v, want %v", dp.Extra, map[string]string{"sslmode": "require"})
	}
}

func TestMigrationZeroValue(t *testing.T) {
	var m Migration
	if m.Version != 0 {
		t.Errorf("zero Migration.Version = %d, want 0", m.Version)
	}
	if m.Name != "" {
		t.Errorf("zero Migration.Name = %q, want empty", m.Name)
	}
	if m.Up != "" {
		t.Errorf("zero Migration.Up = %q, want empty", m.Up)
	}
	if m.Down != "" {
		t.Errorf("zero Migration.Down = %q, want empty", m.Down)
	}
}

func TestDeadLetterEntryZeroValue(t *testing.T) {
	var dle DeadLetterEntry
	if dle.HandlerName != "" {
		t.Errorf("zero DeadLetterEntry.HandlerName = %q, want empty", dle.HandlerName)
	}
	if dle.Error != "" {
		t.Errorf("zero DeadLetterEntry.Error = %q, want empty", dle.Error)
	}
	if !dle.FailedAt.IsZero() {
		t.Errorf("zero DeadLetterEntry.FailedAt = %v, want zero", dle.FailedAt)
	}
	if dle.RetryCount != 0 {
		t.Errorf("zero DeadLetterEntry.RetryCount = %d, want 0", dle.RetryCount)
	}
}

func TestRetryPolicyZeroValue(t *testing.T) {
	var rp RetryPolicy
	if rp.MaxAttempts != 0 {
		t.Errorf("zero RetryPolicy.MaxAttempts = %d, want 0", rp.MaxAttempts)
	}
	if rp.InitialDelay != 0 {
		t.Errorf("zero RetryPolicy.InitialDelay = %v, want 0", rp.InitialDelay)
	}
	if rp.MaxDelay != 0 {
		t.Errorf("zero RetryPolicy.MaxDelay = %v, want 0", rp.MaxDelay)
	}
	if rp.BackoffFactor != 0 {
		t.Errorf("zero RetryPolicy.BackoffFactor = %f, want 0", rp.BackoffFactor)
	}
	if rp.Jitter {
		t.Error("zero RetryPolicy.Jitter should be false")
	}
	if rp.Extra != nil {
		t.Errorf("zero RetryPolicy.Extra = %v, want nil", rp.Extra)
	}
}

func TestSchedulerTaskZeroValue(t *testing.T) {
	var st SchedulerTask
	if st.ID != "" {
		t.Errorf("zero SchedulerTask.ID = %q, want empty", st.ID)
	}
	if st.Name != "" {
		t.Errorf("zero SchedulerTask.Name = %q, want empty", st.Name)
	}
	if st.CronExpr != "" {
		t.Errorf("zero SchedulerTask.CronExpr = %q, want empty", st.CronExpr)
	}
	if st.Payload != nil {
		t.Errorf("zero SchedulerTask.Payload = %v, want nil", st.Payload)
	}
	if st.Timeout != 0 {
		t.Errorf("zero SchedulerTask.Timeout = %v, want 0", st.Timeout)
	}
	if st.Meta != nil {
		t.Errorf("zero SchedulerTask.Meta = %v, want nil", st.Meta)
	}
}

func TestSchedulerTaskStatusConstants(t *testing.T) {
	tests := map[SchedulerTaskStatus]string{
		SchedulerTaskScheduled: "scheduled",
		SchedulerTaskRunning:   "running",
		SchedulerTaskCompleted: "completed",
		SchedulerTaskFailed:    "failed",
		SchedulerTaskCancelled: "cancelled",
	}
	for sts, expected := range tests {
		if string(sts) != expected {
			t.Errorf("SchedulerTaskStatus %s = %q, want %q", expected, string(sts), expected)
		}
	}
}

func TestSchedulerTaskFilterZeroValue(t *testing.T) {
	var stf SchedulerTaskFilter
	if stf.Status != "" {
		t.Errorf("zero SchedulerTaskFilter.Status = %q, want empty", stf.Status)
	}
	if stf.Name != "" {
		t.Errorf("zero SchedulerTaskFilter.Name = %q, want empty", stf.Name)
	}
}

func TestTapestryStatusConstants(t *testing.T) {
	tests := map[TapestryStatus]string{
		TapestryPending:   "pending",
		TapestryRunning:   "running",
		TapestryCompleted: "completed",
		TapestryFailed:    "failed",
		TapestryCancelled: "cancelled",
		TapestryPaused:    "paused",
	}
	for ts, expected := range tests {
		if string(ts) != expected {
			t.Errorf("TapestryStatus %s = %q, want %q", expected, string(ts), expected)
		}
	}
}

func TestStepHandlerKindConstants(t *testing.T) {
	tests := map[StepHandlerKind]string{
		StepHandlerModule:     "module",
		StepHandlerCapability: "capability",
	}
	for shk, expected := range tests {
		if string(shk) != expected {
			t.Errorf("StepHandlerKind %s = %q, want %q", expected, string(shk), expected)
		}
	}
}

func TestTapestryEventConstants(t *testing.T) {
	events := map[string]string{
		EventTapestryStarted:   "tapestry.started",
		EventTapestryCompleted: "tapestry.completed",
		EventTapestryFailed:    "tapestry.failed",
		EventTapestryCancelled: "tapestry.cancelled",
		EventTapestryPaused:    "tapestry.paused",
		EventTapestryResumed:   "tapestry.resumed",
		EventStepStarted:       "tapestry.step.started",
		EventStepCompleted:     "tapestry.step.completed",
		EventStepFailed:        "tapestry.step.failed",
	}
	for constant, expected := range events {
		if constant != expected {
			t.Errorf("Event constant value mismatch: got %q, want %q", constant, expected)
		}
	}
}

func TestTapestryRunZeroValue(t *testing.T) {
	var tr TapestryRun
	if tr.ID != "" {
		t.Errorf("zero TapestryRun.ID = %q, want empty", tr.ID)
	}
	if tr.DefinitionID != "" {
		t.Errorf("zero TapestryRun.DefinitionID = %q, want empty", tr.DefinitionID)
	}
	if tr.Status != "" {
		t.Errorf("zero TapestryRun.Status = %q, want empty", tr.Status)
	}
	if tr.CurrentStep != "" {
		t.Errorf("zero TapestryRun.CurrentStep = %q, want empty", tr.CurrentStep)
	}
	if tr.StepResults != nil {
		t.Errorf("zero TapestryRun.StepResults = %v, want nil", tr.StepResults)
	}
	if tr.Input != nil {
		t.Errorf("zero TapestryRun.Input = %v, want nil", tr.Input)
	}
	if !tr.StartedAt.IsZero() {
		t.Errorf("zero TapestryRun.StartedAt = %v, want zero", tr.StartedAt)
	}
	if !tr.EndedAt.IsZero() {
		t.Errorf("zero TapestryRun.EndedAt = %v, want zero", tr.EndedAt)
	}
	if tr.Error != "" {
		t.Errorf("zero TapestryRun.Error = %q, want empty", tr.Error)
	}
}

func TestStepHandlerZeroValue(t *testing.T) {
	var sh StepHandler
	if sh.Kind != "" {
		t.Errorf("zero StepHandler.Kind = %q, want empty", sh.Kind)
	}
	if sh.Ref != "" {
		t.Errorf("zero StepHandler.Ref = %q, want empty", sh.Ref)
	}
}

func TestTapestryStepZeroValue(t *testing.T) {
	var ts TapestryStep
	if ts.Name != "" {
		t.Errorf("zero TapestryStep.Name = %q, want empty", ts.Name)
	}
	if ts.Retry != 0 {
		t.Errorf("zero TapestryStep.Retry = %d, want 0", ts.Retry)
	}
	if ts.Timeout != 0 {
		t.Errorf("zero TapestryStep.Timeout = %v, want 0", ts.Timeout)
	}
	if ts.DependsOn != nil {
		t.Errorf("zero TapestryStep.DependsOn = %v, want nil", ts.DependsOn)
	}
	if ts.InputMapping != nil {
		t.Errorf("zero TapestryStep.InputMapping = %v, want nil", ts.InputMapping)
	}
}

func TestTapestryDefinitionZeroValue(t *testing.T) {
	var td TapestryDefinition
	if td.ID != "" {
		t.Errorf("zero TapestryDefinition.ID = %q, want empty", td.ID)
	}
	if td.Name != "" {
		t.Errorf("zero TapestryDefinition.Name = %q, want empty", td.Name)
	}
	if td.Description != "" {
		t.Errorf("zero TapestryDefinition.Description = %q, want empty", td.Description)
	}
	if td.Steps != nil {
		t.Errorf("zero TapestryDefinition.Steps = %v, want nil", td.Steps)
	}
	if td.Version != "" {
		t.Errorf("zero TapestryDefinition.Version = %q, want empty", td.Version)
	}
}

func TestTapestryRunFilterZeroValue(t *testing.T) {
	var trf TapestryRunFilter
	if trf.Status != "" {
		t.Errorf("zero TapestryRunFilter.Status = %q, want empty", trf.Status)
	}
	if trf.DefinitionID != "" {
		t.Errorf("zero TapestryRunFilter.DefinitionID = %q, want empty", trf.DefinitionID)
	}
}

func TestSettingTypeConstants(t *testing.T) {
	tests := map[SettingType]string{
		SettingTypeString: "string",
		SettingTypeInt:    "int",
		SettingTypeBool:   "bool",
		SettingTypeSelect: "select",
		SettingTypeSecret: "secret",
	}
	for st, expected := range tests {
		if string(st) != expected {
			t.Errorf("SettingType %s = %q, want %q", expected, string(st), expected)
		}
	}
}

func TestSettingDefZeroValue(t *testing.T) {
	var sd SettingDef
	if sd.Key != "" {
		t.Errorf("zero SettingDef.Key = %q, want empty", sd.Key)
	}
	if sd.Label != "" {
		t.Errorf("zero SettingDef.Label = %q, want empty", sd.Label)
	}
	if sd.Type != "" {
		t.Errorf("zero SettingDef.Type = %q, want empty", sd.Type)
	}
	if sd.Default != "" {
		t.Errorf("zero SettingDef.Default = %q, want empty", sd.Default)
	}
	if sd.Description != "" {
		t.Errorf("zero SettingDef.Description = %q, want empty", sd.Description)
	}
	if sd.Required {
		t.Error("zero SettingDef.Required should be false")
	}
	if sd.Options != nil {
		t.Errorf("zero SettingDef.Options = %v, want nil", sd.Options)
	}
	if sd.Group != "" {
		t.Errorf("zero SettingDef.Group = %q, want empty", sd.Group)
	}
}

func TestSafeContentTypeConstants(t *testing.T) {
	if SafeContentTypeJSON != "application/json" {
		t.Errorf("SafeContentTypeJSON = %q, want %q", SafeContentTypeJSON, "application/json")
	}
	if SafeContentTypeProtobuf != "application/x-protobuf" {
		t.Errorf("SafeContentTypeProtobuf = %q, want %q", SafeContentTypeProtobuf, "application/x-protobuf")
	}
	if SafeContentTypeMsgpack != "application/msgpack" {
		t.Errorf("SafeContentTypeMsgpack = %q, want %q", SafeContentTypeMsgpack, "application/msgpack")
	}
}

func TestSpanStatusCodeValues(t *testing.T) {
	if SpanStatusOK != 0 {
		t.Errorf("SpanStatusOK = %d, want 0", SpanStatusOK)
	}
	if SpanStatusError != 1 {
		t.Errorf("SpanStatusError = %d, want 1", SpanStatusError)
	}
}

func TestTagModuleZeroValue(t *testing.T) {
	var tm TagModule
	if tm.Repo != "" {
		t.Errorf("zero TagModule.Repo = %q, want empty", tm.Repo)
	}
	if tm.Version != "" {
		t.Errorf("zero TagModule.Version = %q, want empty", tm.Version)
	}
	if tm.Required {
		t.Error("zero TagModule.Required should be false")
	}
	if tm.Checksum != "" {
		t.Errorf("zero TagModule.Checksum = %q, want empty", tm.Checksum)
	}
}

func TestTagDefinitionZeroValue(t *testing.T) {
	var td TagDefinition
	if td.Name != "" {
		t.Errorf("zero TagDefinition.Name = %q, want empty", td.Name)
	}
	if td.Description != "" {
		t.Errorf("zero TagDefinition.Description = %q, want empty", td.Description)
	}
	if td.Version != "" {
		t.Errorf("zero TagDefinition.Version = %q, want empty", td.Version)
	}
	if td.Modules != nil {
		t.Errorf("zero TagDefinition.Modules = %v, want nil", td.Modules)
	}
}

func TestBackupInfoZeroValue(t *testing.T) {
	var bi BackupInfo
	if bi.ID != "" {
		t.Errorf("zero BackupInfo.ID = %q, want empty", bi.ID)
	}
	if !bi.Timestamp.IsZero() {
		t.Errorf("zero BackupInfo.Timestamp = %v, want zero", bi.Timestamp)
	}
	if bi.Size != 0 {
		t.Errorf("zero BackupInfo.Size = %d, want 0", bi.Size)
	}
	if bi.Modules != nil {
		t.Errorf("zero BackupInfo.Modules = %v, want nil", bi.Modules)
	}
}

func TestValidationResultZeroValue(t *testing.T) {
	var vr ValidationResult
	if vr.Valid {
		t.Error("zero ValidationResult.Valid should be false")
	}
	if vr.Sanitized != nil {
		t.Errorf("zero ValidationResult.Sanitized = %v, want nil", vr.Sanitized)
	}
	if vr.Errors != nil {
		t.Errorf("zero ValidationResult.Errors = %v, want nil", vr.Errors)
	}
}

func TestEventStoreEntryZeroValue(t *testing.T) {
	var ese EventStoreEntry
	if ese.Stream != "" {
		t.Errorf("zero EventStoreEntry.Stream = %q, want empty", ese.Stream)
	}
	if ese.Sequence != 0 {
		t.Errorf("zero EventStoreEntry.Sequence = %d, want 0", ese.Sequence)
	}
	if !ese.StoredAt.IsZero() {
		t.Errorf("zero EventStoreEntry.StoredAt = %v, want zero", ese.StoredAt)
	}
	if ese.Extra != nil {
		t.Errorf("zero EventStoreEntry.Extra = %v, want nil", ese.Extra)
	}
}

func TestResourceDescriptorZeroValue(t *testing.T) {
	var rd ResourceDescriptor
	if rd.Type != "" {
		t.Errorf("zero ResourceDescriptor.Type = %q, want empty", rd.Type)
	}
	if rd.ID != "" {
		t.Errorf("zero ResourceDescriptor.ID = %q, want empty", rd.ID)
	}
	if rd.Attributes != nil {
		t.Errorf("zero ResourceDescriptor.Attributes = %v, want nil", rd.Attributes)
	}
}

func TestResourceDescriptorConstruction(t *testing.T) {
	rd := ResourceDescriptor{
		Type:       "media",
		ID:         "movie-123",
		Attributes: map[string]string{"owner": "alice"},
	}
	if rd.Type != "media" {
		t.Errorf("ResourceDescriptor.Type = %q, want %q", rd.Type, "media")
	}
	if rd.ID != "movie-123" {
		t.Errorf("ResourceDescriptor.ID = %q, want %q", rd.ID, "movie-123")
	}
	if rd.Attributes["owner"] != "alice" {
		t.Errorf("ResourceDescriptor.Attributes = %v, want %v", rd.Attributes, map[string]string{"owner": "alice"})
	}
}

func TestTapestryWorkflowPayloadsZeroValue(t *testing.T) {
	var sp TapestryStartedPayload
	if sp.RunID != "" {
		t.Errorf("zero TapestryStartedPayload.RunID = %q, want empty", sp.RunID)
	}
	if sp.DefinitionID != "" {
		t.Errorf("zero TapestryStartedPayload.DefinitionID = %q, want empty", sp.DefinitionID)
	}

	var cp TapestryCompletedPayload
	if cp.RunID != "" {
		t.Errorf("zero TapestryCompletedPayload.RunID = %q, want empty", cp.RunID)
	}
	if cp.Duration != "" {
		t.Errorf("zero TapestryCompletedPayload.Duration = %q, want empty", cp.Duration)
	}

	var fp TapestryFailedPayload
	if fp.StepName != "" {
		t.Errorf("zero TapestryFailedPayload.StepName = %q, want empty", fp.StepName)
	}
	if fp.Error != "" {
		t.Errorf("zero TapestryFailedPayload.Error = %q, want empty", fp.Error)
	}

	var ssp StepStartedPayload
	if ssp.StepName != "" {
		t.Errorf("zero StepStartedPayload.StepName = %q, want empty", ssp.StepName)
	}
	if ssp.Handler != "" {
		t.Errorf("zero StepStartedPayload.Handler = %q, want empty", ssp.Handler)
	}
	if ssp.Attempt != 0 {
		t.Errorf("zero StepStartedPayload.Attempt = %d, want 0", ssp.Attempt)
	}

	var scp StepCompletedPayload
	if scp.StepName != "" {
		t.Errorf("zero StepCompletedPayload.StepName = %q, want empty", scp.StepName)
	}
	if scp.Output != nil {
		t.Errorf("zero StepCompletedPayload.Output = %v, want nil", scp.Output)
	}

	var sfp StepFailedPayload
	if sfp.StepName != "" {
		t.Errorf("zero StepFailedPayload.StepName = %q, want empty", sfp.StepName)
	}
	if sfp.Attempt != 0 {
		t.Errorf("zero StepFailedPayload.Attempt = %d, want 0", sfp.Attempt)
	}
	if sfp.MaxRetry != 0 {
		t.Errorf("zero StepFailedPayload.MaxRetry = %d, want 0", sfp.MaxRetry)
	}
	if sfp.Error != "" {
		t.Errorf("zero StepFailedPayload.Error = %q, want empty", sfp.Error)
	}
}

func TestEventHandlerType(t *testing.T) {
	var fn EventHandler
	if fn != nil {
		t.Error("zero EventHandler should be nil")
	}

	called := false
	fn = func(ctx context.Context, event Event) error {
		called = true
		return nil
	}

	if fn == nil {
		t.Error("assigned EventHandler should not be nil")
	}

	_ = fn(context.Background(), Event{})
	if !called {
		t.Error("EventHandler was not called")
	}
}

func TestModuleKind(t *testing.T) {
	var mk ModuleKind
	if string(mk) != "" {
		t.Errorf("zero ModuleKind = %q, want empty", string(mk))
	}
	mk = ModuleKind("auth")
	if string(mk) != "auth" {
		t.Errorf("ModuleKind = %q, want %q", string(mk), "auth")
	}
}

func TestStepResultZeroValue(t *testing.T) {
	var sr StepResult
	if sr.StepName != "" {
		t.Errorf("zero StepResult.StepName = %q, want empty", sr.StepName)
	}
	if sr.Status != "" {
		t.Errorf("zero StepResult.Status = %q, want empty", sr.Status)
	}
	if !sr.StartedAt.IsZero() {
		t.Errorf("zero StepResult.StartedAt = %v, want zero", sr.StartedAt)
	}
	if !sr.EndedAt.IsZero() {
		t.Errorf("zero StepResult.EndedAt = %v, want zero", sr.EndedAt)
	}
	if sr.Attempt != 0 {
		t.Errorf("zero StepResult.Attempt = %d, want 0", sr.Attempt)
	}
	if sr.MaxRetries != 0 {
		t.Errorf("zero StepResult.MaxRetries = %d, want 0", sr.MaxRetries)
	}
	if sr.Output != nil {
		t.Errorf("zero StepResult.Output = %v, want nil", sr.Output)
	}
	if sr.Error != "" {
		t.Errorf("zero StepResult.Error = %q, want empty", sr.Error)
	}
}

func TestInterfaceComplianceCompileTime(t *testing.T) {
	_ = func(EventBus) {}
	_ = func(Module) {}
	_ = func(Registry) {}
	_ = func(Authorizer) {}
	_ = func(ResourceAuthorizer) {}
	_ = func(AuthProvider) {}
	_ = func(StorageOrchestrator) {}
	_ = func(StorageProvider) {}
	_ = func(Streamable) {}
	_ = func(Seekable) {}
	_ = func(Watchable) {}
	_ = func(AtomicMovable) {}
	_ = func(Hardlinkable) {}
	_ = func(TieredProvider) {}
	_ = func(HealthMonitor) {}
	_ = func(Cluster) {}
	_ = func(SecretsProvider) {}
	_ = func(StructuredLogger) {}
	_ = func(RedactingLogger) {}
	_ = func(AuditLogger) {}
	_ = func(MetricsProvider) {}
	_ = func(TracingProvider) {}
	_ = func(Span) {}
	_ = func(CircuitBreaker) {}
	_ = func(DistributedLockProvider) {}
	_ = func(IdempotencyProvider) {}
	_ = func(EncryptionProvider) {}
	_ = func(FeatureFlagProvider) {}
	_ = func(DataRedactionProvider) {}
	_ = func(SpoolResolver) {}
	_ = func(Scheduler) {}
	_ = func(WorkflowEngine) {}
	_ = func(RetryProvider) {}
	_ = func(RateLimiterProvider) {}
	_ = func(DatabaseProvider) {}
	_ = func(Rows) {}
	_ = func(Tx) {}
	_ = func(CacheProvider) {}
	_ = func(CacheLayer) {}
	_ = func(LockHandle) {}
	_ = func(DistributedLockHandle) {}
	_ = func(ModuleMeshClient) {}
	_ = func(MeshHandler) {}
	_ = func(PublishPolicyProvider) {}
	_ = func(ResourcePublishPolicyProvider) {}
	_ = func(CallPolicyProvider) {}
	_ = func(InputValidator) {}
	_ = func(SettingsProvider) {}
	_ = func(SettingsUpdater) {}
	_ = func(BackupProvider) {}
	_ = func(Backupable) {}
	_ = func(IdentityProvider) {}
	_ = func(EventStore) {}
	_ = func(DeadLetterProvider) {}
	_ = func(SerializationProvider) {}
	_ = func(ConfigWatcher) {}
	_ = func(Counter) {}
	_ = func(Gauge) {}
	_ = func(Histogram) {}

	// EventHandler is a function type — verify it matches the godoc signature.
	var _ EventHandler = func(context.Context, Event) error { return nil }
}
