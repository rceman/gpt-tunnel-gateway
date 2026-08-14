package service

import (
	"reflect"
	"testing"
)

func TestAgentTailDeltaReturnsOnlyCurrentViewportSuffix(t *testing.T) {
	selected, hasNew, historyTruncated := agentTailDelta([]string{"old-2", "old-3"}, []string{"old-2", "old-3", "new-4"}, true)
	if !reflect.DeepEqual(selected, []string{"new-4"}) || !hasNew || historyTruncated {
		t.Fatalf("incremental tail delta=%#v new=%v truncated=%v", selected, hasNew, historyTruncated)
	}
	selected, hasNew, historyTruncated = agentTailDelta([]string{"old-2", "old-3"}, []string{"old-2", "old-3"}, true)
	if len(selected) != 0 || hasNew || historyTruncated {
		t.Fatalf("unchanged tail delta=%#v new=%v truncated=%v", selected, hasNew, historyTruncated)
	}
}

func TestAgentTailObservationKeyScopesSessionProjectAndTarget(t *testing.T) {
	s := &Service{}
	firstPath, firstLock := s.agentTailStateLocation("SP-ONE", "project-one", "project-one_master")
	samePath, sameLock := s.agentTailStateLocation("SP-ONE", "project-one", "project-one_master")
	if firstPath != samePath || firstLock != sameLock {
		t.Fatalf("identical observation identity was not stable: %q/%q vs %q/%q", firstPath, firstLock, samePath, sameLock)
	}
	variants := [][3]string{
		{"SP-TWO", "project-one", "project-one_master"},
		{"SP-ONE", "project-two", "project-one_master"},
		{"SP-ONE", "project-one", "project-two_master"},
	}
	for _, variant := range variants {
		path, lock := s.agentTailStateLocation(variant[0], variant[1], variant[2])
		if path == firstPath || lock == firstLock {
			t.Fatalf("observation identity was not isolated for %v: %q/%q", variant, path, lock)
		}
	}
}
