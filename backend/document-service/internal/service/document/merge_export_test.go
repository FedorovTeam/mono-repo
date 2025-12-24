package document

// MergeMarkdownForTests предоставляет доступ к приватной логике слияния для вспомогательных скриптов и интеграционных тестов.
func MergeMarkdownForTests(baseMarkdown, tablesMarkdown string) (string, error) {
	return mergeMarkdownWithTables(baseMarkdown, tablesMarkdown)
}
