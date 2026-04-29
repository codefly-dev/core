// Package templates is the template engine that materializes agent
// scaffolding, service stubs, and configuration files from
// templates/factory/ and templates/builder/ trees.
//
// Templates use Go text/template syntax and a fixed set of variables
// (Service, Module, Workspace, Agent, …). Files ending in .tmpl are
// rendered; others are copied verbatim. Replacer rewrites filenames and
// directory names that contain template placeholders.
package templates
