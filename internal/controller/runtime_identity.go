package controller

import (
	"context"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

var (
	runtimeHashFile       = releaseartifacts.HashFile
	runtimeBinaryVersion  = releaseartifacts.BinaryVersion
	runtimeSourceRevision = releaseartifacts.BinarySourceRevision
)

func (c Controller) RuntimeIdentity(ctx context.Context) RuntimeIdentity {
	gatewayExpected, _ := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	tunnelExpected, _ := filepath.EvalSymlinks(c.Config.Controller.TunnelClientBinary)
	gateway := c.process("gateway", gatewayExpected)
	tunnel := c.process("tunnel", tunnelExpected)
	identity := RuntimeIdentity{
		GatewayPID:                gateway.PID,
		TunnelPID:                 tunnel.PID,
		RunningExecutablePath:     gateway.Executable,
		GatewayReady:              checkURL(ctx, c.gatewayReadyURL()),
		TunnelReady:               checkURL(ctx, c.tunnelReadyURL()),
		InstalledVersion:          installedVersion(c.Config.Controller.GatewayBinary),
		InstalledArtifactVersions: map[string]string{},
	}
	if identity.GatewayReady {
		identity.RunningVersion = runningVersion(ctx, c.gatewayReadyURL(), c.Config.GatewayID)
	}
	identity.VersionMatch = identity.InstalledVersion != "" && identity.RunningVersion != "" && identity.InstalledVersion == identity.RunningVersion
	identity.collectArtifacts(gateway, c.Config.Controller.GatewayBinary)
	return identity
}

func (i *RuntimeIdentity) collectArtifacts(gateway ProcessStatus, gatewayBinary string) {
	paths := releaseartifacts.Paths(gatewayBinary)
	if gateway.Executable != "" {
		if hash, err := runtimeHashFile(gateway.Executable); err == nil {
			i.RunningExecutableSHA256 = hash
		}
	}
	hashes := map[string]string{}
	versions := map[string]string{}
	sources := map[string]string{}
	modified := false
	for _, name := range releaseartifacts.BinaryNames {
		path := paths[name]
		hash, hashErr := runtimeHashFile(path)
		version, versionErr := runtimeBinaryVersion(path)
		if hashErr == nil {
			hashes[name] = hash
		}
		if versionErr == nil && version != "" {
			versions[name] = version
		}
		source, dirty, sourceErr := runtimeSourceRevision(path)
		if sourceErr == nil && source != "" {
			sources[name] = source
			modified = modified || dirty
		}
	}
	i.InstalledGatewaySHA256 = hashes["gpt-tunnel-gatewayd"]
	i.InstalledCLISHA256 = hashes["gpt-tunnel"]
	i.InstalledCTLSHA256 = hashes["gpt-tunnelctl"]
	i.InstalledArtifactVersions = versions
	i.ArtifactSetCoherent = len(hashes) == len(releaseartifacts.BinaryNames) && len(versions) == len(releaseartifacts.BinaryNames) && allEqualMapValues(versions)
	i.RunningGatewayMatchesInstall = gateway.Running && i.RunningExecutableSHA256 != "" && i.RunningExecutableSHA256 == i.InstalledGatewaySHA256
	if len(sources) == len(releaseartifacts.BinaryNames) && allEqualMapValues(sources) && !modified {
		i.SourceSHA = sources["gpt-tunnel-gatewayd"]
		i.SourceProvenanceAvailable = i.SourceSHA != ""
		i.ExactSourceMatch = i.SourceProvenanceAvailable && i.ArtifactSetCoherent
	} else {
		i.ProvenanceReason = "artifact build provenance unavailable or inconsistent"
	}
	if i.RunningExecutableSHA256 == "" && gateway.Running {
		i.ProvenanceReason = "running executable hash unavailable"
	}
}

func allEqualMapValues(values map[string]string) bool {
	var first string
	for _, value := range values {
		if first == "" {
			first = value
			continue
		}
		if value != first {
			return false
		}
	}
	return first != ""
}
