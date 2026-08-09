package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func mergeCanonicalOutputSamples(groups ...map[string]any) map[string]any {
	samples := map[string]any{}
	for _, group := range groups {
		for name, sample := range group {
			if _, exists := samples[name]; exists {
				panic("duplicate canonical output sample: " + name)
			}
			samples[name] = sample
		}
	}
	return samples
}

func TestCanonicalSuccessfulOutputsMatchEveryDeclaredSchema(t *testing.T) {
	f := newCanonicalOutputFixture()
	samples := mergeCanonicalOutputSamples(f.canonicalCoreOutputSamples(), f.canonicalHandoffOutputSamples(), f.canonicalPlanningOutputSamples(), f.canonicalRuntimeOutputSamples())
	server := &Server{Service: service.New(config.Config{})}
	for name, tool := range server.tools() {
		sample, ok := samples[name]
		if !ok {
			t.Errorf("missing canonical sample for %s", name)
			continue
		}
		if err := validateOutputValue(tool.OutputSchema, normalizeObject(sample)); err != nil {
			t.Errorf("%s canonical output rejected: %v", name, err)
		}
	}
	if len(samples) != len(server.tools()) {
		t.Fatalf("sample coverage mismatch: samples=%d tools=%d", len(samples), len(server.tools()))
	}
}
