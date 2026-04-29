// Package generation provides the template-driven code generator that
// powers `codefly add` and agent scaffolding.
//
// Given a generation spec (source template tree, destination, and a
// variable bag), it walks the tree, renders .tmpl files, and writes
// the result while honoring per-agent overrides for filenames,
// directory layout, and post-generation hooks.
package generation
