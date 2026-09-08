package sqlitestore

import "fmt"

const (
	legacyLocalInterSessionMessagesName   = "gpt_tunnel_local_inter_session_messages_v1"
	legacyLocalHistoryIndexesName         = "gpt_tunnel_local_history_indexes_v1"
	legacyLocalHistoryProjectIndexesName  = "gpt_tunnel_local_history_project_indexes_v1"
	legacyLocalHistoryProjectBackfillName = "gpt_tunnel_local_history_project_backfill_v1"
)

func validateSharedMigrationHistory(markers map[int64]string) error {
	allowed := map[int64]map[string]bool{
		1:                     {"gpt_tunnel_shared_authority_v1": true},
		2:                     {sharedReplicationMigrationName: true},
		3:                     {sharedCutoverMigrationName: true},
		4:                     {"gpt_tunnel_shared_project_identifiers_v1": true},
		5:                     {"gpt_tunnel_shared_train_admission_v1": true},
		6:                     {"gpt_tunnel_shared_train_admission_update_guard_v1": true},
		7:                     {sharedTaskSequenceMigrationName: true},
		8:                     {sharedIntegrationCurrentMigrationName: true},
		9:                     {sharedBootstrapMigrationName: true},
		10:                    {sharedADROutboxMigrationName: true},
		11:                    {sharedProjectConfigurationMigrationName: true},
		12:                    {"gpt_tunnel_shared_agents_v12": true, sharedLifecycleMigrationName: true},
		13:                    {"gpt_tunnel_shared_watcher_guides_v13": true},
		14:                    {"gpt_tunnel_shared_journal_sequences_v14": true},
		15:                    {"gpt_tunnel_shared_integration_operations_v15": true},
		16:                    {"gpt_tunnel_shared_outbox_project_v16": true},
		sharedBaselineVersion: {sharedBaselineName: true},
		sharedBridgeVersion:   {sharedBridgeName: true},
	}
	if err := validateKnownMarkers(markers, allowed); err != nil {
		return err
	}
	if markers[12] == sharedLifecycleMigrationName {
		for version := int64(13); version <= 16; version++ {
			if _, ok := markers[version]; ok {
				return fmt.Errorf("bad Shared lifecycle v12 cannot be followed by deployed v%d", version)
			}
		}
	}
	for version := int64(13); version <= 16; version++ {
		if _, present := markers[version]; present {
			if _, previous := markers[version-1]; !previous || markers[version-1] == sharedLifecycleMigrationName {
				return fmt.Errorf("deployed Shared history has a gap before version %d", version)
			}
		}
	}
	if _, ok := markers[sharedBaselineVersion]; ok {
		if len(markers) != 1 {
			return fmt.Errorf("Shared baseline marker must be the only applied marker")
		}
	}
	if _, ok := markers[sharedBridgeVersion]; ok {
		if _, baseline := markers[sharedBaselineVersion]; baseline || len(markers) == 1 {
			return fmt.Errorf("Shared bridge marker requires a legacy migration history")
		}
	}
	return validatePrefix(markers, 1, 11)
}

func validateLocalMigrationHistory(markers map[int64]string) error {
	allowed := map[int64]map[string]bool{
		1:                                   {localOperationalMigrationName: true},
		2:                                   {legacyLocalInterSessionMessagesName: true},
		3:                                   {legacyLocalHistoryIndexesName: true},
		4:                                   {legacyLocalHistoryProjectIndexesName: true},
		5:                                   {legacyLocalHistoryProjectBackfillName: true},
		localCallbackEpochsMigrationVersion: {localCallbackEpochsMigrationDescription: true},
		localAgentRegistryMigrationVersion:  {localAgentRegistryMigrationDescription: true},
		localSessionStoreMigrationVersion:   {localSessionStoreMigrationDescription: true},
		localBaselineVersion:                {localBaselineName: true},
		localBridgeVersion:                  {localBridgeName: true},
	}
	if err := validateKnownMarkers(markers, allowed); err != nil {
		return err
	}
	if _, ok := markers[localBaselineVersion]; ok && len(markers) != 1 {
		return fmt.Errorf("Local baseline marker must be the only applied marker")
	}
	if _, ok := markers[localBridgeVersion]; ok {
		if _, baseline := markers[localBaselineVersion]; baseline || len(markers) == 1 {
			return fmt.Errorf("Local bridge marker requires a legacy migration history")
		}
	}
	if _, ok := markers[localAgentRegistryMigrationVersion]; ok {
		if _, previous := markers[localCallbackEpochsMigrationVersion]; !previous {
			return fmt.Errorf("Local agent migration has no callback migration")
		}
	}
	if _, ok := markers[localSessionStoreMigrationVersion]; ok {
		if _, previous := markers[localAgentRegistryMigrationVersion]; !previous {
			return fmt.Errorf("Local session migration has no agent migration")
		}
	}
	return validatePrefix(markers, 1, 5)
}

func validateKnownMarkers(markers map[int64]string, allowed map[int64]map[string]bool) error {
	for version, name := range markers {
		names, ok := allowed[version]
		if !ok || !names[name] {
			return fmt.Errorf("unknown or mismatched migration marker %d/%q", version, name)
		}
	}
	return nil
}

func validatePrefix(markers map[int64]string, first, last int64) error {
	seenGap := false
	for version := first; version <= last; version++ {
		_, present := markers[version]
		if !present {
			seenGap = true
		} else if seenGap {
			return fmt.Errorf("migration history has a gap before version %d", version)
		}
	}
	return nil
}
