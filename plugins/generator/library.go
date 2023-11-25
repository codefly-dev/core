package generator

//
//func isValidTemplateExpression(s string) bool {
//	_, err := template.New("validate").Parse(s)
//	return err == nil
//}
//
//type Evaluation struct {
//	MatchSummary *MatchSummary
//}
//
//func (e *Evaluation) Files() []string {
//	var files []string
//	for k := range e.MatchSummary.FileMatches {
//		files = append(files, k)
//	}
//	return files
//}
//
//func toCamelCase(str string) string {
//	words := strings.FieldsFunc(str, func(r rune) bool {
//		return r == '_' || r == ' ' || r == '-'
//	})
//	caser := cases.Title(language.English)
//	for i := 0; i < len(words); i++ {
//
//		words[i] = caser.String(words[i])
//	}
//
//	return strings.Join(words, "")
//}
//
//func OtherMatches(hit string) []string {
//	var other []string
//	if s, ok := strings.CutPrefix(hit, "_codefly_"); ok {
//		// Proto go convention _codefly_server_name_ -> XCodeflyServerName
//		other = append(other, fmt.Sprintf("XCodefly%s", toCamelCase(s)))
//	}
//	return other
//
//}
//
//func EvaluateBase(dir string, gen *configurations.LibraryGeneration) (*Evaluation, error) {
//	logger := shared.NewLogger("generator.EvaluateBase")
//	matcher, err := NewRegexpMatcher(gen.Pattern)
//	if err != nil {
//		return nil, logger.Wrapf(err, "cannot create matcher")
//	}
//	g := NewGrep(dir, gen.Extensions, matcher, gen.AutoGenerated)
//	summary, err := g.FindFiles(OtherMatches)
//	logger.Debugf("Found %d files to templatize", len(summary.FileMatches))
//	logger.Debugf("Found %d hits to templatize: %v", len(summary.Hits), summary.Hits)
//	if err != nil {
//		return nil, logger.Wrapf(err, "while finding files to templates")
//	}
//	// Update the generation files to make it easier to set all occurrences for replacements
//	replacements := gen.Replacements
//	presents := make(map[string]string)
//	for _, r := range replacements {
//		presents[r.From] = r.To
//	}
//
//	// Update the gen file with the hits
//	var missing []string
//	for _, hit := range summary.Hits {
//		if v, ok := presents[hit]; ok && v != "" {
//			if isValidTemplateExpression(v) {
//				continue
//			}
//			warn := shared.NewUserWarning("The replacement <%s> set to <%s> and is not a valid template expression", hit, v)
//			logger.Warn(warn)
//		}
//		missing = append(missing, hit)
//		replacements = append(replacements, shared.Replacement{From: hit, To: ""})
//	}
//
//	gen.Replacements = replacements
//	// TODO: Need to fix this once and for all
//	err = gen.SaveToDir(path.Join(dir, ".."))
//	if err != nil {
//		return nil, logger.Wrapf(err, "cannot save generation file")
//	}
//	if len(missing) > 0 {
//		logger.Message("Please update the generation file <%s> with the replacements %v", configurations.LibraryGenerationConfigurationName, missing)
//		os.Exit(0)
//	}
//	logger.Debugf("all replacements are set to templatize")
//	return &Evaluation{MatchSummary: summary}, nil
//}
//
//func CreateLibrary(dir string) error {
//	logger := shared.NewLogger("generator.CreateLibrary<%s>", path.Base(dir))
//	gen, err := configurations.LoadFromDir[configurations.LibraryGeneration](dir)
//	if err != nil {
//		return logger.Wrapf(err, "cannot load library generation from <%s>", dir)
//	}
//	logger.Debugf("loaded libraries generation at <%s>", dir)
//	root := path.Join(dir, gen.Root)
//	evaluation, err := EvaluateBase(root, gen)
//	if err != nil {
//		return logger.Wrapf(err, "cannot evaluate base")
//	}
//	// logger.Debugf(evaluation.MatchSummary.Pretty())
//	err = CreateTemplatizedVersion(gen, root, evaluation)
//	if err != nil {
//		return logger.Wrapf(err, "cannot templatize")
//	}
//	return nil
//}
//
//type TemplatizeVisitor struct {
//	Evaluation   *Evaluation
//	Replacements []shared.Replacement
//	Files        []string
//	Exclude      []string
//}
//
//type S = string
//
//func (t *TemplatizeVisitor) Apply(p shared.File, to shared.Dir) error {
//	//if t.Ignore(p) {
//	//	return nil
//	//}
//	//logger := shared.NewLogger("generator.TemplatizeVisitor.Apply<%s>", path.Base(S(p)))
//	//destination := path.Join(S(to), path.Base(S(p)))
//	//// Check if the file is in the match summary hit list
//	//if !slices.Contains(t.Files, S(p)) {
//	//	err := shared.CopyFile(p.Relative(), destination)
//	//	if err != nil {
//	//		return logger.Wrapf(err, "cannot copy file")
//	//	}
//	//	// logger.Debugf("copy")
//	//	return nil
//	//}
//	//
//	//err := shared.CopyFileWithReplacement(S(p), destination, t.Replacements)
//	//if err != nil {
//	//	return logger.Wrapf(err, "cannot copy file with replacements")
//	//}
//	//// logger.Debugf("copy with replace")
//	return nil
//}
//
////
////func (t *TemplatizeVisitor) Ignore(p templates.FilePath) bool {
////	if strings.Contains(S(p), "library.generation.codefly.yaml") {
////		return true
////	}
////	for _, excl := range t.Exclude {
////		if strings.Contains(S(p), excl) {
////			return true
////		}
////	}
////	return false
////}
//
//func CreateTemplatizedVersion(gen *configurations.LibraryGeneration, dir string, evaluation *Evaluation) error {
//	logger := shared.NewLogger("generator.CreateTemplatizedVersion<%s>", dir)
//	// Will copy all files into the template directory
//	templateDir := path.Join(dir, "../../templates")
//	logger.Debugf("creating template directory: %s", templateDir)
//	err := os.RemoveAll(templateDir)
//	if err != nil {
//		return logger.Wrapf(err, "cannot remove template directory <%s>", templateDir)
//	}
//	err = os.MkdirAll(templateDir, 0755)
//	if err != nil {
//		return logger.Wrapf(err, "cannot create template directory <%s>", templateDir)
//	}
//	//templatize := TemplatizeVisitor{Files: evaluation.Files(), Exclude: gen.Exclude, Replacements: gen.Replacements}
//	//
//	//err = templates.CopyWithModifier(templates.shared.Directory(dir), templates.shared.Directory(templateDir), &templatize)
//	//if err != nil {
//	//	return logger.Wrapf(err, "cannot copy files to template directory <%s>", templateDir)
//	//}
//
//	return nil
//}
