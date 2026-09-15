package qarole

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
)

const productionCatalogRoot = "../../catalog"

func TestProductionQARoleSourcesDecodeAndMeetContracts(t *testing.T) {
	catalogData, err := os.ReadFile(filepath.Join(productionCatalogRoot, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := catalog.DecodeCatalogManifest(catalogData)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Families) != len(catalog.ApprovedFamilyIDs()) {
		t.Fatalf("catalog family count = %d, want %d", len(root.Families), len(catalog.ApprovedFamilyIDs()))
	}

	familyData, err := os.ReadFile(filepath.Join(productionCatalogRoot, "families/quality-assurance/family.json"))
	if err != nil {
		t.Fatal(err)
	}
	family, err := catalog.DecodeFamilyManifest(familyData)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := roleIDs(Catalog())
	if family.ID != "quality-assurance" || !reflect.DeepEqual(family.Agents, wantIDs) || len(family.Capabilities) != len(wantIDs) {
		t.Fatalf("QA family = %+v, want six general-core roles only", family)
	}

	for _, contract := range Catalog() {
		manifestData, err := os.ReadFile(filepath.Join(productionCatalogRoot, "families/quality-assurance/capabilities", string(contract.ID)+".json"))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := catalog.DecodeCapabilityManifest(manifestData)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.ID != string(contract.ID) || manifest.Family != family.ID || manifest.Source != "families/quality-assurance/sources/"+string(contract.ID)+".md" || manifest.Provenance != catalog.ProvenanceCortexOwned || manifest.License != "CC-BY-SA-4.0" || !manifest.RedistributionAllowed {
			t.Fatalf("manifest for %q is not an owned QA source: %+v", contract.ID, manifest)
		}
		source, err := os.ReadFile(filepath.Join(productionCatalogRoot, manifest.Source))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateSourceContract(contract, string(source)); err != nil {
			t.Fatalf("source %q violates its contract: %v", contract.ID, err)
		}
	}
}

func TestValidateSourceContractRejectsEachOmittedMarker(t *testing.T) {
	for _, contract := range Catalog() {
		source := validSourceContract(contract)
		for _, marker := range sourceMarkers(contract) {
			t.Run(string(contract.ID)+"/"+marker, func(t *testing.T) {
				withoutMarker := strings.Replace(source, "<!-- cortex-qa:"+marker+" -->\n", "", 1)
				if err := ValidateSourceContract(contract, withoutMarker); err == nil {
					t.Fatalf("ValidateSourceContract() accepted a source without %q", marker)
				}
			})
		}
	}
}

func validSourceContract(contract RoleContract) string {
	return "# QA role\n" + strings.Join(markerComments(sourceMarkers(contract)), "\n") + "\n"
}

func markerComments(markers []string) []string {
	comments := make([]string, len(markers))
	for index, marker := range markers {
		comments[index] = "<!-- cortex-qa:" + marker + " -->"
	}
	return comments
}

func roleIDs(contracts []RoleContract) []string {
	ids := make([]string, len(contracts))
	for index, contract := range contracts {
		ids[index] = string(contract.ID)
	}
	return ids
}
