package model

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ProjectRetirementSchemaVersion = 1

type ProjectRetirement struct {
	SchemaVersion               int       `json:"schema_version"`
	ProjectID                   string    `json:"project_id"`
	Revision                    int       `json:"revision"`
	Reason                      string    `json:"reason"`
	Actor                       string    `json:"actor"`
	RetiredAt                   time.Time `json:"retired_at"`
	ConfigurationRevision       int       `json:"configuration_revision"`
	CancelledConfigPublications int       `json:"cancelled_config_publications"`
	CancelledConfigOutboxSHA256 string    `json:"cancelled_config_outbox_sha256"`
	LegacyCallbackEpochCount    int       `json:"legacy_callback_epoch_count,omitempty"`
	LegacyCallbackEpochSHA256   string    `json:"legacy_callback_epoch_sha256,omitempty"`
}

func ValidateProjectRetirement(v ProjectRetirement) error {
	if v.SchemaVersion != ProjectRetirementSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Revision != 1 {
		return fmt.Errorf("invalid project retirement identity")
	}
	if len(v.Reason) == 0 || len(v.Reason) > 512 || !utf8.ValidString(v.Reason) || strings.TrimSpace(v.Reason) != v.Reason {
		return fmt.Errorf("invalid project retirement reason")
	}
	for _, r := range v.Reason {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid project retirement reason")
		}
	}
	if v.Actor != "gatewayd" || v.RetiredAt.IsZero() || v.ConfigurationRevision < 0 || v.CancelledConfigPublications < 0 || v.CancelledConfigPublications > 4096 || ValidateSHA256(v.CancelledConfigOutboxSHA256) != nil || v.LegacyCallbackEpochCount < 0 || v.LegacyCallbackEpochCount > 4096 || v.LegacyCallbackEpochSHA256 != "" && ValidateSHA256(v.LegacyCallbackEpochSHA256) != nil || v.LegacyCallbackEpochCount > 0 && v.LegacyCallbackEpochSHA256 == "" {
		return fmt.Errorf("invalid project retirement evidence")
	}
	return nil
}
