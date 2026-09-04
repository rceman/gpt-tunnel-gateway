package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
)

func TestHotfixListAndReadUseBoundedIdentityAuthority(t *testing.T) {
	s, _, base := testServiceWithoutIdentifiers(t)
	if err := s.Git.EnsureMirror(context.Background(), s.Config.Projects["example"]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, slug := range []string{"older", "newer"} {
		if err := s.Git.RecordHotfixIdentity(s.Config.StateDir, gitx.HotfixIdentity{
			ProjectID: "example", HotfixRef: "refs/heads/hotfix/" + slug,
			TaskID: fmt.Sprintf("EXM-TSK%d", i+1), BaseSHA: base,
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.HotfixList(context.Background(), HotfixListInput{ProjectID: "example", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.MainHead != base[:8] || len(first.Hotfixes) != 1 || first.Hotfixes[0].Hotfix != "hotfix/newer" || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page=%#v", first)
	}
	second, err := s.HotfixList(context.Background(), HotfixListInput{ProjectID: "example", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Hotfixes) != 1 || second.Hotfixes[0].Hotfix != "hotfix/older" || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page=%#v", second)
	}
	read, err := s.HotfixRead(context.Background(), HotfixReadInput{ProjectID: "example", Hotfix: "hotfix/newer"})
	if err != nil {
		t.Fatal(err)
	}
	if read.ProjectID != "example" || read.HotfixRef != "refs/heads/hotfix/newer" || read.TaskID != "EXM-TSK2" || read.BaseSHA != base || read.Materialized || read.HeadSHA != "" {
		t.Fatalf("read=%#v", read)
	}
}
