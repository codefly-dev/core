// Package launch bridges resources.Toolbox manifests to running
// host.Plugin processes. Tiny on purpose — the heavy lifting lives
// in host (process management) and resources (manifest validation);
// this package just composes them.
//
// Why not put Launch on *resources.Toolbox directly: resources is a
// foundational package and adding a dependency on toolbox/host
// would create a downward-pointing import (toolbox already imports
// resources via the manifest). Splitting the wire here keeps
// resources free of plugin-runtime concerns.
package launch
