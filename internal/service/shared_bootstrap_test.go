package service

import (
	"testing"
)

func TestSharedADRBootstrapAcceptsLegacyIDWithoutAdvancingSequence(t *testing.T) {
	number, counted, err := sharedADRBootstrapSequenceNumber("ADR-0001", "GTW")
	if err != nil {
		t.Fatalf("legacy ADR rejected: %v", err)
	}
	if counted || number != 0 {
		t.Fatalf("legacy ADR sequence result=(%d,%t), want (0,false)", number, counted)
	}
}

func TestSharedADRBootstrapCountsOnlyRecognizedProjectIDs(t *testing.T) {
	tests := []struct {
		id    string
		want  uint64
		count bool
	}{
		{id: "GTW-ADR9", want: 9, count: true},
		{id: "GTW-A7", want: 7, count: true},
	}
	for _, test := range tests {
		number, counted, err := sharedADRBootstrapSequenceNumber(test.id, "GTW")
		if err != nil || number != test.want || counted != test.count {
			t.Fatalf("%s result=(%d,%t,%v), want (%d,%t,nil)", test.id, number, counted, err, test.want, test.count)
		}
	}
}
