package qagit

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const candidateContract = "cortex.qa.clean-candidate.v1"

type Request struct {
	CWD, Revision, Fingerprint string
}

type Binding struct {
	ObjectFormat, Revision, Tree, Fingerprint, CWDIdentity string
}

type Runner interface {
	Run(context.Context, Command) (Result, error)
}

func VerifyCleanBinding(ctx context.Context, request Request, runner Runner) (Binding, error) {
	if ctx == nil || !canonicalDirectory(request.CWD) || !validOID(request.Revision, 40) && !validOID(request.Revision, 64) || !validFingerprint(request.Fingerprint) {
		return Binding{}, errors.New("invalid clean binding request")
	}
	if runner == nil {
		runner = systemRunner{}
	}
	commands := fixedCommands(request.CWD)
	values := make([]string, len(commands)-1)
	for i, command := range commands {
		result, err := runner.Run(ctx, command)
		if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
			return Binding{}, errors.New("git clean binding command failed")
		}
		if i == len(commands)-1 {
			if len(result.Stdout) != 0 {
				return Binding{}, errors.New("git candidate is not clean")
			}
			continue
		}
		value, ok := commandOutput(result.Stdout)
		if !ok {
			return Binding{}, errors.New("invalid git clean binding output")
		}
		values[i] = value
	}
	if values[0] != request.CWD || !canonicalDirectory(values[1]) || (values[2] != "sha1" && values[2] != "sha256") {
		return Binding{}, errors.New("invalid git clean binding identity")
	}
	length := 40
	if values[2] == "sha256" {
		length = 64
	}
	if !validOID(values[3], length) || !validOID(values[4], length) || values[3] != request.Revision {
		return Binding{}, errors.New("stale git clean binding")
	}
	fingerprint := candidateFingerprint(values[2], values[3], values[4])
	if fingerprint != request.Fingerprint {
		return Binding{}, errors.New("stale git candidate fingerprint")
	}
	return Binding{ObjectFormat: values[2], Revision: values[3], Tree: values[4], Fingerprint: fingerprint, CWDIdentity: cwdPathIdentity(values[0])}, nil
}

func canonicalDirectory(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}

func commandOutput(output []byte) (string, bool) {
	value := strings.TrimSuffix(string(output), "\n")
	return value, strings.HasSuffix(string(output), "\n") && !strings.ContainsAny(value, "\r\n")
}

func validOID(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func validFingerprint(value string) bool {
	return strings.HasPrefix(value, "candidate.") && validOID(strings.TrimPrefix(value, "candidate."), 64)
}

// cwdPathIdentity returns "cwd." plus the lowercase SHA-256 of two frames:
// an 8-byte big-endian domain byte length followed by "cortex.qa.cwd.v1",
// then an 8-byte big-endian canonical Git worktree-root path byte length
// followed by the exact path bytes.
func cwdPathIdentity(root string) string {
	hash := sha256.New()
	for _, value := range []string{"cortex.qa.cwd.v1", root} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "cwd." + fmt.Sprintf("%x", hash.Sum(nil))
}

func candidateFingerprint(format, revision, tree string) string {
	hash := sha256.New()
	for _, value := range []string{candidateContract, format, revision, tree} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "candidate." + fmt.Sprintf("%x", hash.Sum(nil))
}
