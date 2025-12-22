package domain

import (
	"path"
	"strings"
)

// RepositoryRouterRule задаёт переопределения стратегии роутинга для конкретного репозитория.
type RepositoryRouterRule struct {
	Owner                string
	Repository           string
	PreprocessEnabled    *bool
	PreprocessExtensions []string
}

// RouterRules описывает глобальные и репозиторные правила выбора стадий обработки.
type RouterRules struct {
	PreprocessEnabled    bool
	PreprocessExtensions []string
	RepositoryRules      []RepositoryRouterRule
}

type repositoryRule struct {
	owner             string
	repo              string
	preprocessEnabled *bool
	preprocessExt     map[string]struct{}
}

// StageRouter выбирает следующую стадию обработки документа на основе расширения и конфигурации.
type StageRouter struct {
	preprocessEnabled bool
	preprocessExt     map[string]struct{}
	repoRules         []repositoryRule
}

// NewStageRouter создаёт StageRouter с нормализованными правилами.
func NewStageRouter(rules RouterRules) *StageRouter {
	router := &StageRouter{
		preprocessEnabled: rules.PreprocessEnabled,
		preprocessExt:     normalizeExtensions(rules.PreprocessExtensions),
	}
	if len(router.preprocessExt) == 0 {
		router.preprocessExt = defaultPreprocessExtensions()
	}

	for _, rule := range rules.RepositoryRules {
		owner, repo := normalizeRepository(rule.Owner, rule.Repository)
		if owner == "" && repo == "" {
			continue
		}
		router.repoRules = append(router.repoRules, repositoryRule{
			owner:             owner,
			repo:              repo,
			preprocessEnabled: rule.PreprocessEnabled,
			preprocessExt:     normalizeExtensions(rule.PreprocessExtensions),
		})
	}

	return router
}

// NextStageAfterStore возвращает следующую стадию после сохранения оригинала.
func (r *StageRouter) NextStageAfterStore(doc *Document) JobStage {
	if doc == nil {
		return JobStageConvertToMarkdown
	}

	enabled, extensions := r.effectiveRules(doc)
	ext := documentExtension(doc)
	if ext == "docx" {
		return JobStageConvertToMarkdown
	}
	if enabled && ext != "" {
		if _, ok := extensions[ext]; ok {
			return JobStagePreprocessToDocx
		}
	}

	return JobStageConvertToMarkdown
}

func (r *StageRouter) effectiveRules(doc *Document) (bool, map[string]struct{}) {
	if doc == nil {
		return r.preprocessEnabled, cloneExtensions(r.preprocessExt)
	}

	enabled := r.preprocessEnabled
	extensions := r.preprocessExt

	for _, rule := range r.repoRules {
		if rule.matches(doc) {
			if rule.preprocessEnabled != nil {
				enabled = *rule.preprocessEnabled
			}
			if len(rule.preprocessExt) > 0 {
				extensions = rule.preprocessExt
			}
			break
		}
	}

	if len(extensions) == 0 {
		extensions = defaultPreprocessExtensions()
	}

	return enabled, cloneExtensions(extensions)
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
		return r.owner == owner && r.repo == repo
	case r.owner != "":
		return r.owner == owner
	case r.repo != "":
		return r.repo == repo
	default:
		return false
	}
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
	return map[string]struct{}{"doc": {}, "pdf": {}}
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
