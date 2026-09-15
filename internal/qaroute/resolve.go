package qaroute

import (
	"crypto/sha256"
	"fmt"
)

func Resolve(request Request, snapshot Snapshot) (ResolvedRoute, Failure) {
	base, allowed := defaults[request.Role]
	if !allowed || request.Backend != "pi" {
		return ResolvedRoute{}, Failure{Code: "route_disallowed"}
	}
	profileID, digest := "role-default", ""
	if request.ProfileID != "" {
		if !snapshot.Present || !profileIDPattern.MatchString(request.ProfileID) {
			return ResolvedRoute{}, Failure{Code: "profile_invalid"}
		}
		profiles, valid := decodeProfiles(snapshot.Bytes)
		if !valid {
			return ResolvedRoute{}, Failure{Code: "profile_invalid"}
		}
		selected, exists := profiles[request.ProfileID]
		if !exists {
			return ResolvedRoute{}, Failure{Code: "profile_invalid"}
		}
		var found bool
		base, found = selected.routes[request.Role]
		if !found {
			return ResolvedRoute{}, Failure{Code: "route_incomplete"}
		}
		profileID = request.ProfileID
		digest = fmt.Sprintf("%x", sha256.Sum256(snapshot.Bytes))
	}
	fields := make([]string, 0, 3)
	if request.Override.Provider != "" {
		base.provider = request.Override.Provider
		fields = append(fields, "provider")
	}
	if request.Override.Model != "" {
		base.model = request.Override.Model
		fields = append(fields, "model")
	}
	if request.Override.Effort != "" {
		base.effort = request.Override.Effort
		fields = append(fields, "effort")
	}
	if base.provider == "" || base.model == "" || base.effort == "" {
		return ResolvedRoute{}, Failure{Code: "route_incomplete"}
	}
	if base.provider != "nan" {
		return ResolvedRoute{}, Failure{Code: "non_nan_route"}
	}
	if base != defaults[request.Role] {
		return ResolvedRoute{}, Failure{Code: "route_disallowed"}
	}
	return ResolvedRoute{
		PolicyVersion: PolicyVersion, Role: request.Role, Backend: request.Backend,
		Provider: base.provider, Model: base.model, Effort: base.effort,
		ProfileID: profileID, ProfileSHA256: digest, OverrideFields: fields,
	}, Failure{}
}
