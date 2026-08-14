package releaseartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var BinaryNames = []string{"gpt-tunnel-gatewayd", "gpt-tunnel", "gpt-tunnelctl"}

// BuildSourceRevision is embedded into every control binary by the canonical
// release builder. It is deliberately exposed through the binaries' bounded
// --source-sha probe rather than inferred from an MCP tool name.
var BuildSourceRevision = "unknown"

var sourceRevisionRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func Paths(gateway string) map[string]string {
	dir := filepath.Dir(gateway)
	return map[string]string{
		"gpt-tunnel-gatewayd": gateway,
		"gpt-tunnel":          filepath.Join(dir, "gpt-tunnel"),
		"gpt-tunnelctl":       filepath.Join(dir, "gpt-tunnelctl"),
	}
}

func ValidateRelease(dir, target string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"SHA256SUMS": true}
	for _, name := range BinaryNames {
		allowed[name] = true
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !allowed[entry.Name()] || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected release artifact %s", entry.Name())
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || (entry.Name() != "SHA256SUMS" && info.Mode()&0o111 == 0) {
			return fmt.Errorf("invalid release artifact %s", entry.Name())
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	expected := append([]string{"SHA256SUMS"}, append([]string(nil), BinaryNames...)...)
	sort.Strings(expected)
	if strings.Join(names, "\x00") != strings.Join(expected, "\x00") {
		return fmt.Errorf("release output set mismatch")
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(BinaryNames))
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fields[0]) || seen[fields[1]] || fields[1] == "SHA256SUMS" {
			return fmt.Errorf("invalid checksum manifest")
		}
		validName := false
		for _, name := range BinaryNames {
			if fields[1] == name {
				validName = true
			}
		}
		if !validName {
			return fmt.Errorf("invalid checksum manifest")
		}
		got, err := HashFile(filepath.Join(dir, fields[1]))
		if err != nil || got != fields[0] {
			return fmt.Errorf("checksum mismatch for %s", fields[1])
		}
		seen[fields[1]] = true
	}
	if len(seen) != len(BinaryNames) {
		return fmt.Errorf("checksum manifest is incomplete")
	}
	for _, name := range BinaryNames {
		got, err := BinaryVersion(filepath.Join(dir, name))
		if err != nil || got != target {
			return fmt.Errorf("release version mismatch for %s", name)
		}
	}
	return nil
}

func ReplaceAll(dir string, paths map[string]string, old map[string][]byte) error {
	staged := make(map[string]string, len(BinaryNames))
	cleanup := func() {
		for _, path := range staged {
			_ = os.Remove(path)
		}
	}
	for _, name := range BinaryNames {
		src := filepath.Join(dir, name)
		tmp, err := stage(src, paths[name])
		if err != nil {
			cleanup()
			return err
		}
		if got, err := HashFile(tmp); err != nil || got != mustHash(src) {
			cleanup()
			return fmt.Errorf("staged checksum verification failed for %s", name)
		}
		staged[name] = tmp
	}
	for _, name := range BinaryNames {
		if err := os.Rename(staged[name], paths[name]); err != nil {
			cleanup()
			_ = RestoreAll(paths, old)
			return err
		}
		staged[name] = ""
	}
	return nil
}

func RestoreAll(paths map[string]string, old map[string][]byte) error {
	var first error
	for _, name := range BinaryNames {
		if err := writeAtomic(paths[name], old[name]); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func VerifyInstalled(dir string, paths map[string]string) error {
	for _, name := range BinaryNames {
		want, err := HashFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		got, err := HashFile(paths[name])
		if err != nil || got != want {
			return fmt.Errorf("installed checksum mismatch for %s", name)
		}
	}
	return nil
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func BinaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	return strings.TrimSpace(string(out)), err
}

func BinarySourceRevision(path string) (string, bool, error) {
	out, err := exec.Command(path, "--source-sha").Output()
	if err != nil {
		return "", false, err
	}
	revision := strings.TrimSpace(string(out))
	if !sourceRevisionRE.MatchString(revision) {
		return "", false, fmt.Errorf("invalid embedded source revision")
	}
	return revision, false, nil
}

func stage(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(in)
	closeErr := in.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".gpt-tunnel-activation-")
	if err != nil {
		return "", err
	}
	path := out.Name()
	cleanup := func(primary error) (string, error) {
		_ = out.Close()
		_ = os.Remove(path)
		return "", primary
	}
	if _, err := out.Write(data); err != nil {
		return cleanup(err)
	}
	if err := out.Chmod(0o755); err != nil {
		return cleanup(err)
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func writeAtomic(dst string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".gpt-tunnel-restore-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if err := f.Chmod(0o755); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func mustHash(path string) string {
	hash, _ := HashFile(path)
	return hash
}
