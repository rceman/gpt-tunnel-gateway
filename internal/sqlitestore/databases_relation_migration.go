package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

// sharedRelationMaterializedCorrection is the reviewed correction relation
// materialized by the relation migration: Task GTW-TSK585 corrects Task
// GTW-TSK521. It is inserted only when both Tasks exist in the same project.
const (
	sharedRelationMaterializedCorrectionSource = "GTW-TSK585"
	sharedRelationMaterializedCorrectionTarget = "GTW-TSK521"
)

func sharedRelationMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedRelationMigrationVersion,
		Name:    sharedRelationMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS shared_relations (
project_id TEXT NOT NULL,
kind TEXT NOT NULL,
source_id TEXT NOT NULL,
target_id TEXT NOT NULL,
created_at TEXT NOT NULL,
created_by TEXT NOT NULL,
PRIMARY KEY (project_id, kind, source_id, target_id)
)`},
			{SQL: `CREATE INDEX IF NOT EXISTS shared_relations_target_idx ON shared_relations(project_id, target_id, kind, source_id)`},
			{SQL: `INSERT OR IGNORE INTO shared_relations(project_id,kind,source_id,target_id,created_at,created_by)
SELECT json_extract(t.payload,'$.project_id'),'corrects','` + sharedRelationMaterializedCorrectionSource + `','` + sharedRelationMaterializedCorrectionTarget + `',strftime('%Y-%m-%dT%H:%M:%fZ','now'),'migration'
FROM shared_tasks t
WHERE t.id='` + sharedRelationMaterializedCorrectionSource + `' AND json_extract(t.payload,'$.project_id') IS NOT NULL AND EXISTS(SELECT 1 FROM shared_tasks u WHERE u.id='` + sharedRelationMaterializedCorrectionTarget + `' AND json_extract(u.payload,'$.project_id')=json_extract(t.payload,'$.project_id'))`},
		},
	}
}

func localRelationMigration() migrate.Migration {
	return migrate.Migration{
		Version: localRelationMigrationVersion,
		Name:    localRelationMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS local_relations (
project_id TEXT NOT NULL,
kind TEXT NOT NULL,
source_id TEXT NOT NULL,
target_id TEXT NOT NULL,
created_at TEXT NOT NULL,
created_by TEXT NOT NULL,
PRIMARY KEY (project_id, kind, source_id, target_id)
)`},
			{SQL: `CREATE INDEX IF NOT EXISTS local_relations_target_idx ON local_relations(project_id, target_id, kind, source_id)`},
		},
	}
}
