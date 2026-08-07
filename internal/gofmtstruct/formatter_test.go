package gofmtstruct

import (
	"strings"
	"testing"
)

func formatted(t *testing.T, source string) string {
	t.Helper()
	result, err := FormatSource("fixture.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}

func TestFormatSourceUsesVerticalKeyedStructLiterals(t *testing.T) {
	got := formatted(t, `package fixture

type Named struct { A int; B int }

var _ = struct { A int; B int }{A: 1, B: 2}
var _ = Named{A: 1, B: 2}
`)
	if strings.Count(got, "A: 1,\n") != 2 || strings.Count(got, "B: 2,\n") != 2 {
		t.Fatalf("keyed literals were not vertical:\n%s", got)
	}
}

func TestFormatSourceHandlesNestedAndCommentedLiterals(t *testing.T) {
	got := formatted(t, `package fixture

type Outer struct { Inner struct { A int; B int }; C int }

var _ = Outer{Inner: struct { A int; B int }{A: 1, B: 2}, /* keep */ C: 3}
`)
	for _, want := range []string{"Inner:", "A: 1,", "B: 2,", "/* keep */", "C: 3,"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output lost %q:\n%s", want, got)
		}
	}
}

func TestFormatSourceLeavesSingleFieldUnkeyedAndMapsCompact(t *testing.T) {
	got := formatted(t, `package fixture

type Named struct { A int; B int }
type Alias map[string]int

var _ = Named{A: 1}
var _ = Named{1, 2}
var _ = map[string]int{"a": 1, "b": 2}
var _ = Alias{"a": 1, "b": 2}
`)
	if strings.Contains(got, "Named{\n") {
		t.Fatalf("single-field or unkeyed literal was expanded:\n%s", got)
	}
	if strings.Contains(got, "Alias{\n") || strings.Contains(got, "map[string]int{\n") {
		t.Fatalf("map literal was expanded:\n%s", got)
	}
}

func TestFormatSourceIsIdempotent(t *testing.T) {
	source := `package fixture

type Named struct { A int; B int }

var _ = Named{
	A: 1,
	B: 2,
}
`
	first := formatted(t, source)
	second := formatted(t, first)
	if first != second {
		t.Fatalf("formatter is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
