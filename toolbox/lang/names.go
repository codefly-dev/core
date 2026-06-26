package lang

// Conventional tool names every language plugin exposes through the
// Toolbox contract. The naming rule is `lang.<verb>` in snake_case
// — `lang` because the convention applies to ANY language plugin
// (python, go, rust, …), and snake_case to match the Toolbox
// convention used by other tool sets (`git.status`, `docker.list_containers`).
//
// These constants are the stable names used by the bidirectional bridge
// (NewToolboxFromTooling, ToolingFromToolbox). The exported catalog is
// derived from the bridge spec table, so callers should use ToolNames()
// when they need the complete set.
const (
	// Modification
	ToolFix       = "lang.fix"
	ToolApplyEdit = "lang.apply_edit"

	// Dependencies
	ToolListDependencies = "lang.list_dependencies"
	ToolAddDependency    = "lang.add_dependency"
	ToolRemoveDependency = "lang.remove_dependency"

	// Project metadata
	ToolGetProjectInfo = "lang.get_project_info"

	// Dev validation (delegates to Runtime)
	ToolBuild = "lang.build"
	ToolTest  = "lang.test"
	ToolLint  = "lang.lint"
)
