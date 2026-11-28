package qaroute

import "github.com/refactor-ia/cortex/internal/qarole"

// EffectiveRoute is one role's resolved route on one admitted backend, as a
// read-only view for `cortex qa routes` and the interactive configurator
// (#457, #454). Source is "profile:<id>", the resolver's policy default id, or
// "unset" when the resolver refuses the row with route_incomplete; an unset
// row carries no provider, model or effort.
type EffectiveRoute struct {
	Role     qarole.RoleID
	Backend  string
	Provider string
	Model    string
	Effort   string
	Source   string
}

// Backends lists the admitted execution backends in stable display order.
// Every entry is admitted by allowedBackends; the order only fixes output.
func Backends() []string {
	return []string{"pi", "claude", "opencode"}
}
