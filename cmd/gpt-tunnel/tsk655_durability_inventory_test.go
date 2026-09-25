package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type tsk665DurabilityRoute struct {
	Path      string `json:"path"`
	Boundary  string `json:"boundary"`
	Access    string `json:"access"`
	Rationale string `json:"rationale"`
}

func TestTSK665Gate20OperatorCLIDurabilityInventory(t *testing.T) {
	inventory := []tsk665DurabilityRoute{
		{Path: "admin", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "top-level Admin Session command dispatches through daemon routes"},
		{Path: "admin session mint", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "Admin Session creation is owned by the live daemon"},
		{Path: "admin session revoke", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "Daemon verifies and revokes only Admin-role Sessions"},
		{Path: "agent register", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon resolves registered project and persists Agent/runtime binding"},
		{Path: "agent send", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns runtime dispatch state"},
		{Path: "agent status", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon resolves local Agent status"},
		{Path: "agent tail", Boundary: "unsupported CLI transport", Access: "LOCAL_NON_DURABLE", Rationale: "command fails closed; it does not open persistence"},
		{Path: "adr create", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon commits canonical ADR state"},
		{Path: "adr list", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical ADR state"},
		{Path: "adr read", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical ADR state"},
		{Path: "daemon install", Boundary: "local service controller", Access: "LOCAL_NON_DURABLE", Rationale: "manages the local daemon service, not Shared/Local application state"},
		{Path: "daemon remove", Boundary: "local service controller", Access: "LOCAL_NON_DURABLE", Rationale: "manages the local daemon service, not Shared/Local application state"},
		{Path: "daemon restart", Boundary: "local service controller", Access: "LOCAL_NON_DURABLE", Rationale: "manages the local daemon service, not Shared/Local application state"},
		{Path: "daemon status", Boundary: "local service controller", Access: "LOCAL_NON_DURABLE", Rationale: "reads local daemon service status"},
		{Path: "format", Boundary: "local gate runner", Access: "LOCAL_NON_DURABLE", Rationale: "runs repository gates without Shared/Local persistence"},
		{Path: "check", Boundary: "local gate runner", Access: "LOCAL_NON_DURABLE", Rationale: "runs repository gates without Shared/Local persistence"},
		{Path: "test", Boundary: "local gate runner", Access: "LOCAL_NON_DURABLE", Rationale: "runs repository gates without Shared/Local persistence"},
		{Path: "verify", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns the Local verification receipt"},
		{Path: "verify status", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads the Local verification receipt"},
		{Path: "work checkpoint", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns the Local checkpoint baseline and receipt"},
		{Path: "work status", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads Local checkpoint state"},
		{Path: "git compare", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git diff", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git log", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git merge-base", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git read-file", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git refresh", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git operation uses the configured mirror"},
		{Path: "git refs", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git show", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git tree", Boundary: "daemon project-config resolve; local Git mirror", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured mirror"},
		{Path: "git worktree-diff", Boundary: "daemon project-config resolve; local Git worktree", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured worktree"},
		{Path: "git worktree-status", Boundary: "daemon project-config resolve; local Git worktree", Access: "DAEMON_API", Rationale: "project metadata resolves through daemon; bounded Git read uses the configured worktree"},
		{Path: "journal migrate", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns Shared journal migration and sequencing"},
		{Path: "plan cutover", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns canonical project state"},
		{Path: "plan history", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads Hub-backed history"},
		{Path: "plan read", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical project state"},
		{Path: "plan render", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads and renders canonical project state"},
		{Path: "plan section-create", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon commits canonical project state"},
		{Path: "plan section-read", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical project state"},
		{Path: "plan section-update", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon commits canonical project state"},
		{Path: "plan update", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon commits canonical project state"},
		{Path: "project identifiers-adopt", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns canonical identifiers and Hub revision checks"},
		{Path: "project identifiers-read", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical identifiers"},
		{Path: "project list", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns Hub project enumeration"},
		{Path: "project onboard", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns bootstrap, Shared state, and Hub writes"},
		{Path: "project read", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical project state"},
		{Path: "project register", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon commits canonical project registration"},
		{Path: "project status", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns semantic status projection"},
		{Path: "project token", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon resolves stable project bootstrap grants"},
		{Path: "project update", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon owns durable project code and config updates"},
		{Path: "project workflow-policy-read", Boundary: "operator-token daemon HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical Shared Rules and configuration"},
		{Path: "task current", Boundary: "session-authorized MCP HTTP", Access: "DAEMON_API", Rationale: "daemon owns Local TaskExecution state"},
		{Path: "task read", Boundary: "session-authorized MCP HTTP", Access: "DAEMON_API", Rationale: "daemon reads canonical Shared Task state"},
		{Path: "task submit-code", Boundary: "durable daemon HTTP admission", Access: "DAEMON_API", Rationale: "daemon owns Worker submission admission"},
		{Path: "task submit-rebase", Boundary: "durable daemon HTTP admission", Access: "DAEMON_API", Rationale: "daemon owns Worker submission admission"},
		{Path: "version", Boundary: "local binary metadata", Access: "LOCAL_NON_DURABLE", Rationale: "reads embedded version only"},
		{Path: "--version", Boundary: "local binary metadata", Access: "LOCAL_NON_DURABLE", Rationale: "reads embedded version only"},
		{Path: "--source-sha", Boundary: "local binary metadata", Access: "LOCAL_NON_DURABLE", Rationale: "reads embedded build revision only"},
		{Path: "help", Boundary: "local static help", Access: "LOCAL_NON_DURABLE", Rationale: "prints static command guidance"},
		{Path: "guide", Boundary: "local static guide", Access: "LOCAL_NON_DURABLE", Rationale: "prints the canonical embedded agent guide"},
		{Path: "test-only main_test_cli_validation_test.go", Boundary: "fixture seed before HTTP operator server", Access: "TEST_ONLY_SETUP_INSPECTION", Rationale: "seeds a disposable project before testing CLI identifier routes through the operator HTTP boundary"},
		{Path: "test-only tsk654_project_token_live_test.go", Boundary: "SQLite read after daemon stop", Access: "TEST_ONLY_SETUP_INSPECTION", Rationale: "asserts exactly one durable bootstrap grant per project after live endpoint tests"},
		{Path: "test-only tsk655_live_gateway_test.go", Boundary: "SQLite read after daemon stop", Access: "TEST_ONLY_SETUP_INSPECTION", Rationale: "asserts daemon-down CLI did not create an active Admin Session"},
		{Path: "test-only tsk657_submit_transport_live_test.go", Boundary: "fixture seed before daemon start", Access: "TEST_ONLY_SETUP_INSPECTION", Rationale: "seeds disposable Shared/Local fixtures before exercising live submit transport"},
		{Path: "test-only tsk659_sequential_submit_live_test.go", Boundary: "fixture seed before daemon start", Access: "TEST_ONLY_SETUP_INSPECTION", Rationale: "seeds disposable sequential Task and Session fixtures before daemon startup"},
	}
	seen := make(map[string]struct{}, len(inventory))
	for _, item := range inventory {
		if item.Path == "" || item.Boundary == "" || item.Rationale == "" {
			t.Fatalf("incomplete Gate-20 route: %#v", item)
		}
		if _, exists := seen[item.Path]; exists {
			t.Fatalf("duplicate Gate-20 path %q", item.Path)
		}
		seen[item.Path] = struct{}{}
		switch item.Access {
		case "DAEMON_API", "LOCAL_NON_DURABLE", "TEST_ONLY_SETUP_INSPECTION":
		default:
			t.Fatalf("unclassified Gate-20 path %q: %s", item.Path, item.Access)
		}
	}
	caseAssertions := []struct {
		file       string
		function   string
		argsIndex  int
		tagName    string
		pathPrefix string
	}{
		{file: "project_commands.go", function: "project", argsIndex: 0, pathPrefix: "project"},
		{file: "plan_commands.go", function: "plan", argsIndex: 0, pathPrefix: "plan"},
		{file: "adr_commands.go", function: "adr", argsIndex: 0, pathPrefix: "adr"},
		{file: "agent_commands.go", function: "agent", argsIndex: 0, pathPrefix: "agent"},
		{file: "journal_commands.go", function: "journal", argsIndex: 0, pathPrefix: "journal"},
		{file: "git_commands.go", function: "gitcmd", argsIndex: 0, pathPrefix: "git"},
		{file: "task_commands.go", function: "task", argsIndex: 0, pathPrefix: "task"},
		{file: "admin_commands.go", function: "admin", argsIndex: 1, pathPrefix: "admin session"},
		{file: "daemon_commands.go", function: "daemon", argsIndex: 0, pathPrefix: "daemon"},
	}
	for _, assertion := range caseAssertions {
		actual := tsk665SwitchStringCases(t, assertion.file, assertion.function, assertion.argsIndex, assertion.tagName)
		want := make([]string, 0)
		for _, item := range inventory {
			if strings.HasPrefix(item.Path, assertion.pathPrefix+" ") {
				want = append(want, strings.TrimPrefix(item.Path, assertion.pathPrefix+" "))
			}
		}
		sort.Strings(actual)
		sort.Strings(want)
		if strings.Join(actual, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("%s dispatch cases=%v inventory=%v", assertion.function, actual, want)
		}
	}
	mainCases := tsk665SwitchStringCases(t, "main.go", "main", -1, "group")
	wantMainCases := []string{"adr", "agent", "daemon", "git", "journal", "plan", "project", "task", "test", "check", "format", "verify", "work"}
	sort.Strings(mainCases)
	sort.Strings(wantMainCases)
	if strings.Join(mainCases, "\x00") != strings.Join(wantMainCases, "\x00") {
		t.Fatalf("top-level durable/local command groups=%v", mainCases)
	}
	var expectedEarlyCommands []string
	for _, item := range inventory {
		if item.Boundary == "local binary metadata" || item.Boundary == "local static help" || item.Boundary == "local static guide" || item.Path == "admin" {
			expectedEarlyCommands = append(expectedEarlyCommands, item.Path)
		}
	}
	actualEarlyCommands := tsk665MainEarlyCommandLiterals(t)
	sort.Strings(expectedEarlyCommands)
	sort.Strings(actualEarlyCommands)
	if strings.Join(actualEarlyCommands, "\x00") != strings.Join(expectedEarlyCommands, "\x00") {
		t.Fatalf("early local command paths=%v inventory=%v", actualEarlyCommands, expectedEarlyCommands)
	}
	var expectedTestOpenFiles []string
	for _, item := range inventory {
		if item.Access == "TEST_ONLY_SETUP_INSPECTION" {
			expectedTestOpenFiles = append(expectedTestOpenFiles, strings.TrimPrefix(item.Path, "test-only "))
		}
	}
	actualTestOpenFiles := tsk665TestSQLiteOpenFiles(t)
	sort.Strings(expectedTestOpenFiles)
	sort.Strings(actualTestOpenFiles)
	if strings.Join(actualTestOpenFiles, "\x00") != strings.Join(expectedTestOpenFiles, "\x00") {
		t.Fatalf("test-only direct SQLite openings=%v inventory=%v", actualTestOpenFiles, expectedTestOpenFiles)
	}
	encoded, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gate-20 operator CLI durability inventory: %s", encoded)
}

func tsk665SwitchStringCases(t *testing.T, path, functionName string, argsIndex int, tagName string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == functionName {
			function = candidate
			break
		}
	}
	if function == nil {
		t.Fatalf("function %s not found in %s", functionName, path)
	}
	var cases []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switchStatement, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		matches := false
		if argsIndex >= 0 {
			index, ok := switchStatement.Tag.(*ast.IndexExpr)
			if ok {
				owner, ownerOK := index.X.(*ast.Ident)
				literal, literalOK := index.Index.(*ast.BasicLit)
				matches = ownerOK && owner.Name == "args" && literalOK && literal.Value == strconv.Itoa(argsIndex)
			}
		} else if identifier, ok := switchStatement.Tag.(*ast.Ident); ok {
			matches = identifier.Name == tagName
		}
		if !matches {
			return true
		}
		for _, statement := range switchStatement.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expression := range clause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				cases = append(cases, value)
			}
		}
		return false
	})
	return cases
}

func tsk665MainEarlyCommandLiterals(t *testing.T) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "main" {
			function = candidate
			break
		}
	}
	if function == nil {
		t.Fatal("main function not found")
	}
	var commands []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		ast.Inspect(statement.Cond, func(condition ast.Node) bool {
			comparison, ok := condition.(*ast.BinaryExpr)
			if !ok || comparison.Op != token.EQL {
				return true
			}
			literal, ok := comparison.Y.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			matches := false
			switch value := comparison.X.(type) {
			case *ast.Ident:
				matches = value.Name == "group"
			case *ast.IndexExpr:
				owner, ownerOK := value.X.(*ast.SelectorExpr)
				index, indexOK := value.Index.(*ast.BasicLit)
				if ownerOK && owner.Sel.Name == "Args" && indexOK && index.Value == "1" {
					base, baseOK := owner.X.(*ast.Ident)
					matches = baseOK && base.Name == "os"
				}
			}
			if matches {
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				commands = append(commands, value)
			}
			return true
		})
		return true
	})
	return commands
}

func tsk665TestSQLiteOpenFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		aliases := make(map[string]struct{})
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(importPath, "/internal/sqlitestore") {
				continue
			}
			name := filepath.Base(importPath)
			if spec.Name != nil {
				name = spec.Name.Name
			}
			aliases[name] = struct{}{}
		}
		hasOpen := false
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Open" {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if ok {
				if _, exists := aliases[owner.Name]; exists {
					hasOpen = true
				}
			}
			return true
		})
		if hasOpen {
			opened = append(opened, path)
		}
	}
	return opened
}

func TestTSK665CLIRejectsProductionDurabilityOwnership(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbiddenPackages := []string{
		"database/sql",
		"/internal/hub",
		"/internal/sqlitestore",
		"/internal/session",
		"/internal/durability",
		"/internal/runtime_log",
		"sqlite",
		"go-sqlite-store",
		"os/exec",
	}
	forbiddenMethods := map[string]struct{}{
		"AdminSessionMint": {}, "AdminSessionRevoke": {},
		"ProjectOnboard": {}, "ProjectUpdate": {}, "AgentBootstrap": {},
		"JournalMigrate": {}, "ProjectToken": {}, "ProjectList": {},
		"ProjectRead": {}, "ProjectStatus": {}, "ProjectIdentifiersRead": {},
		"ProjectIdentifiersAdopt": {}, "ProjectWorkflowPolicyRead": {}, "ProjectRegister": {},
		"AgentSend": {}, "AgentStatus": {}, "PlanRead": {}, "PlanHistory": {},
		"PlanCutover": {}, "PlanUpdate": {}, "PlanSectionRead": {},
		"PlanSectionCreate": {}, "PlanSectionUpdate": {}, "PlanRender": {},
		"ADRList": {}, "ADRRead": {}, "ADRCreate": {},
		"WorkCheckpoint": {}, "WorkCheckpointStatus": {}, "Verify": {}, "VerifyStatus": {},
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		imports := make(map[string]string, len(parsed.Imports))
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range forbiddenPackages {
				if strings.Contains(importPath, forbidden) {
					t.Errorf("%s imports prohibited persistence package %q", path, importPath)
				}
			}
			localName := filepath.Base(importPath)
			if spec.Name != nil {
				localName = spec.Name.Name
			}
			imports[localName] = importPath
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if strings.HasPrefix(selector.Sel.Name, "NewWithDurability") {
					if owner, ok := selector.X.(*ast.Ident); ok && strings.HasSuffix(imports[owner.Name], "/service") {
						t.Errorf("%s calls durability-owning service constructor at %s", path, fset.Position(value.Pos()))
					}
				}
				if owner, ok := selector.X.(*ast.Ident); ok && owner.Name == "s" && selector.Sel.Name != "ExecuteCanonicalTestGate" {
					t.Errorf("%s calls service instance method %s directly at %s", path, selector.Sel.Name, fset.Position(value.Pos()))
				}
				if _, prohibited := forbiddenMethods[selector.Sel.Name]; prohibited {
					t.Errorf("%s calls persistence-owning service method %s at %s", path, selector.Sel.Name, fset.Position(value.Pos()))
				}
			case *ast.SelectorExpr:
				if value.Sel.Name == "Durability" {
					t.Errorf("%s references a direct durability field at %s", path, fset.Position(value.Pos()))
				}
			}
			return true
		})
	}
}
