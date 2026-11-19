package desktop

// accountLocal are record fields that belong to the account (or to this
// app instance's runtime) rather than to the conversation. They are never
// copied to another account's folder, never compared, and a record keeps its
// own values of them when the rest is updated.
//
// Copying them is not just wasteful (remoteMcpServersConfig alone can be
// 250 KB per record) but wrong: MCP tool grants, Remote Control bridges,
// computer-use grants, scheduled tasks and published artifacts are tied to
// the account that made them.
var accountLocal = map[string]bool{
	// MCP servers and tools available to the account.
	"remoteMcpServersConfig": true,
	"enabledMcpTools":        true,
	"toolSurfaceSnapshot":    true,
	// Remote Control.
	"bridgeSessionIds":                true,
	"remoteControlAutoEligible":       true,
	"remoteControlUserEnabled":        true,
	"steeredByRemoteClient":           true,
	"sendToProjectRemoteControlOffer": true,
	// Errors of a run in this account.
	"error":          true,
	"errorAt":        true,
	"priorErrorMark": true,
	// Computer use and Chrome grants.
	"cuGrantFlags":         true,
	"cuAllowedApps":        true,
	"cuFlagsGrantedAt":     true,
	"cuSelectedDisplayId":  true,
	"cuLastScreenshotDims": true,
	"chromeTabGroupId":     true,
	// Permissions granted in this account.
	"alwaysAllowedReasons":     true,
	"sessionPermissionUpdates": true,
	// Account-owned objects.
	"scheduledTaskId":    true,
	"publishedArtifacts": true,
	// Runtime prompt state the app regenerates.
	"promptAppendSnapshot":  true,
	"pendingSystemReminder": true,
	// The record's own id.
	"sessionId": true,
}

// IsSynced reports whether a record field is part of the conversation and
// is kept equal across accounts.
func IsSynced(key string) bool { return !accountLocal[key] }
