// Package lang defines the conventional language toolbox: the stable
// set of tool names every language plugin (python, go, rust, ...)
// exposes through the unified Toolbox contract.
//
// Why this package exists: the Tooling proto service has 17 typed
// RPCs that every language agent implements (ListSymbols, Test, Lint,
// FindReferences, etc.). Going forward, every callable codefly
// surface speaks the Toolbox contract. To preserve Mind's typed
// ergonomics WITHOUT keeping two parallel proto contracts, this
// package provides:
//
//   - Stable convention names (Tool[ListSymbols] = "lang.list_symbols", etc.)
//   - A bridge in both directions:
//       NewToolboxFromTooling — wrap an existing Tooling impl as a Toolbox.
//                               Lets language agents migrate by adding
//                               one line to PluginRegistration; the
//                               Tooling impl stays unchanged.
//       ToolingFromToolbox    — wrap a Toolbox client as a typed
//                               ToolingClient. Lets Mind keep its
//                               existing call sites.
//
// Both bridges round-trip via protojson + structpb, so the typed
// proto messages travel intact. The wire is unified (Toolbox); the
// Go API surfaces remain typed at the boundaries that need them.
//
// Migration shape:
//
//   - Phase B (this work): Tooling impls stay; agents register both
//     Tooling and a NewToolboxFromTooling-wrapped Toolbox. Mind can
//     opt into the Toolbox surface (via ToolingFromToolbox) per
//     consumer site.
//   - Phase α (follow-up): Language agents move directly to a Toolbox
//     impl using these tool names; the Tooling proto + generated code
//     are deleted. NewToolboxFromTooling becomes obsolete; the typed
//     ToolingFromToolbox wrapper survives as Mind's stable interface.
package lang
