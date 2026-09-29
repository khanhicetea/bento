// Package runtime plans Docker resources for Bento: central naming/labels,
// generated per-app configuration, container specifications, and the
// non-secret generation fingerprint.
package runtime

import (
	"errors"
	"maps"
	"regexp"
)

// Label keys. Labels identify managed resources for observation; they are not
// authentication (anyone with Docker access can forge them).
const (
	LabelManaged    = "io.bento.managed"
	LabelStackID    = "io.bento.stack-id"
	LabelAppID      = "io.bento.app-id"
	LabelRole       = "io.bento.role"
	LabelGeneration = "io.bento.generation"
	LabelOperation  = "io.bento.operation-id"
	LabelService    = "io.bento.service"
	LabelImageKey   = "io.bento.image-key"
)

type Role string

const (
	RoleRuntime  Role = "runtime"
	RoleTool     Role = "tool"
	RoleEdge     Role = "edge"
	RoleDatabase Role = "database"
	RoleCache    Role = "cache"
	RoleTunnel   Role = "tunnel"
	RoleBackup   Role = "backup"
	RoleDBAdmin  Role = "dbadmin"
	RoleNetwork  Role = "network"
	RoleVolume   Role = "volume"
	RoleProbe    Role = "probe"
)

var stackNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,22}[a-z0-9]$`)

// ValidateStackName checks the human stack name used as a Docker name prefix.
func ValidateStackName(name string) error {
	if !stackNamePattern.MatchString(name) {
		return errors.New("stack name must be 2-24 lowercase letters, digits, or hyphens, starting with a letter")
	}
	return nil
}

// Names derives Docker names for one stack. Names are display and routing
// conventions; ownership is always verified from labels and configuration.
type Names struct {
	StackID   string
	StackName string
}

func (n Names) prefix() string                   { return "bento-" + n.StackName }
func (n Names) AppsNetwork() string              { return n.prefix() + "-apps" }
func (n Names) DataNetwork() string              { return n.prefix() + "-data" }
func (n Names) AppContainer(appID string) string { return n.prefix() + "-app-" + appID }
func (n Names) ToolContainer(appID, opID string) string {
	return n.prefix() + "-tool-" + appID + "-" + opID
}
func (n Names) EdgeContainer() string    { return n.prefix() + "-edge" }
func (n Names) TunnelContainer() string  { return n.prefix() + "-cloudflared" }
func (n Names) DBAdminContainer() string { return n.prefix() + "-dbadmin" }
func (n Names) ServiceContainer(service string) string {
	return n.prefix() + "-" + service
}
func (n Names) ServiceVolume(service string) string { return n.prefix() + "-" + service + "-data" }
func (n Names) BackupContainer(opID string) string  { return n.prefix() + "-job-" + opID }

// AppAlias is the stable network-scoped routing alias for an app.
func AppAlias(appID string) string { return "app-" + appID }

// Labels builds the label set for a resource.
func (n Names) Labels(role Role, extra map[string]string) map[string]string {
	l := map[string]string{
		LabelManaged: "true",
		LabelStackID: n.StackID,
		LabelRole:    string(role),
	}
	maps.Copy(l, extra)
	return l
}

// StackSelector selects every managed resource of this stack.
func (n Names) StackSelector() map[string]string {
	return map[string]string{LabelManaged: "true", LabelStackID: n.StackID}
}

// OwnedBy reports whether labels claim this stack and role (and app if given).
func (n Names) OwnedBy(labels map[string]string, role Role, appID string) bool {
	if labels[LabelManaged] != "true" || labels[LabelStackID] != n.StackID || labels[LabelRole] != string(role) {
		return false
	}
	if appID != "" && labels[LabelAppID] != appID {
		return false
	}
	return true
}
