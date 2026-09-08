package sqlitestore

import upstream "github.com/rceman/go-sqlite-store/store"

func sharedBaselineStatements() []upstream.Statement {
	return []upstream.Statement{
		{SQL: `INSERT OR IGNORE INTO replication_state(id,status,updated_at) VALUES(1,'synced',CURRENT_TIMESTAMP)`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_pending_idx ON hub_outbox(published_at, created_at)`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_due_idx ON hub_outbox(published_at,next_attempt_at,created_at,id)`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_operation_idx ON hub_outbox(operation_id)`},
		{SQL: `CREATE INDEX IF NOT EXISTS hub_outbox_retry_idx ON hub_outbox(published_at, next_attempt_at, created_at)`},
		{SQL: `CREATE INDEX IF NOT EXISTS shared_operations_entity_idx ON shared_operations(entity_type,entity_id,revision)`},
		{SQL: `CREATE INDEX IF NOT EXISTS shared_train_task_admissions_train_idx ON shared_train_task_admissions(project_id,train_id)`},
		{SQL: `CREATE TRIGGER IF NOT EXISTS shared_train_task_admission_conflict BEFORE INSERT ON shared_train_task_admissions WHEN EXISTS (SELECT 1 FROM shared_train_task_admissions WHERE project_id=NEW.project_id AND task_id=NEW.task_id AND train_id<>NEW.train_id) BEGIN SELECT RAISE(ABORT,'task is already admitted to another Shared Train'); END`},
		{SQL: `CREATE TRIGGER IF NOT EXISTS shared_train_task_admission_update_conflict BEFORE UPDATE ON shared_train_task_admissions WHEN OLD.train_id<>NEW.train_id BEGIN SELECT RAISE(ABORT,'task is already admitted to another Shared Train'); END`},
		{SQL: `CREATE INDEX IF NOT EXISTS shared_entity_revisions_project_idx ON shared_entity_revisions(project_id, entity_type, entity_id, revision)`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'task',project_id,project_code,next_task_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'train',project_id,project_code,next_train_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'rule',project_id,project_code,next_rule_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'journal',project_id,project_code,next_journal_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'adr',project_id,project_code,next_adr_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) SELECT 'adr',id,COALESCE(json_extract(payload,'$.project_id'),''),CASE WHEN CAST(json_extract(payload,'$.revision') AS INTEGER) >= 1 THEN CAST(json_extract(payload,'$.revision') AS INTEGER) ELSE 1 END,'migration',substr(COALESCE(NULLIF(trim(CAST(json_extract(payload,'$.updated_by') AS TEXT)),''),NULLIF(trim(CAST(json_extract(payload,'$.created_by') AS TEXT)),''),'migration'),1,1024),substr(COALESCE(NULLIF(trim(CAST(json_extract(payload,'$.last_reason') AS TEXT)),''),'migration'),1,1024),json('["migration"]'),payload,COALESCE(NULLIF(json_extract(payload,'$.updated_at'),''),NULLIF(json_extract(payload,'$.created_at'),''),updated_at) FROM shared_adrs`},
	}
}
