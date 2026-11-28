package qapi

import (
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// EffectiveRoutes reports the route `qa run` would resolve for every role (in
// qarole.Catalog order) on every admitted backend, through the same
// resolveProfileRoute call, so the view never disagrees with a run (#457). A
// route_incomplete row is reported as unset; any other refusal, such as
// profile_invalid, aborts the whole view with the code `qa run` reports. It
// only reads the profile snapshot and writes nothing.
func EffectiveRoutes(catalogRoot, profile string) ([]qaroute.EffectiveRoute, qaroute.Failure) {
	var routes []qaroute.EffectiveRoute
	for _, role := range qarole.Catalog() {
		for _, backend := range qaroute.Backends() {
			resolved, failure := resolveProfileRoute(catalogRoot, AdmissionRequest{Role: role.ID, Backend: backend, Profile: profile})
			row := qaroute.EffectiveRoute{Role: role.ID, Backend: backend, Source: "unset"}
			switch {
			case failure.Code == "route_incomplete":
			case failure.Code != "":
				return nil, failure
			default:
				row.Provider, row.Model, row.Effort = resolved.Provider, resolved.Model, resolved.Effort
				row.Source = resolved.ProfileID
				if resolved.ProfileSHA256 != "" {
					row.Source = "profile:" + resolved.ProfileID
				}
			}
			routes = append(routes, row)
		}
	}
	return routes, qaroute.Failure{}
}
