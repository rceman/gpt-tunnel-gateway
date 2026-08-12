package train

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// RequiredCutoverActions is the semantic Action Registry surface that must be
// present before Train v2 becomes the sole writable execution authority.
var RequiredCutoverActions = []string{
	"task/create", "task/update", "task/ready", "task/list", "task/read",
	"train/create", "train/add", "train/read", "train/list", "train/start", "train/integrate", "train/cutover",
}

type CutoverEvidence struct {
	CurrentExecutionModel       string
	MaterializationAcknowledged bool
	PlanRetirementAcknowledged  bool
	ActiveLegacyRuns            int
	ActiveTrains                int
	HistoricalCompatibilityOK   bool
	IntegrationClean            bool
	SourceHead                  string
	MirrorHead                  string
	RuntimeReady                bool
	RuntimeVersionMatch         bool
	RegisteredActions           []string
}

func ValidateCutover(e CutoverEvidence) error {
	if e.CurrentExecutionModel != "legacy" {
		return fmt.Errorf("train v2 cutover requires legacy execution authority")
	}
	if !e.MaterializationAcknowledged || !e.PlanRetirementAcknowledged {
		return fmt.Errorf("train v2 cutover requires explicit plan materialization and retirement acknowledgements")
	}
	if e.ActiveLegacyRuns != 0 || e.ActiveTrains != 0 {
		return fmt.Errorf("train v2 cutover requires no active legacy run or train")
	}
	if !e.HistoricalCompatibilityOK {
		return fmt.Errorf("historical compatibility health is not proven")
	}
	if !e.IntegrationClean || e.SourceHead == "" || e.SourceHead != e.MirrorHead || model.ValidateCommitSHA(e.SourceHead) != nil {
		return fmt.Errorf("integration state is not clean and mirror-synchronized")
	}
	if !e.RuntimeReady || !e.RuntimeVersionMatch {
		return fmt.Errorf("target runtime is not ready and version-matched")
	}
	return ValidateActionRegistry(RequiredCutoverActions, e.RegisteredActions)
}

func ValidateActionRegistry(required, registered []string) error {
	seen := make(map[string]bool, len(registered))
	for _, action := range registered {
		if strings.TrimSpace(action) == "" || seen[action] {
			return fmt.Errorf("invalid or duplicate registered action %q", action)
		}
		seen[action] = true
	}
	missing := make([]string, 0)
	for _, action := range required {
		if !seen[action] {
			missing = append(missing, action)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("action registry is missing required actions: %s", strings.Join(missing, ", "))
	}
	return nil
}

func CutoverConfiguration(current model.ProjectConfiguration, updatedBy string, now time.Time) (model.ProjectConfiguration, error) {
	if err := model.ValidateProjectConfiguration(current); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if current.ExecutionModel != "legacy" {
		return model.ProjectConfiguration{}, fmt.Errorf("project is already cut over or has unsupported execution authority")
	}
	if updatedBy == "" || strings.ContainsAny(updatedBy, "\x00\r\n") {
		return model.ProjectConfiguration{}, fmt.Errorf("updated_by is required")
	}
	current.ExecutionModel = "train_v2"
	current.Revision++
	current.UpdatedBy = updatedBy
	current.UpdatedAt = now.UTC()
	if err := model.ValidateProjectConfiguration(current); err != nil {
		return model.ProjectConfiguration{}, err
	}
	return current, nil
}

func NewCutoverReceipt(projectID string, configuration model.ProjectConfiguration, sourceHead, runtimeHead string, materializationAcknowledged, planRetirementAcknowledged bool, updatedBy string, now time.Time) (model.TrainV2CutoverReceipt, error) {
	receipt := model.TrainV2CutoverReceipt{
		SchemaVersion:               model.TrainV2CutoverSchemaVersion,
		ProjectID:                   projectID,
		ExecutionModel:              "train_v2",
		ConfigurationRevision:       configuration.Revision,
		SourceHead:                  sourceHead,
		RuntimeHead:                 runtimeHead,
		ActionSchemaRevision:        1,
		HistoricalCompatibility:     "preserved",
		MaterializationAcknowledged: materializationAcknowledged,
		PlanRetirementAcknowledged:  planRetirementAcknowledged,
		NextAction:                  "use train_v2 task authoring and Train lifecycle",
		UpdatedBy:                   updatedBy,
		UpdatedAt:                   now.UTC(),
	}
	if err := model.ValidateTrainV2CutoverReceipt(receipt); err != nil {
		return model.TrainV2CutoverReceipt{}, err
	}
	return receipt, nil
}
