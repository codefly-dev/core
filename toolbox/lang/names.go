package lang

// Conventional tool names every language plugin exposes through the
// Toolbox contract. The naming rule is `lang.<verb>` in snake_case
// — `lang` because the convention applies to ANY language plugin
// (python, go, rust, …), and snake_case to match the Toolbox
// convention used by other tool sets (`git.status`, `docker.list_containers`).
//
// These constants are the source of truth. The bidirectional bridge
// (NewToolboxFromTooling, ToolingFromToolbox) reads them; renaming
// one here automatically renames it on both sides.
const (
	// LSP / analysis
	ToolListSymbols    = "lang.list_symbols"
	ToolGetDiagnostics = "lang.get_diagnostics"
	ToolGoToDefinition = "lang.go_to_definition"
	ToolFindReferences = "lang.find_references"
	ToolRenameSymbol   = "lang.rename_symbol"
	ToolGetHoverInfo   = "lang.get_hover_info"
	ToolGetCompletions = "lang.get_completions"

	// Modification
	ToolFix       = "lang.fix"
	ToolApplyEdit = "lang.apply_edit"

	// Dependencies
	ToolListDependencies   = "lang.list_dependencies"
	ToolAddDependency      = "lang.add_dependency"
	ToolRemoveDependency   = "lang.remove_dependency"

	// Project-level analysis
	ToolGetProjectInfo = "lang.get_project_info"
	ToolGetCallGraph   = "lang.get_call_graph"

	// Dev validation (delegates to Runtime)
	ToolBuild = "lang.build"
	ToolTest  = "lang.test"
	ToolLint  = "lang.lint"
)

// AllTools is the full conventional tool list every language plugin
// must expose. Test fixtures use it to assert no tool was dropped on
// the bridge.
var AllTools = []string{
	ToolListSymbols, ToolGetDiagnostics, ToolGoToDefinition,
	ToolFindReferences, ToolRenameSymbol, ToolGetHoverInfo,
	ToolGetCompletions, ToolFix, ToolApplyEdit,
	ToolListDependencies, ToolAddDependency, ToolRemoveDependency,
	ToolGetProjectInfo, ToolGetCallGraph,
	ToolBuild, ToolTest, ToolLint,
}
