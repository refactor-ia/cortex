package qaadmission

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var diagnosticRedactors = []struct {
	class string
	expr  *regexp.Regexp
}{
	{"credential-assignment", regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|password|secret)\b[ \t]*[:=][ \t]*(?:"[^"\r\n]{1,1000}"|'[^'\r\n]{1,1000}'|[A-Za-z0-9._~+/=-]{8,1000})`)},
	{"authorization-bearer", regexp.MustCompile(`(?i)authorization[ \t]*:[ \t]*bearer[ \t]+[A-Za-z0-9._~+/-]{8,1000}={0,2}`)},
	{"authorization-basic", regexp.MustCompile(`(?i)authorization[ \t]*:[ \t]*basic[ \t]+[A-Za-z0-9+/]{8,1000}={0,2}`)},
	{"private-key", regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
}

func RedactDiagnostic(source string, raw []byte, limit int) (*BoundedDiagnostic, error) {
	if (source != "stdout" && source != "stderr") || limit < 0 || limit > MaxDiagnosticBytes {
		return nil, fmt.Errorf("invalid diagnostic source or limit")
	}
	redacted := string(raw)
	classes := make([]string, 0, len(diagnosticRedactors))
	for _, redactor := range diagnosticRedactors {
		if redactor.expr.MatchString(redacted) {
			classes = append(classes, redactor.class)
			redacted = redactor.expr.ReplaceAllString(redacted, "[REDACTED:"+redactor.class+"]")
		}
	}
	retained := []byte(redacted)
	completeness, truncation := "complete", "none"
	if len(retained) > limit {
		retained = retained[:limit]
		for !utf8.Valid(retained) {
			retained = retained[:len(retained)-1]
		}
		completeness, truncation = "truncated", fmt.Sprint(len(retained))
	}
	redaction := "none"
	if len(classes) > 0 {
		redaction = strings.Join(classes, ",")
	}
	digest := sha256.Sum256(retained)
	return &BoundedDiagnostic{
		Source:         source,
		Redaction:      redaction,
		ObservedBytes:  fmt.Sprint(len(raw)),
		RetainedBytes:  fmt.Sprint(len(retained)),
		RetainedSHA256: fmt.Sprintf("%x", digest),
		Truncation:     truncation,
		Completeness:   completeness,
	}, nil
}
