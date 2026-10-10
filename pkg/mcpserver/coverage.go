package mcpserver

// What the MCP server covers, kept beside the tools so a new command or
// pack cannot ship without a tool or a stated reason (TestCoverage).

// CommandTools maps each lidza command to the tools that do its work.
var CommandTools = map[string][]string{
	"ship":        {"lidza_ship"},
	"build":       {"lidza_build"},
	"check":       {"lidza_check"},
	"gen":         {"lidza_gen", "lidza_gen_resource", "lidza_gen_llms"},
	"pack":        {"lidza_pack_add", "lidza_pack_build", "lidza_pack_scaffold", "lidza_packs"},
	"db":          {"lidza_db_migrate", "lidza_db_rollback", "lidza_db_status", "lidza_db_new"},
	"test":        {"lidza_test"},
	"verify":      {"lidza_verify"},
	"audit":       {"lidza_audit_layout", "lidza_audit_performance"},
	"doctor":      {"lidza_doctor"},
	"context":     {"lidza_context"},
	"api":         {"lidza_api"},
	"snippet":     {"lidza_snippet"},
	"credentials": {"lidza_credentials_list", "lidza_credentials_set"},
	"admin":       {"lidza_admins", "lidza_owner_status"},
	"decision":    {"lidza_decision_add"},
	"brief":       {"lidza_brief", "lidza_brief_answer", "lidza_brief_skip"},
	"note":        {"lidza_note_add"},
	"recipe":      {"lidza_recipe_add", "lidza_recipes"},
}

// TerminalOnly are the commands with no tool, and why.
var TerminalOnly = map[string]string{
	"new":       "creates a project; an agent works inside one",
	"install":   "installs dependencies, services and databases on the developer's machine",
	"setup":     "the old name of install",
	"dev":       "a long-running server; the agent reads its output with lidza_logs",
	"benchmark": "a load test against the running app, run deliberately",
	"update":    "moves the app to another framework version: the developer's decision",
	"mcp":       "is this server",
	"version":   "printed by the server's initialize reply",
	"help":      "the tools describe themselves",
}

// PackTools maps each official Go pack to the tools that show what it
// holds or does.
var PackTools = map[string][]string{
	"db":        {"lidza_db_status"},
	"auth":      {"lidza_workspaces", "lidza_owner_status", "lidza_admins"},
	"jobs":      {"lidza_jobs"},
	"mail":      {"lidza_mail"},
	"llm":       {"lidza_llm", "lidza_llm_usage"},
	"storage":   {"lidza_storage"},
	"analytics": {"lidza_errors"},
	"audit":     {"lidza_audit"},
	"hooks":     {"lidza_hooks"},
	"billing":   {"lidza_billing"},
}

// PackNoTool are the official Go packs with nothing stored to inspect,
// and why.
var PackNoTool = map[string]string{
	"cache":    "entries expire; nothing an agent needs to read back",
	"i18n":     "the catalogs are files in locales/ the agent reads",
	"realtime": "messages are not kept; publish and watch them in the app",
}
