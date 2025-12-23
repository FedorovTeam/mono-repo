package domain

import (
	"path"
	"sort"
	"strings"
)

// RepositoryRouterRule задаёт переопределения стратегии роутинга для конкретного репозитория.
type RepositoryRouterRule struct {
	Owner                    string
	Repository               string
	PathPrefix               string
	PreprocessEnabled        *bool
	PreprocessExtensions     []string
	TableExtractorEnabled    *bool
	TableExtractorExtensions []string
	ArtifactsBucket          string
	ArtifactsPrefixTemplate  string
	ForceTableExtractionPDF  *bool
}

// RouterRules описывает глобальные и репозиторные правила выбора стадий обработки.
type RouterRules struct {
	PreprocessEnabled        bool
	PreprocessExtensions     []string
	TableExtractorEnabled    bool
	TableExtractorExtensions []string
	ArtifactsBucket          string
	ArtifactsPrefixTemplate  string
	RepositoryRules          []RepositoryRouterRule
	TableDetectionMinCount   int
	ForceTableExtractionPDF  bool
}

type repositoryRule struct {
	owner                 string
	repo                  string
	pathPrefix            string
	preprocessEnabled     *bool
	preprocessExt         map[string]struct{}
	tableExtractorEnabled *bool
	tableExtractorExt     map[string]struct{}
	artifactsBucket       string
	artifactsPrefixTmpl   string
	forceTableExtraction  *bool
}

type effectiveRoutingRules struct {
	preprocessEnabled      bool
	preprocessExt          map[string]struct{}
	tableExtractorEnabled  bool
	tableExtractorExt      map[string]struct{}
	artifactsBucket        string
	artifactsPrefixTmpl    string
	tableDetectionMinCount int
	forceTableExtraction   bool
}

// StageRouter выбирает следующую стадию обработки документа на основе расширения и конфигурации.
type StageRouter struct {
	preprocessEnabled      bool
	preprocessExt          map[string]struct{}
	tableExtractorEnabled  bool
	tableExtractorExt      map[string]struct{}
	artifactsBucket        string
	artifactsPrefixTmpl    string
	repoRules              []repositoryRule
	tableDetectionMinCount int
	forceTableExtraction   bool
}

// NewStageRouter создаёт StageRouter с нормализованными правилами.
func NewStageRouter(rules RouterRules) *StageRouter {
	router := &StageRouter{
		preprocessEnabled:      rules.PreprocessEnabled,
		preprocessExt:          normalizeExtensions(rules.PreprocessExtensions),
		tableExtractorEnabled:  rules.TableExtractorEnabled,
		tableExtractorExt:      normalizeExtensions(rules.TableExtractorExtensions),
		artifactsBucket:        strings.TrimSpace(rules.ArtifactsBucket),
		artifactsPrefixTmpl:    strings.TrimSpace(rules.ArtifactsPrefixTemplate),
		tableDetectionMinCount: rules.TableDetectionMinCount,
		forceTableExtraction:   rules.ForceTableExtractionPDF,
	}
	if len(router.preprocessExt) == 0 {
		router.preprocessExt = defaultPreprocessExtensions()
	}
	if router.tableDetectionMinCount <= 0 {
		router.tableDetectionMinCount = 1
	}

	for _, rule := range rules.RepositoryRules {
		owner, repo := normalizeRepository(rule.Owner, rule.Repository)
		if owner == "" && repo == "" && strings.TrimSpace(rule.PathPrefix) == "" {
			continue
		}
		router.repoRules = append(router.repoRules, repositoryRule{
			owner:                 owner,
			repo:                  repo,
			pathPrefix:            strings.TrimSpace(rule.PathPrefix),
			preprocessEnabled:     rule.PreprocessEnabled,
			preprocessExt:         normalizeExtensions(rule.PreprocessExtensions),
			tableExtractorEnabled: rule.TableExtractorEnabled,
			tableExtractorExt:     normalizeExtensions(rule.TableExtractorExtensions),
			artifactsBucket:       strings.TrimSpace(rule.ArtifactsBucket),
			artifactsPrefixTmpl:   strings.TrimSpace(rule.ArtifactsPrefixTemplate),
			forceTableExtraction:  rule.ForceTableExtractionPDF,
		})
	}

	return router
}

