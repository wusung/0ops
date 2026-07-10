package main

import (
	"html/template"
	"testing"
)

// mustTestTemplates builds a minimal, hermetic template set for unit tests so
// render tests do not depend on the on-disk template files. It mirrors the
// production contract: each page template ("blog-post", ...) defines a "content"
// block and delegates the outer frame to "layout". The production entrypoint
// loads the real templates from docs/marketing/site/templates/.
func mustTestTemplates(t *testing.T) *template.Template {
	t.Helper()
	const layout = `{{ define "layout" }}<!doctype html><html><head><link rel="canonical" href="{{ .CanonicalURL }}"></head><body>{{ block "content" . }}{{ end }}</body></html>{{ end }}`
	const blogPost = `{{ define "blog-post" }}{{ template "layout" . }}{{ end }}{{ define "content" }}<article lang="zh">{{ .ZHHTML }}</article><article lang="en">{{ .ENHTML }}</article>{{ end }}`
	tmpl := template.Must(template.New("root").Parse(layout))
	tmpl = template.Must(tmpl.Parse(blogPost))
	return tmpl
}
