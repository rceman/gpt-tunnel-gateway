package onboarding

import "regexp"

const MaxSafeInteger uint64 = 9007199254740991

// PositiveInteger is a JSON Schema integer constrained to the positive,
// JavaScript-safe range used by the onboarding contract.
type PositiveInteger uint64

func (n *PositiveInteger) UnmarshalJSON(data []byte) error {
	value, err := parsePositiveInteger(data)
	if err != nil {
		return err
	}
	*n = PositiveInteger(value)
	return nil
}

type Request struct {
	SchemaVersion       PositiveInteger `json:"schema_version"`
	ProjectID           string          `json:"project_id"`
	Root                string          `json:"root"`
	Remote              string          `json:"remote"`
	RepositoryURL       string          `json:"repository_url"`
	DefaultBranch       string          `json:"default_branch"`
	Airelay             Airelay         `json:"airelay"`
	ProjectCode         string          `json:"project_code"`
	GatewayStateDir     string          `json:"gateway_state_dir"`
	Workflow            *Workflow       `json:"workflow,omitempty"`
	InitialPlan         InitialPlan     `json:"initial_plan"`
	ExpectedHubRevision string          `json:"expected_hub_revision"`
}

type Airelay struct {
	SessionRequired bool    `json:"session_required"`
	SessionKey      *string `json:"session_key,omitempty"`
}

type Workflow struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type InitialPlan struct {
	SchemaVersion    PositiveInteger      `json:"schema_version"`
	ProjectID        string               `json:"project_id"`
	Revision         PositiveInteger      `json:"revision"`
	Title            string               `json:"title"`
	Summary          string               `json:"summary"`
	CurrentObjective string               `json:"current_objective"`
	Queue            []string             `json:"queue"`
	Sections         []InitialPlanSection `json:"sections"`
	UpdatedBy        string               `json:"updated_by"`
	UpdatedAt        string               `json:"updated_at"`
}

type InitialPlanSection struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	ShortDescription string          `json:"short_description"`
	Revision         PositiveInteger `json:"revision"`
}

var (
	projectIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	objectIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	projectCodePattern  = regexp.MustCompile(`^[A-Z]{3}$`)
	remotePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	sessionKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	workflowRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	sha40Pattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)
