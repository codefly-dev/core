package resources

const (
	AGENT = "agent"

	WORKSPACE = "workspace"
	MODULE    = "module"
	SERVICE   = "service"
	ENDPOINT  = "endpoint"

	// EXTERNAL is a capability provided outside the workspace. It appears in
	// dependency graphs so a declaration stays visible, but it is not a service:
	// nothing in the workspace can load it.
	EXTERNAL = "external"
)
