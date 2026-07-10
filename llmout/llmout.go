// Package llmout compresses raw command output before it is returned to the
// model, using the gortk filter catalog (https://github.com/codefly-dev/gortk).
//
// It is the integration seam for codefly's language agents: a Build/Test/Lint
// implementation that has captured a command's combined output passes it
// through Compress, tagged with the command that produced it, so the matching
// gortk filter strips noise (downloads, progress, "ok" lines) while preserving
// diagnostics. Commands with no dedicated filter pass through essentially
// unchanged (only size-bounded), so this is always safe to apply.
package llmout

import "github.com/codefly-dev/gortk"

// registry is built once and shared; it is read-only after construction and
// safe for concurrent use.
var registry = gortk.Default()

// Compress runs the gortk catalog over a command's output and returns the
// compact, model-facing text. name/args identify the command so the right
// filter applies, e.g. ("go","vet") -> go-vet, ("ruff","check") -> ruff,
// ("cargo","build") -> cargo. The combined output goes in as stdout; gortk
// filters read both streams, so a single combined string is fine.
func Compress(name string, args []string, output string) string {
	return registry.Compress(gortk.Command{
		Name:   name,
		Args:   args,
		Stdout: []byte(output),
	}).Text
}

// CompressResult is like Compress but also returns whether anything was dropped
// and a short human-readable note (for logging "compressed N noise lines").
func CompressResult(name string, args []string, output string) (text string, lossy bool, note string) {
	res := registry.Compress(gortk.Command{
		Name:   name,
		Args:   args,
		Stdout: []byte(output),
	})
	return res.Text, !res.Lossless(), res.Truncation.Note
}
