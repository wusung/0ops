package main

import (
	"strings"
	"testing"
)

const sampleWithSlug = `---
cadence: weekly
source: docs/adrs/0002-idempotency-and-compensation.md
slug: ship-with-ai-agent-safely
---

## 中文

# 讓 AI 幫你出貨

你的 AI 已經會寫 code 了。**天生安全**：叫它做兩次，只發生一次。

` + "`curl -fsSL https://0ops.sh/install | sh`" + `

## English

# Let your AI ship it

Your AI already writes the code. **Safe by design**: tell it twice, it happens once.

` + "`0ops apps create`" + `
`

const sampleNoSlug = `---
cadence: monthly
source: docs/adrs/0099-example.md
---

## 中文

# 標題甲

段落一。

## English

# Title A

Paragraph one.
`

func TestParsePost_FrontMatterAndBilingual(t *testing.T) {
	p, warn, err := parsePost("2026-07-05-ship-with-ai-agent-safely.md", []byte(sampleWithSlug))
	if err != nil {
		t.Fatalf("parsePost error: %v", err)
	}
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if p.Slug != "ship-with-ai-agent-safely" {
		t.Errorf("slug = %q, want ship-with-ai-agent-safely", p.Slug)
	}
	if p.Cadence != "weekly" {
		t.Errorf("cadence = %q, want weekly", p.Cadence)
	}
	if p.Source != "docs/adrs/0002-idempotency-and-compensation.md" {
		t.Errorf("source = %q, want the adr path", p.Source)
	}
	if strings.TrimSpace(p.ZHMarkdown) == "" {
		t.Error("zh section is empty")
	}
	if strings.TrimSpace(p.ENMarkdown) == "" {
		t.Error("en section is empty")
	}
	if !strings.Contains(p.ZHMarkdown, "讓 AI 幫你出貨") {
		t.Errorf("zh section missing expected heading: %q", p.ZHMarkdown)
	}
	if !strings.Contains(p.ENMarkdown, "Let your AI ship it") {
		t.Errorf("en section missing expected heading: %q", p.ENMarkdown)
	}
	// zh section must not bleed into en section.
	if strings.Contains(p.ZHMarkdown, "Let your AI ship it") {
		t.Error("zh section bled into en content")
	}
}

func TestParsePost_MissingSlugDerivesFromFilenameWithWarning(t *testing.T) {
	p, warn, err := parsePost("2026-07-05-provable-agent-audit-trail.md", []byte(sampleNoSlug))
	if err != nil {
		t.Fatalf("parsePost error: %v", err)
	}
	if warn == "" {
		t.Error("expected a warning when slug is missing")
	}
	if p.Slug != "provable-agent-audit-trail" {
		t.Errorf("derived slug = %q, want provable-agent-audit-trail", p.Slug)
	}
}

func TestCanonicalURL(t *testing.T) {
	cases := []struct {
		base, slug, want string
	}{
		{"https://0ops.sh", "my-slug", "https://0ops.sh/blog/my-slug"},
		{"https://0ops.sh/", "my-slug", "https://0ops.sh/blog/my-slug"},
		{"https://0ops.sh///", "my-slug", "https://0ops.sh/blog/my-slug"},
	}
	for _, c := range cases {
		if got := canonicalURL(c.base, c.slug); got != c.want {
			t.Errorf("canonicalURL(%q,%q) = %q, want %q", c.base, c.slug, got, c.want)
		}
	}
}

func TestRenderMarkdown_BoldCodeHeadings(t *testing.T) {
	html := renderMarkdown("# Head\n\nA **bold** and `code` line.")
	for _, want := range []string{"<h1", "Head", "<strong>bold</strong>", "<code>code</code>"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered html missing %q; got:\n%s", want, html)
		}
	}
}

func TestRenderPost_NoPlaceholdersAndBilingual(t *testing.T) {
	tmpls := mustTestTemplates(t)
	p, _, err := parsePost("post.md", []byte(sampleWithSlug))
	if err != nil {
		t.Fatalf("parsePost: %v", err)
	}
	page := p.toPage("https://0ops.sh")
	out, err := renderTemplate(tmpls, "blog-post", page)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if strings.Contains(out, "{{") {
		t.Errorf("rendered post still contains a {{ placeholder:\n%s", out)
	}
	if !strings.Contains(out, "讓 AI 幫你出貨") {
		t.Error("rendered post missing zh content")
	}
	if !strings.Contains(out, "Let your AI ship it") {
		t.Error("rendered post missing en content")
	}
	if !strings.Contains(out, "https://0ops.sh/blog/ship-with-ai-agent-safely") {
		t.Error("rendered post missing canonical url")
	}
}

func TestSafeSlug_RejectsPathEscape(t *testing.T) {
	// A slug must never escape the blog/ output directory when used as a filename.
	for _, bad := range []string{"../evil", "..", "a/b", "/abs", "foo/../bar", ""} {
		if got, err := safeSlug(bad); err == nil {
			t.Errorf("safeSlug(%q) = %q, nil; want rejection", bad, got)
		}
	}
	// A clean single-segment slug passes through unchanged.
	for _, ok := range []string{"ship-with-ai-agent-safely", "provable-agent-audit-trail"} {
		got, err := safeSlug(ok)
		if err != nil {
			t.Errorf("safeSlug(%q) unexpected error: %v", ok, err)
		}
		if got != ok {
			t.Errorf("safeSlug(%q) = %q, want unchanged", ok, got)
		}
	}
}

func TestRenderMarkdown_EscapesRawHTMLScript(t *testing.T) {
	// goldmark's default (safe) mode escapes/omits raw HTML; lock that property so
	// a future unsafe renderer swap can't silently ship an XSS vector.
	html := renderMarkdown("A line then <script>alert(1)</script> end.")
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Errorf("raw <script> survived rendering (unsafe HTML):\n%s", html)
	}
}

func TestToPage_CanonicalWired(t *testing.T) {
	p, _, err := parsePost("post.md", []byte(sampleWithSlug))
	if err != nil {
		t.Fatalf("parsePost: %v", err)
	}
	page := p.toPage("https://0ops.sh/")
	if page.CanonicalURL != "https://0ops.sh/blog/ship-with-ai-agent-safely" {
		t.Errorf("page canonical = %q", page.CanonicalURL)
	}
	if strings.TrimSpace(string(page.ZHHTML)) == "" || strings.TrimSpace(string(page.ENHTML)) == "" {
		t.Error("page bilingual html must be non-empty")
	}
}
