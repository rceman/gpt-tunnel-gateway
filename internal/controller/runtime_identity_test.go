package controller

import (
	"context"
	"errors"
	"testing"
)

func TestCollectRuntimeIdentityMatchesRunningAndInstalledArtifacts(t *testing.T) {
	restore := stubRuntimeArtifactProbes()
	defer restore()
	runtimeHashFile = func(path string) (string, error) {
		if path == "/tmp/running-gateway" {
			return "shared-hash-/tmp/gateway", nil
		}
		return "shared-hash-" + path, nil
	}
	runtimeBinaryVersion = func(context.Context, string) (string, error) { return "0.6.11", nil }
	runtimeSourceRevision = func(context.Context, string) (string, bool, error) { return "source-sha", false, nil }

	var identity RuntimeIdentity
	identity.collectArtifacts(context.Background(), ProcessStatus{
		Running:    true,
		Executable: "/tmp/running-gateway",
	}, "/tmp/gateway")

	if !identity.ArtifactSetCoherent || !identity.RunningGatewayMatchesInstall {
		t.Fatalf("coherent artifact identity=%+v", identity)
	}
	if !identity.SourceProvenanceAvailable || !identity.ExactSourceMatch || identity.SourceSHA != "source-sha" {
		t.Fatalf("source identity=%+v", identity)
	}
}

func TestCollectRuntimeIdentityRejectsStaleOrIncompleteArtifacts(t *testing.T) {
	restore := stubRuntimeArtifactProbes()
	defer restore()
	runtimeHashFile = func(path string) (string, error) {
		if path == "/tmp/running-gateway" {
			return "running-hash", nil
		}
		if path == "/tmp/gpt-tunnel-gatewayd" {
			return "installed-hash", nil
		}
		if path == "/tmp/gpt-tunnelctl" {
			return "", errors.New("artifact missing")
		}
		return "other-hash", nil
	}
	runtimeBinaryVersion = func(_ context.Context, path string) (string, error) {
		if path == "/tmp/gpt-tunnelctl" {
			return "", errors.New("artifact missing")
		}
		return "0.6.11", nil
	}
	runtimeSourceRevision = func(context.Context, string) (string, bool, error) {
		return "", false, errors.New("build provenance unavailable")
	}

	var identity RuntimeIdentity
	identity.collectArtifacts(context.Background(), ProcessStatus{
		Running:    true,
		Executable: "/tmp/running-gateway",
	}, "/tmp/gateway")

	if identity.ArtifactSetCoherent || identity.RunningGatewayMatchesInstall {
		t.Fatalf("incomplete/stale artifact identity accepted=%+v", identity)
	}
	if identity.SourceProvenanceAvailable || identity.ExactSourceMatch || identity.ProvenanceReason == "" {
		t.Fatalf("unavailable provenance identity=%+v", identity)
	}
}

func TestAllEqualMapValuesRequiresNonEmptyExactValues(t *testing.T) {
	if allEqualMapValues(nil) || allEqualMapValues(map[string]string{"a": ""}) {
		t.Fatal("empty values were accepted as exact identity")
	}
	if !allEqualMapValues(map[string]string{"a": "x", "b": "x"}) || allEqualMapValues(map[string]string{"a": "x", "b": "y"}) {
		t.Fatal("map equality result was incorrect")
	}
}

func stubRuntimeArtifactProbes() func() {
	oldHash, oldVersion, oldSource := runtimeHashFile, runtimeBinaryVersion, runtimeSourceRevision
	return func() { runtimeHashFile, runtimeBinaryVersion, runtimeSourceRevision = oldHash, oldVersion, oldSource }
}