// NextStageAfterStore возвращает следующую стадию после сохранения оригинала.
func (r *StageRouter) NextStageAfterStore(doc *Document) JobStage {
	if doc == nil {
		return JobStageConvertToMarkdown
	}

	rules := r.effectiveRules(doc)
	ext := documentExtension(doc)
	strategy := NormalizeConversionStrategy(string(doc.ConversionStrategy))
	doclingWithTables := strategy == ConversionStrategyDoclingWithTables
	wantsTables := doc.ExtractTables || strategy == ConversionStrategyTablesOnly

	if doclingWithTables {
		return JobStageConvertToMarkdown
	}

	if wantsTables && rules.tableExtractorEnabled {
		if canUseTableExtractor(ext, rules.tableExtractorExt) {
			return JobStageExtractTables
		}
	}

	if ext == "pdf" {
		return JobStageConvertToMarkdown
	}

	if ext == "docx" {
		return JobStageConvertToMarkdown
	}
	if rules.preprocessEnabled && ext != "" {
		if _, ok := rules.preprocessExt[ext]; ok {
			return JobStagePreprocessToDocx
		}
	}

	return JobStageConvertToMarkdown
}

// TableExtractionConfig возвращает конфигурацию для шага извлечения таблиц.
func (r *StageRouter) TableExtractionConfig(doc *Document) TableExtractionConfig {
	rules := r.effectiveRules(doc)
	return TableExtractionConfig{
		Enabled:              rules.tableExtractorEnabled,
		ArtifactsBucket:      rules.artifactsBucket,
		ArtifactsPrefixTmpl:  rules.artifactsPrefixTmpl,
		Extensions:           cloneExtensions(rules.tableExtractorExt),
		PreprocessExtensions: cloneExtensions(rules.preprocessExt),
	}
}

// DefaultExtractTables определяет, нужно ли включать извлечение таблиц по умолчанию.
func (r *StageRouter) DefaultExtractTables(doc *Document) bool {
	if doc == nil {
		return false
	}
	strategy := NormalizeConversionStrategy(string(doc.ConversionStrategy))
	return strategy == ConversionStrategyTablesOnly || strategy == ConversionStrategyDoclingWithTables
}

// ShouldExtractTablesAfterConvert определяет, нужно ли запускать извлечение таблиц после конвертации Docling.
func (r *StageRouter) ShouldExtractTablesAfterConvert(doc *Document, extractTables bool, tableCount int, tablePages []int32, markdownTablesHint bool) bool {
	if doc == nil {
		return false
	}
	rules := r.effectiveRules(doc)
	if !rules.tableExtractorEnabled {
		return false
	}
	strategy := NormalizeConversionStrategy(string(doc.ConversionStrategy))
	explicitTables := doc.ExtractTables || strategy == ConversionStrategyTablesOnly
	pages := uniquePages(tablePages)
	detectedTables := tableCount
	if detectedTables == 0 && len(pages) > 0 {
		detectedTables = len(pages)
	}
	detectionSatisfied := detectedTables > 0 && detectedTables >= rules.tableDetectionMinCount

	ext := documentExtension(doc)
	if !canUseTableExtractor(ext, rules.tableExtractorExt) {
		return false
	}

	if explicitTables {
		return true
	}

	if detectionSatisfied {
		return true
	}

	if markdownTablesHint && ext == "pdf" && rules.forceTableExtraction {
		return true
	}

	return extractTables && detectionSatisfied
}

