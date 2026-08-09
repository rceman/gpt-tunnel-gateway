package upgrade

import (
	"context"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

var (
	sourceRootFn               = sourceRoot
	validateSourceFn           = validateSource
	validateInstalledRuntimeFn = validateInstalledRuntime
	validateTunnelEnvFn        = controller.ValidateTunnelEnv
	buildReleaseFn             = buildRelease
	validateReleaseFn          = validateRelease
	preflightFn                = func(ctx context.Context, c config.Config, path string) (InspectResult, error) {
		return Inspect(ctx, c, path)
	}
	newUpgradeControllerFn = func(c config.Config, path string) upgradeController {
		return liveUpgradeController{controller.Controller{Config: c, ConfigPath: path}}
	}
	smokeFn                     = smoke
	persistStartupDiagnosticsFn = writeTransaction
)

var (
	stageCopy           = stageOne
	stageRename         = os.Rename
	stageRemove         = os.Remove
	stageSyncDir        = syncDir
	removeUpgradeBackup = os.RemoveAll
)
