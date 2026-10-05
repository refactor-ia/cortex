package qaactor

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func identityDigest(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func identitySuffix() []byte {
	return []byte("\n\n" + testGuidanceStart + "\nuntrusted guidance\r\n\t  \n" + testGuidanceEnd + "\n")
}

func TestGuidanceIdentityRenderedUpdates(t *testing.T) {
	sources, previous := testRendered(t)
	desiredSources := sources.Sources()
	for i := range desiredSources {
		desiredSources[i].body = []byte(strings.Replace(string(desiredSources[i].body), "# ", "# Updated ", 1))
		refreshSourceHash(&desiredSources[i])
	}
	desired, err := Render(SourceSet{catalogFingerprint: sources.CatalogFingerprint(), sources: desiredSources})
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Actors()) != 6 {
		t.Fatal("expected all six real rendered fixtures")
	}
	for i, actor := range previous.Actors() {
		t.Run(string(actor.RoleID()), func(t *testing.T) {
			canonical, next := actor.Content(), desired.Actors()[i].Content()
			if bytes.Equal(canonical, next) || !bytes.HasSuffix(canonical, []byte("\n")) || bytes.HasSuffix(canonical, []byte("\n\n")) {
				t.Fatal("fixture must change and have exactly one canonical trailing LF")
			}
			for _, guidance := range []bool{false, true} {
				for _, registered := range []bool{false, true} {
					t.Run(fmt.Sprintf("guidance=%t/registered=%t", guidance, registered), func(t *testing.T) {
						current, want := canonical, next
						if guidance {
							current = append(bytes.TrimRight(canonical, "\n"), identitySuffix()...)
							want = append(bytes.TrimRight(next, "\n"), identitySuffix()...)
						}
						prior := GuidanceIdentity{CanonicalSHA256: actor.GeneratedSHA256(), AdoptLegacyGuidance: guidance && !registered}
						if registered {
							prior.EffectiveSHA256 = identityDigest(current)
						}
						beforeCurrent, beforeNext := bytes.Clone(current), bytes.Clone(next)
						got, err := ComposeGuidanceWithIdentity(current, next, prior)
						if err != nil || !bytes.Equal(got, want) {
							t.Fatalf("identity update = %q, error = %v", got, err)
						}
						if !bytes.Equal(current, beforeCurrent) || !bytes.Equal(next, beforeNext) {
							t.Fatal("composition mutated input")
						}
						got[0] = 'X'
						if current[0] == 'X' || next[0] == 'X' {
							t.Fatal("output aliases input")
						}
					})
				}
			}
		})
	}
}

func TestGuidanceIdentityRejectsUnprovedUpdates(t *testing.T) {
	_, rendered := testRendered(t)
	canonical := rendered.Actors()[0].Content()
	current := append(bytes.TrimRight(canonical, "\n"), identitySuffix()...)
	legacy := GuidanceIdentity{CanonicalSHA256: identityDigest(canonical), AdoptLegacyGuidance: true}
	registered := legacy
	registered.EffectiveSHA256 = identityDigest(current)
	withoutOptIn := legacy
	withoutOptIn.AdoptLegacyGuidance = false
	wrong := legacy
	wrong.CanonicalSHA256 = identityDigest(rendered.Actors()[1].Content())
	for _, tc := range []struct {
		name             string
		current, desired []byte
		prior            GuidanceIdentity
	}{
		{"missing opt-in", current, canonical, withoutOptIn},
		{"wrong canonical", current, canonical, wrong},
		{"effective block tamper despite opt-in", bytes.Replace(current, []byte("untrusted"), []byte("changed"), 1), canonical, registered},
		{"effective LF tamper", append(bytes.Clone(current), '\n'), canonical, registered},
		{"outside drift", bytes.Replace(current, []byte("  - ls"), []byte("  - bash"), 1), canonical, legacy},
		{"unmarked LF drift", bytes.TrimSuffix(canonical, []byte("\n")), canonical, legacy},
		{"malformed suffix", bytes.TrimSuffix(current, []byte("\n")), canonical, legacy},
		{"extra separator LF", bytes.Replace(current, []byte("\n\n"+testGuidanceStart), []byte("\n\n\n"+testGuidanceStart), 1), canonical, legacy},
		{"duplicate suffix", append(bytes.Clone(current), identitySuffix()...), canonical, legacy},
		{"empty desired", current, nil, legacy},
		{"marked desired", current, current, legacy},
		{"malformed desired", current, append(bytes.Clone(canonical), []byte(testGuidanceTool)...), legacy},
		{"arbitrary legacy body", []byte("arbitrary body"), canonical, legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := bytes.Clone(tc.current)
			got, err := ComposeGuidanceWithIdentity(tc.current, tc.desired, tc.prior)
			if err == nil || got != nil || !bytes.Equal(tc.current, before) {
				t.Fatalf("unproved update returned bytes or mutated input: %v", err)
			}
			if strings.HasPrefix(tc.name, "effective") && !strings.Contains(err.Error(), "registered effective identity") {
				t.Fatalf("effective identity must reject before composition/layout checks: %v", err)
			}
		})
	}
	// A matching effective digest cannot bypass canonical proof or suffix parsing.
	for _, data := range [][]byte{bytes.Replace(current, []byte("  - ls"), []byte("  - bash"), 1), bytes.TrimSuffix(current, []byte("\n"))} {
		prior := registered
		prior.EffectiveSHA256 = identityDigest(data)
		if got, err := ComposeGuidanceWithIdentity(data, canonical, prior); err == nil || got != nil {
			t.Fatal("effective identity bypassed canonical/layout proof")
		}
	}
}