func uniquePages(pages []int32) []int32 {
	if len(pages) == 0 {
		return nil
	}
	seen := make(map[int32]struct{}, len(pages))
	result := make([]int32, 0, len(pages))
	for _, page := range pages {
		if _, exists := seen[page]; exists {
			continue
		}
		seen[page] = struct{}{}
		result = append(result, page)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (r *StageRouter) effectiveRules(doc *Document) effectiveRoutingRules {
	if doc == nil {
		return effectiveRoutingRules{
			preprocessEnabled:      r.preprocessEnabled,
			preprocessExt:          cloneExtensions(r.preprocessExt),
			tableExtractorEnabled:  r.tableExtractorEnabled,
			tableExtractorExt:      cloneExtensions(r.tableExtractorExt),
			artifactsBucket:        r.artifactsBucket,
			artifactsPrefixTmpl:    r.artifactsPrefixTmpl,
			tableDetectionMinCount: r.tableDetectionMinCount,
			forceTableExtraction:   r.forceTableExtraction,
		}
	}

	enabled := r.preprocessEnabled
	extensions := r.preprocessExt
	tableExtractorEnabled := r.tableExtractorEnabled
	tableExtractorExt := r.tableExtractorExt
	artifactsBucket := r.artifactsBucket
	artifactsPrefix := r.artifactsPrefixTmpl
	forceTableExtraction := r.forceTableExtraction

	for _, rule := range r.repoRules {
		if rule.matches(doc) {
			if rule.preprocessEnabled != nil {
				enabled = *rule.preprocessEnabled
			}
			if len(rule.preprocessExt) > 0 {
				extensions = rule.preprocessExt
			}
			if rule.tableExtractorEnabled != nil {
				tableExtractorEnabled = *rule.tableExtractorEnabled
			}
			if len(rule.tableExtractorExt) > 0 {
				tableExtractorExt = rule.tableExtractorExt
			}
			if strings.TrimSpace(rule.artifactsBucket) != "" {
				artifactsBucket = rule.artifactsBucket
			}
			if strings.TrimSpace(rule.artifactsPrefixTmpl) != "" {
				artifactsPrefix = rule.artifactsPrefixTmpl
			}
			if rule.forceTableExtraction != nil {
				forceTableExtraction = *rule.forceTableExtraction
			}
			break
		}
	}

	if len(extensions) == 0 {
		extensions = defaultPreprocessExtensions()
	}
	if strings.TrimSpace(artifactsPrefix) == "" {
		artifactsPrefix = "tables/{{ .DocumentID }}/{{ .QueueID }}/"
	}

	return effectiveRoutingRules{
		preprocessEnabled:      enabled,
		preprocessExt:          cloneExtensions(extensions),
		tableExtractorEnabled:  tableExtractorEnabled,
		tableExtractorExt:      cloneExtensions(tableExtractorExt),
		artifactsBucket:        artifactsBucket,
		artifactsPrefixTmpl:    artifactsPrefix,
		tableDetectionMinCount: r.tableDetectionMinCount,
		forceTableExtraction:   forceTableExtraction,
	}
}

func (r repositoryRule) matches(doc *Document) bool {
	if doc == nil {
		return false
	}

	owner, repo := normalizeRepository(doc.Owner, doc.Repository)
	if owner == "" && doc.Repository != "" && strings.Contains(doc.Repository, "/") {
		parts := strings.SplitN(doc.Repository, "/", 2)
		owner = strings.ToLower(strings.TrimSpace(parts[0]))
		if len(parts) > 1 {
			repo = strings.ToLower(strings.TrimSpace(parts[1]))
		}
	}

	switch {
	case r.owner != "" && r.repo != "":
		return r.owner == owner && r.repo == repo && r.matchesPath(doc.Path)
	case r.owner != "":
		return r.owner == owner && r.matchesPath(doc.Path)
	case r.repo != "":
		return r.repo == repo && r.matchesPath(doc.Path)
	default:
		return r.matchesPath(doc.Path)
	}
}

func (r repositoryRule) matchesPath(pathValue string) bool {
	if strings.TrimSpace(r.pathPrefix) == "" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(pathValue), r.pathPrefix)
}

func documentExtension(doc *Document) string {
	if doc == nil {
		return ""
	}
	return extensionFromStrings(doc.OriginalObjectKey, doc.Name)
}

func extensionFromStrings(values ...string) string {
	for _, val := range values {
		if strings.TrimSpace(val) == "" {
			continue
		}
		ext := path.Ext(val)
		ext = strings.TrimPrefix(ext, ".")
		if ext != "" {
			return strings.ToLower(ext)
		}
	}
	return ""
}

func normalizeExtensions(values []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, val := range values {
		ext := strings.ToLower(strings.TrimSpace(val))
		if ext == "" {
			continue
		}
		ext = strings.TrimPrefix(ext, ".")
		result[ext] = struct{}{}
	}
	return result
}

func normalizeRepository(owner, repo string) (string, string) {
	return strings.ToLower(strings.TrimSpace(owner)), strings.ToLower(strings.TrimSpace(repo))
}

func defaultPreprocessExtensions() map[string]struct{} {
	return map[string]struct{}{"doc": {}}
}

func cloneExtensions(src map[string]struct{}) map[string]struct{} {
	if len(src) == 0 {
		return map[string]struct{}{}
	}
	dst := make(map[string]struct{}, len(src))
	for k := range src {
		dst[k] = struct{}{}
	}
	return dst
}

// TableExtractionConfig описывает настройки артефактов для шага извлечения таблиц.
type TableExtractionConfig struct {
	Enabled              bool
	ArtifactsBucket      string
	ArtifactsPrefixTmpl  string
	Extensions           map[string]struct{}
	PreprocessExtensions map[string]struct{}
}

func canUseTableExtractor(ext string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 || ext == "" {
		return true
	}
	_, ok := allowed[ext]
	return ok
}
