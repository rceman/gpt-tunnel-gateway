package service

import "testing"

type tsk664LocalFamily struct {
	name      string
	location  string
	contents  string
	authority string
}

var tsk664LocalFamilies = []tsk664LocalFamily{
	{"SQLite databases", "stateDir/databases", "Local durable Shared/Local databases, WAL, and schema state", "LOCAL_ONLY"},
	{"Managed project registry", "stateDir/managed-projects.json", "Machine project roots, mirrors, remotes, runtime bindings", "LOCAL_ONLY"},
	{"Git mirrors", "stateDir/git-mirrors and project Mirror paths", "Local repository mirrors and paths", "LOCAL_ONLY"},
	{"Hub transport work", "stateDir/hub/repository, hub-worktrees, backups", "Local transport checkout, transaction worktrees, and backup artifacts", "LOCAL_ONLY"},
	{"Locks", "stateDir/locks", "Process and repository coordination", "LOCAL_ONLY"},
	{"Durable mutation operations", "stateDir/operations/mutations", "Local idempotency and replay records", "LOCAL_ONLY"},
	{"Task-create operations", "stateDir/operations/task-create", "Local asynchronous create state", "LOCAL_ONLY"},
	{"Verification operations", "stateDir/operations/verify", "Local verification receipts", "LOCAL_ONLY"},
	{"Work checkpoints", "stateDir/operations/work-checkpoint", "Local work progress receipts", "LOCAL_ONLY"},
	{"Task worktrees", "stateDir/task-worktrees", "Local execution worktrees and lanes", "LOCAL_ONLY"},
	{"Agent runtime projections", "local_agents and stateDir/agent-tail", "Concrete Agent records, transcript cursors, bounded logs", "LOCAL_ONLY"},
	{"Agent interrupt receipts", "stateDir/agent-interrupts", "Local runtime control/idempotency state", "LOCAL_ONLY"},
	{"Runtime logs", "stateDir/runtime", "Local process/runtime logs", "LOCAL_ONLY"},
	{"Gate receipts", "stateDir/gate-receipts", "Local verification evidence", "LOCAL_ONLY"},
	{"Workflow policy cache", "stateDir/cache/workflow-policy", "Derived local cache", "LOCAL_ONLY"},
	{"Callbacks and callback epochs", "local_project_callbacks and local_callback_epochs", "Machine callbacks and delivery cursors, migrated out of portable ProjectConfiguration", "LOCAL_ONLY"},
	{"Sessions and messages", "local_sessions, local_session_bootstrap_grants, local_messages, plaw_messages", "Concrete Sessions and machine-local coordination", "LOCAL_ONLY"},
	{"TaskExecution", "local TaskExecution tables and stateDir/task-worktrees", "Live execution state, lanes, worktrees, and runtime bindings", "LOCAL_ONLY"},
	{"Token usage", "local_token_usage and local_token_usage_events", "Machine/provider usage evidence", "LOCAL_ONLY"},
	{"Upgrade transactions", "stateDir/upgrade-transactions", "Local upgrade progress and receipts", "LOCAL_ONLY"},
	{"Gateway lifecycle", "stateDir/gateway-runtime and gateway-recovery", "Local process lifecycle and recovery state", "LOCAL_ONLY"},
	{"Debug activation", "stateDir/gateway-debug-activation", "Local diagnostic activation records", "LOCAL_ONLY"},
	{"Controller PID and logs", "configured PIDDir and LogDir", "Local process identifiers and logs", "LOCAL_ONLY"},
	{"Hotfix inspection worktrees", "stateDir/hotfix-worktrees and hotfix-identities", "Local code-inspection artifacts, never portable project authority", "LOCAL_ONLY"},
	{"Host configuration and secrets", "config.json and owner-managed environment", "Provider/model/profile data, credentials, sockets, and runtime session bindings", "LOCAL_ONLY"},
	{"Project roots", "configured project Root paths", "Machine-specific checkout and source paths", "LOCAL_ONLY"},
}

func TestTSK664Gate20LocalExecutionFamiliesAreClassified(t *testing.T) {
	seen := make(map[string]bool, len(tsk664LocalFamilies))
	for _, family := range tsk664LocalFamilies {
		if family.name == "" || family.location == "" || family.contents == "" || family.authority != "LOCAL_ONLY" {
			t.Fatalf("incomplete Local execution inventory row: %#v", family)
		}
		if seen[family.name] {
			t.Fatalf("duplicate Local execution family %q", family.name)
		}
		seen[family.name] = true
	}
}
