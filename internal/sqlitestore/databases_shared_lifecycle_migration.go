package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	"github.com/rceman/go-sqlite-store/store"
)

var sharedLifecycleMigration = migrate.Migration{
	Version: 12,
	Name:    sharedLifecycleMigrationName,
	Statements: []store.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS shared_entity_sequences (entity_type TEXT NOT NULL, project_id TEXT NOT NULL, project_code TEXT NOT NULL, next_number INTEGER NOT NULL, PRIMARY KEY(entity_type, project_id))`},
		{SQL: `CREATE TABLE IF NOT EXISTS shared_entity_revisions (entity_type TEXT NOT NULL, entity_id TEXT NOT NULL, project_id TEXT NOT NULL, revision INTEGER NOT NULL, mutation_kind TEXT NOT NULL, actor TEXT NOT NULL, reason TEXT NOT NULL, changed_fields BLOB NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, PRIMARY KEY(entity_type, entity_id, revision))`},
		{SQL: `CREATE INDEX IF NOT EXISTS shared_entity_revisions_project_idx ON shared_entity_revisions(project_id, entity_type, entity_id, revision)`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'task',project_id,project_code,next_task_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'train',project_id,project_code,next_train_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'rule',project_id,project_code,next_rule_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'journal',project_id,project_code,next_journal_number FROM shared_project_identifiers`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'adr',project_id,project_code,next_adr_number FROM shared_project_identifiers`},
		{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'task',p.project_id,p.project_code,MAX(p.next_task_number,COALESCE(s.next_task_number,1)) FROM shared_project_identifiers p LEFT JOIN shared_task_sequences s ON s.project_id=p.project_id ON CONFLICT(entity_type,project_id) DO UPDATE SET project_code=excluded.project_code,next_number=CASE WHEN excluded.next_number > shared_entity_sequences.next_number THEN excluded.next_number ELSE shared_entity_sequences.next_number END`},
		{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'adr',p.project_id,p.project_code,MAX(p.next_adr_number,COALESCE(s.next_adr_number,1)) FROM shared_project_identifiers p LEFT JOIN shared_adr_sequences s ON s.project_id=p.project_id ON CONFLICT(entity_type,project_id) DO UPDATE SET project_code=excluded.project_code,next_number=CASE WHEN excluded.next_number > shared_entity_sequences.next_number THEN excluded.next_number ELSE shared_entity_sequences.next_number END`},
		{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'task',s.project_id,s.project_code,MAX(COALESCE(p.next_task_number,1),s.next_task_number) FROM shared_task_sequences s LEFT JOIN shared_project_identifiers p ON p.project_id=s.project_id ON CONFLICT(entity_type,project_id) DO UPDATE SET project_code=excluded.project_code,next_number=CASE WHEN excluded.next_number > shared_entity_sequences.next_number THEN excluded.next_number ELSE shared_entity_sequences.next_number END`},
		{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) SELECT 'adr',s.project_id,s.project_code,MAX(COALESCE(p.next_adr_number,1),s.next_adr_number) FROM shared_adr_sequences s LEFT JOIN shared_project_identifiers p ON p.project_id=s.project_id ON CONFLICT(entity_type,project_id) DO UPDATE SET project_code=excluded.project_code,next_number=CASE WHEN excluded.next_number > shared_entity_sequences.next_number THEN excluded.next_number ELSE shared_entity_sequences.next_number END`},
		{SQL: `INSERT OR IGNORE INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) SELECT 'adr',id,COALESCE(json_extract(payload,'$.project_id'),''),CASE WHEN CAST(json_extract(payload,'$.revision') AS INTEGER) >= 1 THEN CAST(json_extract(payload,'$.revision') AS INTEGER) ELSE 1 END,'migration',substr(COALESCE(NULLIF(trim(CAST(json_extract(payload,'$.updated_by') AS TEXT)),''),NULLIF(trim(CAST(json_extract(payload,'$.created_by') AS TEXT)),''),'migration'),1,1024),substr(COALESCE(NULLIF(trim(CAST(json_extract(payload,'$.last_reason') AS TEXT)),''),'migration'),1,1024),json('["migration"]'),payload,COALESCE(NULLIF(json_extract(payload,'$.updated_at'),''),NULLIF(json_extract(payload,'$.created_at'),''),updated_at) FROM shared_adrs`},
	},
}
