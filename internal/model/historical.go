package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// HistoricalRunV1 is an immutable read model for the protocol-v1 run.json
// shape. Its local result/evidence paths are intentionally never projected to
// Run and cannot be used by finalization.
type HistoricalRunV1 struct {
	SchemaVersion    int        `json:"schema_version"`
	ID               string     `json:"id"`
	TaskID           string     `json:"task_id"`
	TaskSHA256       string     `json:"task_sha256"`
	ProjectID        string     `json:"project_id"`
	GatewayID        string     `json:"gateway_id"`
	SessionKey       string     `json:"session_key"`
	Branch           string     `json:"branch"`
	BaseRevision     string     `json:"base_revision"`
	HubRevision      string     `json:"hub_revision"`
	Status           string     `json:"status"`
	DispatchMessage  string     `json:"dispatch_message,omitempty"`
	DispatchExitCode *int       `json:"dispatch_exit_code,omitempty"`
	DispatchStdout   string     `json:"dispatch_stdout,omitempty"`
	DispatchStderr   string     `json:"dispatch_stderr,omitempty"`
	ResultPath       string     `json:"result_path"`
	EvidencePath     string     `json:"evidence_path"`
	CreatedAt        time.Time  `json:"created_at"`
	DispatchedAt     *time.Time `json:"dispatched_at,omitempty"`
	RepromptCount    int        `json:"reprompt_count,omitempty"`
	LastRepromptAt   *time.Time `json:"last_reprompt_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

func (v HistoricalRunV1) PublicRun() Run {
	return Run{
		SchemaVersion:    v.SchemaVersion,
		ID:               v.ID,
		TaskID:           v.TaskID,
		TaskSHA256:       v.TaskSHA256,
		ProjectID:        v.ProjectID,
		GatewayID:        v.GatewayID,
		SessionKey:       v.SessionKey,
		Branch:           v.Branch,
		BaseRevision:     v.BaseRevision,
		HubRevision:      v.HubRevision,
		Status:           v.Status,
		DispatchMessage:  v.DispatchMessage,
		DispatchExitCode: v.DispatchExitCode,
		DispatchStdout:   v.DispatchStdout,
		DispatchStderr:   v.DispatchStderr,
		Historical:       true,
		CreatedAt:        v.CreatedAt,
		DispatchedAt:     v.DispatchedAt,
		RepromptCount:    v.RepromptCount,
		LastRepromptAt:   v.LastRepromptAt,
		FinishedAt:       v.FinishedAt,
	}
}

func decodeTypedStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("historical run has trailing JSON")
	}
	return nil
}

// DecodeRunRecord accepts exactly the current run shape or the immutable v1
// shape and returns whether the record is historical. It is deliberately not a
// completion decoder and does not synthesize a completion path.
func DecodeRunRecord(data []byte) (Run, bool, error) {
	obj, err := strictJSONObject(data)
	if err != nil {
		return Run{}, false, err
	}
	_, oldResult := obj["result_path"]
	_, oldEvidence := obj["evidence_path"]
	_, current := obj["completion_path"]
	if oldResult || oldEvidence {
		if !oldResult || !oldEvidence || current {
			return Run{}, false, fmt.Errorf("invalid historical run shape")
		}
		var v HistoricalRunV1
		if err := decodeTypedStrict(data, &v); err != nil {
			return Run{}, false, err
		}
		return v.PublicRun(), true, nil
	}
	if !current {
		return Run{}, false, fmt.Errorf("run record has no recognized protocol shape")
	}
	var v Run
	if err := decodeTypedStrict(data, &v); err != nil {
		return Run{}, false, err
	}
	return v, false, nil
}