func TestGuidanceIdentityInvalidHashes(t *testing.T) {
	_, rendered := testRendered(t)
	canonical := rendered.Actors()[0].Content()
	for _, invalid := range []string{"", "bad", strings.Repeat("g", 64), strings.ToUpper(identityDigest(canonical)), identityDigest(canonical) + " "} {
		for _, effective := range []bool{false, true} {
			if effective && invalid == "" {
				continue // Empty effective digest explicitly selects legacy identity.
			}
			t.Run(fmt.Sprintf("%q/effective=%t", invalid, effective), func(t *testing.T) {
				prior := GuidanceIdentity{CanonicalSHA256: identityDigest(canonical)}
				if effective {
					prior.EffectiveSHA256 = invalid
				} else {
					prior.CanonicalSHA256 = invalid
				}
				if got, err := ComposeGuidanceWithIdentity(canonical, canonical, prior); err == nil || got != nil {
					t.Fatal("invalid hash accepted")
				}
			})
		}
	}
}

func TestGuidanceIdentityEndingCompatibility(t *testing.T) {
	sources, _ := testRendered(t)
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("prior-LFs=%d", count), func(t *testing.T) {
			changed := sources.Sources()
			changed[0].body = append(bytes.TrimRight(changed[0].body, "\n"), bytes.Repeat([]byte("\n"), count)...)
			refreshSourceHash(&changed[0])
			rendered, err := Render(SourceSet{catalogFingerprint: sources.CatalogFingerprint(), sources: changed})
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(rendered); err != nil {
				t.Fatalf("ending must be a valid canonical render: %v", err)
			}
			canonical := rendered.Actors()[0].Content()
			for _, guidance := range []bool{false, true} {
				for _, registered := range []bool{false, true} {
					current := bytes.Clone(canonical)
					if guidance {
						current = append(bytes.TrimRight(current, "\n"), identitySuffix()...)
					}
					prior := GuidanceIdentity{CanonicalSHA256: identityDigest(canonical), AdoptLegacyGuidance: true}
					if registered {
						prior.EffectiveSHA256 = identityDigest(current)
					}
					before := bytes.Clone(current)
					got, err := ComposeGuidanceWithIdentity(current, canonical, prior)
					if !bytes.Equal(current, before) {
						t.Fatal("ending compatibility check mutated current bytes")
					}
					if guidance && count != 1 {
						if err == nil || got != nil || !strings.Contains(err.Error(), "one-LF") {
							t.Fatalf("unsupported historical ending accepted or unclear error: %v", err)
						}
					} else if err != nil || !bytes.Equal(got, current) {
						t.Fatalf("supported exact identity rejected: %v", err)
					}
				}
			}
		})
	}
}
