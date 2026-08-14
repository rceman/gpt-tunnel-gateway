package query

import "testing"

func TestParseCollectionDSL(t *testing.T) {
	parsed, err := Parse(`task.list(status=[ready,planned],search="gateway").select(id,title,status).order_by(id=desc).limit(10).count()`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Entity != "task" || len(parsed.Filters) != 2 || len(parsed.Select) != 3 || parsed.Order.Field != "id" || !parsed.Order.Desc || parsed.Limit != 10 || !parsed.Count {
		t.Fatalf("parsed=%#v", parsed)
	}
}

func TestParseRejectsExactReadsAndUnsafeExpressions(t *testing.T) {
	for _, input := range []string{`task.read()`, `task.list().select(*)`, `task.list().select(all)`, `task.list().join(adr)`, `task.list(status=ready or title=x)`} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("unsafe query accepted: %s", input)
		}
	}
}
