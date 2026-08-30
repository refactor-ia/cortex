// Package qaactor renders immutable Pi actor values from admitted QA sources.
package qaactor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/refactor-ia/cortex/internal/qarole"
)

// ActorContractVersion identifies the versioned Pi actor byte contract.
const ActorContractVersion = "cortex.qa.pi-actor.v1"

// Actor is one immutable rendered Pi actor document.
type Actor struct {
	roleID              qarole.RoleID
	roleContractVersion string
	description         string
	sourceSHA256        string
	generatedSHA256     string
	content             []byte
}

func (actor Actor) RoleID() qarole.RoleID {
	return actor.roleID
}
func (actor Actor) RoleContractVersion() string {
	return actor.roleContractVersion
}
func (actor Actor) Description() string {
	return actor.description
}
func (actor Actor) SourceSHA256() string {
	return actor.sourceSHA256
}
func (actor Actor) GeneratedSHA256() string {
	return actor.generatedSHA256
}
func (actor Actor) Content() []byte {
	return append([]byte(nil), actor.content...)
}

// Set is the immutable rendered catalog for the six QA roles.
type Set struct {
	actorContract      string
	catalogFingerprint string
	sources            []Source
	actors             []Actor
}

func (set Set) ActorContract() string {
	return set.actorContract
}
func (set Set) CatalogFingerprint() string {
	return set.catalogFingerprint
}
func (set Set) Sources() []Source {
	sources := make([]Source, len(set.sources))
	for index, source := range set.sources {
		sources[index] = cloneSource(source)
	}
	return sources
}

func (set Set) Actors() []Actor {
	actors := make([]Actor, len(set.actors))
	for index, actor := range set.actors {
		actors[index] = cloneActor(actor)
	}
	return actors
}

// Render creates exact Pi actor documents for one usable closed source set.
func Render(sourceSet SourceSet) (Set, error) {
	if sourceSet.catalogFingerprint == "" || len(sourceSet.sources) != len(qarole.Catalog()) {
		return Set{}, errors.New("QA actor render requires a usable closed source set")
	}

	sources := sourceSet.Sources()
	actors := make([]Actor, len(sources))
	for index, source := range sources {
		content := renderContent(source)
		actors[index] = Actor{
			roleID:              source.RoleID(),
			roleContractVersion: source.RoleContractVersion(),
			description:         source.Description(),
			sourceSHA256:        source.SourceSHA256(),
			generatedSHA256:     fmt.Sprintf("%x", sha256.Sum256(content)),
			content:             content,
		}
	}

	return Set{
		actorContract:      ActorContractVersion,
		catalogFingerprint: sourceSet.CatalogFingerprint(),
		sources:            sources,
		actors:             actors,
	}, nil
}

func renderContent(source Source) []byte {
	frontmatter := strings.Join([]string{
		"---",
		"name: cortex-" + string(source.RoleID()),
		"description: " + yamlScalar(source.Description()),
		"subagent_mode: task",
		"tools:",
		"  - read",
		"  - grep",
		"  - find",
		"  - ls",
		"---",
		"",
		"",
	}, "\n")
	return append([]byte(frontmatter), source.Body()...)
}

func yamlScalar(value string) string {
	if isPlainYAMLScalar(value) {
		return value
	}
	return strconv.Quote(value)
}

func isPlainYAMLScalar(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "---") || strings.HasPrefix(value, "...") {
		return false
	}
	if value == "~" || strings.EqualFold(value, "null") || strings.EqualFold(value, "true") || strings.EqualFold(value, "false") {
		return false
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return false
	}
	if strings.ContainsAny(value, "\r\n\t") || strings.Contains(value, ": ") || strings.Contains(value, " #") {
		return false
	}
	if strings.ContainsRune("-?:,[]{}#&*!|>'\"%@`", rune(value[0])) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func cloneActor(actor Actor) Actor {
	actor.content = append([]byte(nil), actor.content...)
	return actor
}
