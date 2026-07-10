// Package main implements mkt-site, a static-site renderer for the 0ops
// marketing landing page + build-in-public blog. It reads bilingual posts from
// docs/marketing/posts/*.md (front-matter + `## 中文` / `## English` sections),
// renders them to self-contained static HTML via html/template, and writes the
// result to docs/marketing/site/dist/. render.go holds the pure, unit-tested
// functions; main.go is the filesystem entrypoint.
//
// See docs/features/marketing-landing-site/spec.md (§3 architecture, §4
// canonical_url, §5 gate S1–S6).
package main

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
)

// Post is a parsed marketing post: front-matter metadata plus its two
// language sections as raw markdown.
type Post struct {
	Slug       string
	Cadence    string
	Source     string
	Title      string // first zh heading, used for listings
	ZHMarkdown string
	ENMarkdown string
}

// View holds the layout-level fields every page shares. RootPath is the
// relative path from the current page back to the site root (e.g. "" for
// index.html, "../" for blog/index.html, "../" for blog/<slug>.html), and
// AssetPath points at the assets/ directory the same way.
type View struct {
	Site         SiteMeta
	Title        string
	Description  string
	CanonicalURL string
	RootPath     string
	AssetPath    string
}

// Page is the template data model for a rendered blog post.
type Page struct {
	View
	Slug   string
	ZHHTML template.HTML
	ENHTML template.HTML
}

// SiteMeta carries site-wide values shared across every page.
type SiteMeta struct {
	Name       string
	BaseURL    string
	Tagline    string
	InstallCmd string
	CreateCmd  string
}

var (
	frontMatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)
	datePrefixRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)
	fileExtRe     = regexp.MustCompile(`\.md$`)
	h1Re          = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
)

// parsePost parses a post's bytes into a Post. filename is used to derive a
// slug when front-matter has none (a non-empty warning is returned in that
// case). It errors only when a required bilingual section is missing/empty.
func parsePost(filename string, raw []byte) (Post, string, error) {
	src := string(raw)
	var p Post
	var warn string

	if m := frontMatterRe.FindStringSubmatch(src); m != nil {
		parseFrontMatter(m[1], &p)
		src = src[len(m[0]):]
	}

	zh, en, err := splitBilingual(src)
	if err != nil {
		return p, warn, err
	}
	p.ZHMarkdown = zh
	p.ENMarkdown = en

	if hm := h1Re.FindStringSubmatch(zh); hm != nil {
		p.Title = strings.TrimSpace(hm[1])
	}
	if p.Title == "" {
		if hm := h1Re.FindStringSubmatch(en); hm != nil {
			p.Title = strings.TrimSpace(hm[1])
		}
	}

	if p.Slug == "" {
		p.Slug = slugFromFilename(filename)
		warn = fmt.Sprintf("post %q has no front-matter slug; derived %q from filename", filename, p.Slug)
	}
	return p, warn, nil
}

// parseFrontMatter reads the small subset of YAML we need (flat `key: value`
// lines). We intentionally avoid a full YAML dependency here.
func parseFrontMatter(fm string, p *Post) {
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		switch k {
		case "slug":
			p.Slug = v
		case "cadence":
			p.Cadence = v
		case "source":
			p.Source = v
		}
	}
}

// splitBilingual extracts the `## 中文` and `## English` sections. Both must be
// present and non-empty.
func splitBilingual(body string) (zh, en string, err error) {
	zh = sectionBody(body, "## 中文")
	en = sectionBody(body, "## English")
	if strings.TrimSpace(zh) == "" {
		return "", "", fmt.Errorf("missing or empty `## 中文` section")
	}
	if strings.TrimSpace(en) == "" {
		return "", "", fmt.Errorf("missing or empty `## English` section")
	}
	return zh, en, nil
}

// sectionBody returns the markdown between the given `## Heading` and the next
// top-level `## ` heading (or end of document).
func sectionBody(body, heading string) string {
	idx := strings.Index(body, heading)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(heading):]
	// Drop to the end of the heading line.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = ""
	}
	// Cut at the next `## ` section header at line start.
	lines := strings.Split(rest, "\n")
	var out []string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// slugFromFilename derives a slug from a post filename by stripping a leading
// date prefix and the .md extension.
func slugFromFilename(filename string) string {
	base := filename
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = fileExtRe.ReplaceAllString(base, "")
	base = datePrefixRe.ReplaceAllString(base, "")
	return base
}

// canonicalURL builds the canonical blog URL for a slug, normalizing trailing
// slashes on the base so `<base>/blog/<slug>` never doubles up.
func canonicalURL(base, slug string) string {
	base = strings.TrimRight(base, "/")
	return base + "/blog/" + slug
}

// renderMarkdown converts a markdown fragment to HTML using goldmark.
func renderMarkdown(md string) string {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(md), &buf); err != nil {
		// goldmark.Convert only fails on writer errors; a bytes.Buffer never
		// errors, so this is unreachable in practice. Fall back to escaped text.
		return template.HTMLEscapeString(md)
	}
	return buf.String()
}

// toPage builds the template data model for a post, wiring the canonical URL
// from base + slug and rendering both language sections to HTML.
func (p Post) toPage(base string) Page {
	return Page{
		View: View{
			Site:         SiteMeta{Name: "0ops", BaseURL: strings.TrimRight(base, "/")},
			Title:        p.Title,
			CanonicalURL: canonicalURL(base, p.Slug),
			RootPath:     "../",
			AssetPath:    "../assets/",
		},
		Slug:   p.Slug,
		ZHHTML: template.HTML(renderMarkdown(p.ZHMarkdown)),
		ENHTML: template.HTML(renderMarkdown(p.ENMarkdown)),
	}
}

// renderTemplate executes the named template against data and returns the HTML.
func renderTemplate(t *template.Template, name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
