package releasecatalog

import (
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
)

const testFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testAdmission(id string, catalogVersion int, fingerprint string) admission {
	return admission{id: id, catalogVersion: catalogVersion, fingerprint: fingerprint}
}

func TestNewSourceRejectsInvalidAdmissions(t *testing.T) {
	valid := testAdmission(identityFor(1, testFingerprint), 1, testFingerprint)
	cases := []struct {
		name       string
		admissions []admission
	}{
		{name: "empty identity", admissions: []admission{testAdmission("", 1, testFingerprint)}},
		{name: "identity does not match evidence", admissions: []admission{testAdmission("cortex-1", 1, testFingerprint)}},
		{name: "invalid catalog version", admissions: []admission{testAdmission(identityFor(0, testFingerprint), 0, testFingerprint)}},
		{name: "malformed fingerprint", admissions: []admission{testAdmission(identityFor(1, "not-a-fingerprint"), 1, "not-a-fingerprint")}},
		{name: "duplicate identity", admissions: []admission{valid, valid}},
		{name: "overlapping fingerprint", admissions: []admission{valid, testAdmission(identityFor(2, testFingerprint), 2, testFingerprint)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newSource(tc.admissions); err == nil {
				t.Fatal("newSource() error = nil, want rejected admission")
			}
		})
	}
}

func TestNewSourceOrdersAndIsolatesAdmissions(t *testing.T) {
	secondFingerprint := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	input := []admission{
		testAdmission(identityFor(2, secondFingerprint), 2, secondFingerprint),
		testAdmission(identityFor(1, testFingerprint), 1, testFingerprint),
	}
	source, err := newSource(input)
	if err != nil {
		t.Fatalf("newSource() error = %v", err)
	}

	input[0] = admission{}
	want := []admission{
		testAdmission(identityFor(1, testFingerprint), 1, testFingerprint),
		testAdmission(identityFor(2, secondFingerprint), 2, secondFingerprint),
	}
	if !reflect.DeepEqual(source.admissions, want) {
		t.Fatalf("newSource() admissions = %#v, want %#v", source.admissions, want)
	}
}

func TestSourceResolvesOnlyExactPrivateEvidence(t *testing.T) {
	source, err := newSource([]admission{testAdmission(identityFor(1, testFingerprint), 1, testFingerprint)})
	if err != nil {
		t.Fatalf("newSource() error = %v", err)
	}

	cases := []struct {
		name     string
		evidence snapshotEvidence
		wantErr  bool
	}{
		{name: "exact match", evidence: snapshotEvidence{id: identityFor(1, testFingerprint), catalogVersion: 1, fingerprint: testFingerprint}},
		{name: "unknown identity", evidence: snapshotEvidence{id: identityFor(1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), catalogVersion: 1, fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, wantErr: true},
		{name: "catalog version mismatch", evidence: snapshotEvidence{id: identityFor(2, testFingerprint), catalogVersion: 2, fingerprint: testFingerprint}, wantErr: true},
		{name: "fingerprint mismatch", evidence: snapshotEvidence{id: identityFor(1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), catalogVersion: 1, fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := source.resolveEvidence(tc.evidence)
			if tc.wantErr {
				if err == nil {
					t.Fatal("resolveEvidence() error = nil, want rejection")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveEvidence() error = %v", err)
			}
			if got.evidence() != tc.evidence {
				t.Fatalf("resolveEvidence() = %#v, want %#v", got.evidence(), tc.evidence)
			}
		})
	}
}

func TestBuiltInSourceAdmitsNoSnapshots(t *testing.T) {
	source := BuiltInSource()
	if got := len(source.admissions); got != 0 {
		t.Fatalf("BuiltInSource() admissions = %d, want 0", got)
	}
	if _, err := source.resolveEvidence(snapshotEvidence{id: identityFor(1, testFingerprint), catalogVersion: 1, fingerprint: testFingerprint}); err == nil {
		t.Fatal("BuiltInSource().resolveEvidence() error = nil, want rejection")
	}
	if _, err := source.ResolveSnapshot(catalog.CatalogSnapshot{}); err == nil {
		t.Fatal("BuiltInSource().ResolveSnapshot() error = nil, want rejection")
	}
}
