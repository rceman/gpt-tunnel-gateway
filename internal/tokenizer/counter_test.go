package tokenizer

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCounterExactAndDeterministic(t *testing.T) {
	counter := NewCounter()
	inputs := []string{"", "hello world", "こんにちは世界", "package main\n\nfunc main() {}\n"}
	for _, input := range inputs {
		first, err := counter.CountText([]byte(input))
		if err != nil {
			t.Fatalf("count %q: %v", input, err)
		}
		second, err := counter.CountText([]byte(input))
		if err != nil || first != second {
			t.Fatalf("repeat count %q: first=%d second=%d err=%v", input, first, second, err)
		}
	}
	if got, err := counter.CountText([]byte("hello world")); err != nil || got != 2 {
		t.Fatalf("ASCII count=%d err=%v, want 2", got, err)
	}
}

func TestCounterIsOffline(t *testing.T) {
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("network disabled")
	})
	defer func() { http.DefaultTransport = previous }()

	if _, err := NewCounter().CountText([]byte("offline")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("tokenizer made %d network calls", calls)
	}
}

func TestReferenceParityFixtures(t *testing.T) {
	counter := NewCounter()
	fixtures := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "ascii", text: "hello world", want: 2},
		{name: "unicode", text: "こんにちは世界", want: 2},
		{name: "multiline_go", text: "package main\n\nfunc main() {}\n", want: 7},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := counter.CountText([]byte(fixture.text))
			if err != nil || got != fixture.want {
				t.Fatalf("count=%d err=%v want=%d", got, err, fixture.want)
			}
		})
	}
}

func TestCurrentRepositoryNativeInventory(t *testing.T) {
	if os.Getenv("GPT_TUNNEL_NATIVE_INVENTORY") != "1" {
		t.Skip("explicit inventory evidence test")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	report, err := CountRepository(context.Background(), root, NewCounter())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("COUNT=%d MAX=%d MAX_PATH=%s", report.CountAboveMax, report.Max.Tokens, report.Max.Path)
	if len(report.Offending) != 0 {
		t.Fatalf("offenders=%#v", report.Offending)
	}
}

func TestCounterRejectsInvalidText(t *testing.T) {
	counter := NewCounter()
	for _, input := range [][]byte{{0xff, 0xfe}, {'a', 0, 'b'}} {
		if _, err := counter.CountText(input); err == nil {
			t.Fatalf("invalid input accepted: %v", input)
		}
	}
}

func TestRepositoryEnumerationIsNativeAndDeterministic(t *testing.T) {
	root := t.TempDir()
	if err := runGit(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "small.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "add", "small.go", "ignored.bin"); err != nil {
		t.Fatal(err)
	}
	first, err := CountRepository(context.Background(), root, NewCounter())
	if err != nil {
		t.Fatal(err)
	}
	second, err := CountRepository(context.Background(), root, NewCounter())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 || first.Files[0].Path != "small.go" {
		t.Fatalf("files=%#v", first.Files)
	}
	if strings.TrimSpace(first.Files[0].Path) != strings.TrimSpace(second.Files[0].Path) || first.Files[0].Tokens != second.Files[0].Tokens {
		t.Fatalf("nondeterministic reports: %#v %#v", first, second)
	}
}

func TestRepositoryRejectsApplicableNUL(t *testing.T) {
	root := t.TempDir()
	if err := runGit(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bad.go")
	if err := os.WriteFile(path, []byte("package main\x00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "add", "bad.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := CountRepository(context.Background(), root, NewCounter()); err == nil {
		t.Fatal("NUL-containing source passed admission")
	}
}

func TestRepositoryThresholdAdmission(t *testing.T) {
	root := t.TempDir()
	if err := runGit(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "small.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	counter := NewCounter()
	large := ""
	for i := 0; i < 10000; i++ {
		large += "x "
		count, err := counter.CountText([]byte(large))
		if err != nil {
			t.Fatal(err)
		}
		if count > MaxTokens {
			break
		}
	}
	count, err := counter.CountText([]byte(large))
	if err != nil || count <= MaxTokens {
		t.Fatalf("failed to construct exact over-limit fixture: count=%d err=%v", count, err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.go"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "add", "small.go", "large.go"); err != nil {
		t.Fatal(err)
	}
	report, err := CountRepository(context.Background(), root, counter)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Offending) != 1 || report.Offending[0].Path != "large.go" || report.Offending[0].Tokens <= MaxTokens {
		t.Fatalf("offenders=%#v", report.Offending)
	}
	if report.Max.Path != "large.go" || report.Max.Tokens != count {
		t.Fatalf("max=%#v count=%d", report.Max, count)
	}
}

func TestNativeRuntimeDoesNotUseRepoDexOrRepoSuite(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"internal/tokenizer/counter.go", "internal/gates/gates.go"} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, `"repodex"`) || strings.Contains(text, `"reposuite"`) {
			t.Fatalf("runtime token path mentions external oracle in %s", relative)
		}
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(gitPath))
	fixture := t.TempDir()
	if err := runGit(fixture, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "small.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(fixture, "add", "small.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := CountRepository(context.Background(), fixture, NewCounter()); err != nil {
		t.Fatalf("native count depended on unavailable external tools: %v", err)
	}
}

func runGit(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	return cmd.Run()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
