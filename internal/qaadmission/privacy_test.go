package qaadmission

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactDiagnosticRedactsBeforeAccountingDigestAndCanonicalReceipt(t *testing.T) {
	raw := []byte("Authorization: Bearer abcdefgh\npassword = hunter2xx")
	diagnostic, err := RedactDiagnostic("stderr", raw, MaxDiagnosticBytes)
	if err != nil {
		t.Fatal(err)
	}
	redacted := "[REDACTED:authorization-bearer]\n[REDACTED:credential-assignment]"
	if diagnostic.ObservedBytes != fmt.Sprint(len(raw)) || diagnostic.RetainedBytes != fmt.Sprint(len(redacted)) {
		t.Fatalf("metadata = %#v", diagnostic)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(redacted)))
	if diagnostic.RetainedSHA256 != expected || diagnostic.Redaction != "credential-assignment,authorization-bearer" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}

	receipt := testReceipt()
	receipt.Diagnostic = diagnostic
	receipt.ReceiptID = ReceiptID(receipt)
	encoded, err := CanonicalJSON(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"abcdefgh", "hunter2xx"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("canonical receipt retained %q", secret)
		}
	}
}

func TestRedactDiagnosticRecognizesDocumentedClassesOnly(t *testing.T) {
	for _, tc := range []struct {
		name, raw, class string
	}{
		{"credential-assignment", "api_key: abcdefgh", "credential-assignment"},
		{"bearer", "authorization: bearer abcdefgh", "authorization-bearer"},
		{"basic", "Authorization: Basic dXNlcjpwYXNz", "authorization-basic"},
		{"private-key", "-----BEGIN RSA PRIVATE KEY-----\nmaterial\n-----END RSA PRIVATE KEY-----", "private-key"},
		{"ordinary-token", "token=abcdefgh", "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnostic, err := RedactDiagnostic("stdout", []byte(tc.raw), MaxDiagnosticBytes)
			if err != nil {
				t.Fatal(err)
			}
			if diagnostic.Redaction != tc.class {
				t.Fatalf("redaction = %q, want %q", diagnostic.Redaction, tc.class)
			}
		})
	}
}

func TestRedactDiagnosticPreservesUTF8AtCapBoundary(t *testing.T) {
	diagnostic, err := RedactDiagnostic("stdout", []byte("éé"), 3)
	if err != nil {
		t.Fatal(err)
	}
	retained := []byte("é")
	expected := fmt.Sprintf("%x", sha256.Sum256(retained))
	if diagnostic.Completeness != "truncated" || diagnostic.Truncation != "2" || diagnostic.RetainedBytes != "2" || diagnostic.RetainedSHA256 != expected {
		t.Fatalf("UTF-8 boundary = %#v", diagnostic)
	}
	if !utf8.Valid(retained) {
		t.Fatal("retained diagnostic fixture is not valid UTF-8")
	}

	for _, tc := range []struct {
		name   string
		source string
		limit  int
	}{
		{"invalid-source", "other", MaxDiagnosticBytes},
		{"negative-limit", "stdout", -1},
		{"over-limit", "stdout", MaxDiagnosticBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diagnostic, err := RedactDiagnostic(tc.source, []byte("safe"), tc.limit); err == nil || diagnostic != nil {
				t.Fatalf("RedactDiagnostic(%q, %d) = %#v, %v", tc.source, tc.limit, diagnostic, err)
			}
		})
	}
}

func TestRedactDiagnosticCapBoundaryUsesRedactedBytes(t *testing.T) {
	const retained = "[REDACTED:authorization-bearer]"
	diagnostic, err := RedactDiagnostic("stdout", []byte("Authorization: Bearer abcdefgh"), len(retained))
	if err != nil || diagnostic.Completeness != "complete" || diagnostic.Truncation != "none" {
		t.Fatalf("at cap = %#v, %v", diagnostic, err)
	}
	diagnostic, err = RedactDiagnostic("stdout", []byte("Authorization: Bearer abcdefgh"), len(retained)-1)
	if err != nil || diagnostic.Completeness != "truncated" || diagnostic.Truncation != fmt.Sprint(len(retained)-1) {
		t.Fatalf("one byte under = %#v, %v", diagnostic, err)
	}
}
