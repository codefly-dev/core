package templates

//
//func CopyTemplateFile(source Source, f shared.File, destination shared.Directory, obj any, override shared.BaseLogger) error {
//	logger := shared.NewLogger("templates.CopyAndApplyTemplate").IfNot(override)
//	base := filepath.Base(string(f))
//	target := path.Join(string(destination), base)
//	logger.Debugf("copying template file <%s> to <%s>", f, target)
//	if source == nil {
//		logger.DebugMe("source is nil")
//		return nil
//	}
//	tmpl := fmt.Sprintf("%s.tmpl", f)
//	data, err := source.ReadFile(shared.File(tmpl))
//	if err != nil {
//		return logger.Wrapf(err, "cannot read file source file: %s", f)
//	}
//	out, err := ApplyTemplate(string(data), obj)
//	if err != nil {
//		return logger.Wrapf(err, "cannot apply template")
//	}
//	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
//	if err != nil {
//		return logger.Wrapf(err, "cannot open file: %s", destination)
//	}
//	_, err = file.Write([]byte(out))
//	if err != nil {
//		return logger.Wrapf(err, "cannot write to file")
//	}
//	err = file.Close()
//	if err != nil {
//		return logger.Wrapf(err, "cannot close file")
//	}
//	return nil
//}
//
//type SimpleTemplaterVisitor struct {
//	source Source
//	logger shared.BaseLogger
//	data   any
//}
//
//func NewSimpleTemplaterVisitor(source Source, data any) *SimpleTemplaterVisitor {
//	return &SimpleTemplaterVisitor{
//		source: source,
//		data:   data,
//	}
//}
//
//func (t *SimpleTemplaterVisitor) Apply(path shared.File, to shared.Directory) error {
//	err := CopyTemplateFile(t.source, path, to, t.data, t.logger)
//	if err != nil {
//		return err
//	}
//	return nil
//}
//
//func (t *SimpleTemplaterVisitor) Ignore(file shared.File) bool {
//	return false
//}
