package onboarding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

const fixtureSHA256 = "338608e7e9cade79513d55e165ef8a506706b72904a382e44e134903d7b7240f"

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/airelay-request.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustReplace(t *testing.T, input, old, replacement string) []byte {
	t.Helper()
	if !strings.Contains(input, old) {
		t.Fatalf("fixture did not contain %q", old)
	}
	return []byte(strings.Replace(input, old, replacement, 1))
}

func expectInvalid(t *testing.T, data []byte) {
	t.Helper()
	if _, err := DecodeRequest(data); err == nil {
		t.Fatalf("expected invalid request, got success")
	}
}

func TestCanonicalFixtureDecodeValidateAndDigest(t *testing.T) {
	raw := fixtureBytes(t)
	rawBefore := append([]byte(nil), raw...)
	digest := sha256.Sum256(raw)
	if got := hex.EncodeToString(digest[:]); got != fixtureSHA256 {
		t.Fatalf("fixture SHA-256 = %s, want %s", got, fixtureSHA256)
	}

	request, err := DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
	if !bytes.Equal(raw, rawBefore) {
		t.Fatal("DecodeRequest mutated input")
	}

	canonical, err := CanonicalRequestJSON(request)
	if err != nil {
		t.Fatalf("CanonicalRequestJSON: %v", err)
	}
	if len(canonical) == 0 || bytes.ContainsAny(canonical, "\r\n") {
		t.Fatal("canonical JSON must be compact and newline-free")
	}
	if !json.Valid(canonical) {
		t.Fatal("canonical JSON is invalid")
	}
	decodedCanonical, err := DecodeRequest(canonical)
	if err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	if !reflect.DeepEqual(decodedCanonical, request) {
		t.Fatal("canonical decode differs from original request")
	}

	digestOne, err := RequestDigest(request)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	digestTwo, err := request.Digest()
	if err != nil {
		digestTwo = ""
	}
	if digestOne == "" || digestOne != digestTwo {
		t.Fatalf("digest is not stable: %q vs %q", digestOne, digestTwo)
	}
	wantDigest := sha256.Sum256(canonical)
	if digestOne != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("digest = %s, want hash of canonical JSON", digestOne)
	}
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requestWithUnicodeFields(t *testing.T, title, summary, objective, updatedBy, sectionTitle, sectionDescription string) []byte {
	t.Helper()
	fixture := string(fixtureBytes(t))
	fixture = string(mustReplace(t, fixture, `"title": "Airelay onboarding"`, `"title": `+jsonString(t, title)))
	fixture = string(mustReplace(t, fixture, `"summary": "Initial idle plan for the registered Airelay project."`, `"summary": `+jsonString(t, summary)))
	fixture = string(mustReplace(t, fixture, `"current_objective": ""`, `"current_objective": `+jsonString(t, objective)))
	fixture = string(mustReplace(t, fixture, `"updated_by": "planner-onboarding"`, `"updated_by": `+jsonString(t, updatedBy)))
	section := fmt.Sprintf(`"sections": [{"id":"S1","title":%s,"short_description":%s,"revision":1}],`, jsonString(t, sectionTitle), jsonString(t, sectionDescription))
	return mustReplace(t, fixture, `"sections": [],`, section)
}
