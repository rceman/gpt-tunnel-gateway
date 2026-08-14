package service

import "testing"

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
