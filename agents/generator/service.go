package generator

import (
	"context"
	"path"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/generation"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

type visitor struct {
	base     string
	replacer *templates.ServiceReplacer
	ignores  []string
}

func (v *visitor) Apply(ctx context.Context, p string, to string) error {
	w := wool.Get(ctx).In("visitor.Apply", wool.Field("from", p), wool.Field("to", to))
	if !v.Keep(p) {
		w.Trace("ignoring")
		return nil
	}
	w.Trace("copying")
	err := templates.CopyAndReplace(ctx, shared.NewDirReader(), p, to, v.replacer)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}

func (v *visitor) Keep(file string) bool {
	if strings.Contains(file, ".idea") {
		return false
	}
	if strings.HasSuffix(file, ".sum") {
		return false
	}
	if strings.HasSuffix(file, ".lock") {
		return false
	}
	if file == "service.codefly.yaml" {
		return false
	}
	if file == "service.generation.codefly.yaml" {
		return false
	}
	for _, ignore := range v.ignores {
		if strings.Contains(file, ignore) {
			return false
		}
	}
	return true
}

func GenerateServiceTemplate(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("GenerateServiceTemplate", wool.DirField(dir))
	base := path.Join(dir, "base")
	_, err := shared.DirectoryExists(ctx, base)
	if err != nil {
		return w.Wrapf(err, "we expect to find a working service in </base> folder")
	}
	w.Trace("found base to generate new agent templates")
	gen, err := resources.LoadFromDir[generation.Service](ctx, base)
	if err != nil {
		return w.Wrapf(err, "cannot load generation configuration")
	}
	w.Trace("ignoring files", wool.Field("ignores", gen.Ignores))
	replacer := templates.NewServiceReplacer(gen)

	//Copy and add .tmpl
	target := path.Join(dir, "templates/factory")

	v := &visitor{base: base, replacer: replacer, ignores: gen.Ignores}
	err = templates.CopyAndVisit(ctx, shared.NewDirReader(), base, target, templates.AddTemplateSuffix{}, v)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}
