package onboarding

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnicodeStringLengthMatchesPlanner(t *testing.T) {
	valid := requestWithUnicodeFields(t, strings.Repeat("é", 150), strings.Repeat("é", 250), strings.Repeat("é", 20000), strings.Repeat("é", 128), strings.Repeat("é", 150), strings.Repeat("é", 250))
	if _, err := DecodeRequest(valid); err != nil {
		t.Fatalf("Unicode values within character limits should be valid: %v", err)
	}
	for name, request := range map[string][]byte{
		"title maximum":               requestWithUnicodeFields(t, strings.Repeat("é", 301), "summary", "", "updated", "section", "short"),
		"summary maximum":             requestWithUnicodeFields(t, "title", strings.Repeat("é", 501), "", "updated", "section", "short"),
		"objective maximum":           requestWithUnicodeFields(t, "title", "summary", strings.Repeat("é", 20001), "updated", "section", "short"),
		"updated_by maximum":          requestWithUnicodeFields(t, "title", "summary", "", strings.Repeat("é", 256), "section", "short"),
		"section title maximum":       requestWithUnicodeFields(t, "title", "summary", "", "updated", strings.Repeat("é", 301), "short"),
		"section description maximum": requestWithUnicodeFields(t, "title", "summary", "", "updated", "section", strings.Repeat("é", 501)),
	} {
		t.Run(name, func(t *testing.T) {
			expectInvalid(t, request)
		})
	}
}

func TestRepositoryURLRejectsUnicodeWhitespace(t *testing.T) {
	fixture := string(fixtureBytes(t))
	bad := mustReplace(t, fixture, `"repository_url": "git@github.com-therceman:therceman/airelay.git"`, `"repository_url": "git@example.com:owner/airelay`+string('\u00a0')+`repo.git"`)
	expectInvalid(t, bad)
}

func TestAbsolutePathRealpathParity(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Request){
		"root":                    func(request *Request) { request.Root = link },
		"gateway state dir":       func(request *Request) { request.GatewayStateDir = link },
		"absolute repository URL": func(request *Request) { request.RepositoryURL = link },
	} {
		t.Run(name, func(t *testing.T) {
			request, err := DecodeRequest(fixtureBytes(t))
			if err != nil {
				t.Fatal(err)
			}
			mutate(&request)
			if err := ValidateRequest(request); err == nil {
				t.Fatal("symlink spelling should be rejected")
			}
		})
	}

	request, err := DecodeRequest(fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	request.Root = filepath.Join(real, "missing", "root")
	request.GatewayStateDir = filepath.Join(real, "missing", "state")
	request.RepositoryURL = filepath.Join(real, "missing", "repository.git")
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("normalized nonexistent suffix should be accepted: %v", err)
	}
}

func TestDateTimeAcceptsLowercaseT(t *testing.T) {
	fixture := mustReplace(t, string(fixtureBytes(t)), `"updated_at": "2026-08-05T09:00:00Z"`, `"updated_at": "2026-08-05t09:00:00Z"`)
	if _, err := DecodeRequest(fixture); err != nil {
		t.Fatalf("lowercase t should be accepted: %v", err)
	}
}

func TestDecodeRequestRejectsInvalidUTF8(t *testing.T) {
	valid := fixtureBytes(t)
	invalid := bytes.Replace(valid, []byte("airelay"), []byte{'a', 0xff, 'r', 'e', 'l', 'a', 'y'}, 1)
	if _, err := DecodeRequest(invalid); err == nil {
		t.Fatal("invalid UTF-8 should be rejected")
	}
}

func TestValidateRequestRejectsGoModelViolations(t *testing.T) {
	request, err := DecodeRequest(fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	request.InitialPlan.Queue = nil
	if err := ValidateRequest(request); err == nil {
		t.Fatal("nil queue should be rejected")
	}
	request, err = DecodeRequest(fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	request.InitialPlan.Sections = nil
	if err := ValidateRequest(request); err == nil {
		t.Fatal("nil sections should be rejected")
	}
}
