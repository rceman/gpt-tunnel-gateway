package sqlitestore

import (
	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

func sharedRelationOutboxMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedRelationOutboxMigrationVersion,
		Name:    sharedRelationOutboxMigrationName,
		Statements: []upstream.Statement{{SQL: `CREATE TRIGGER IF NOT EXISTS shared_relations_hub_outbox_after_insert
AFTER INSERT ON shared_relations
BEGIN
 INSERT OR IGNORE INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at,operation_id,request_sha256)
 VALUES(
  'relation-'||NEW.project_id||'|'||NEW.kind||'|'||NEW.source_id||'|'||NEW.target_id,
  'relation',
  NEW.project_id||'|'||NEW.kind||'|'||NEW.source_id||'|'||NEW.target_id,
  NEW.project_id,
  1,
  'relation-create',
  json_object('schema_version',1,'project_id',NEW.project_id,'kind',NEW.kind,'source',NEW.source_id,'target',NEW.target_id,'created_at',NEW.created_at,'created_by',NEW.created_by),
  NEW.created_at,
  'relation-'||NEW.project_id||'|'||NEW.kind||'|'||NEW.source_id||'|'||NEW.target_id,
  ''
 );
END`}},
	}
}

func sharedRelationOutboxBlobMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedRelationOutboxBlobMigrationVersion,
		Name:    sharedRelationOutboxBlobMigrationName,
		Statements: []upstream.Statement{
			{SQL: `DROP TRIGGER IF EXISTS shared_relations_hub_outbox_after_insert`},
			{SQL: `CREATE TRIGGER shared_relations_hub_outbox_after_insert
AFTER INSERT ON shared_relations
BEGIN
 INSERT OR IGNORE INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at,operation_id,request_sha256)
 VALUES(
  'relation-'||NEW.project_id||'|'||NEW.kind||'|'||NEW.source_id||'|'||NEW.target_id,
  'relation',
  NEW.project_id||'|'||NEW.kind||'|'||NEW.source_id||'|'||NEW.target_id,
  NEW.project_id,
  1,
  'relation-create',
  CAST(json_object('schema_version',1,'project_id',NEW.project_id,'kind',NEW.kind,'source',NEW.source_id,'target',NEW.target_id,'created_at',NEW.created_at,'created_by',NEW.created_by) AS BLOB),
  NEW.created_at,
  'relation-'||NEW.project_id||'|'||NEW.kind||'|'||NEW.source_id||'|'||NEW.target_id,
  ''
 );
END`},
		},
	}
}
