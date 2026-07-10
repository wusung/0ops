package main

import (
	"flag"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mkt-site renders the 0ops marketing landing page + build-in-public blog from
// docs/marketing/posts/*.md into a self-contained static site under -out.
//
// Usage:
//
//	go run ./cmd/devtools/mkt-site \
//	  -posts docs/marketing/posts \
//	  -templates docs/marketing/site/templates \
//	  -assets docs/marketing/site/assets \
//	  -landing docs/marketing/site/landing.md \
//	  -out docs/marketing/site/dist \
//	  -base-url https://0ops.sh
func main() {
	// Default paths are relative to the repo root; manage.sh invokes this from
	// src/, so resolve them against the repo root (parent of src/).
	repoRoot := defaultRepoRoot()
	var (
		postsDir  = flag.String("posts", filepath.Join(repoRoot, "docs/marketing/posts"), "directory of source posts")
		tmplDir   = flag.String("templates", filepath.Join(repoRoot, "docs/marketing/site/templates"), "directory of html templates")
		assetsDir = flag.String("assets", filepath.Join(repoRoot, "docs/marketing/site/assets"), "directory of static assets")
		landingMD = flag.String("landing", filepath.Join(repoRoot, "docs/marketing/site/landing.md"), "landing copy source")
		outDir    = flag.String("out", filepath.Join(repoRoot, "docs/marketing/site/dist"), "output directory")
		baseURL   = flag.String("base-url", "https://0ops.sh", "canonical base url")
	)
	flag.Parse()

	if err := run(config{
		postsDir:  *postsDir,
		tmplDir:   *tmplDir,
		assetsDir: *assetsDir,
		landingMD: *landingMD,
		outDir:    *outDir,
		baseURL:   strings.TrimRight(*baseURL, "/"),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "mkt-site:", err)
		os.Exit(1)
	}
}

type config struct {
	postsDir, tmplDir, assetsDir, landingMD, outDir, baseURL string
}

func run(c config) error {
	posts, warnings, err := loadPosts(c.postsDir)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if len(posts) == 0 {
		return fmt.Errorf("no posts found under %s", c.postsDir)
	}

	landingTmpl, err := parsePageTemplate(c.tmplDir, "landing")
	if err != nil {
		return err
	}
	indexTmpl, err := parsePageTemplate(c.tmplDir, "blog-index")
	if err != nil {
		return err
	}
	postTmpl, err := parsePageTemplate(c.tmplDir, "blog-post")
	if err != nil {
		return err
	}

	// Fresh output tree.
	if err := os.RemoveAll(c.outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(c.outDir, "blog"), 0o755); err != nil {
		return err
	}

	summaries := postSummaries(posts)

	// Landing (index.html).
	landing := buildLanding(c.landingMD, c.baseURL, summaries)
	if err := writePage(filepath.Join(c.outDir, "index.html"), landingTmpl, "landing", landing); err != nil {
		return err
	}

	// Blog index (blog/index.html).
	idx := struct {
		View
		Posts []PostSummary
	}{
		View:  View{Site: SiteMeta{Name: "0ops", BaseURL: c.baseURL}, Title: "Blog", CanonicalURL: c.baseURL + "/blog/", RootPath: "../", AssetPath: "../assets/"},
		Posts: summaries,
	}
	if err := writePage(filepath.Join(c.outDir, "blog", "index.html"), indexTmpl, "blog-index", idx); err != nil {
		return err
	}

	// Each post (blog/<slug>.html). Sanitize the slug so a malicious or
	// malformed slug (e.g. `../evil`, `a/b`) can never escape blog/.
	for _, p := range posts {
		slug, err := safeSlug(p.Slug)
		if err != nil {
			return fmt.Errorf("post %q: %w", p.Title, err)
		}
		page := p.toPage(c.baseURL)
		out := filepath.Join(c.outDir, "blog", slug+".html")
		if err := writePage(out, postTmpl, "blog-post", page); err != nil {
			return err
		}
	}

	// Copy assets.
	if err := copyDir(c.assetsDir, filepath.Join(c.outDir, "assets")); err != nil {
		return err
	}

	fmt.Printf("mkt-site: built %d posts → %s\n", len(posts), c.outDir)
	return nil
}

// loadPosts reads and parses every *.md post, sorted by filename descending
// (newest first, since filenames are date-prefixed).
func loadPosts(dir string) ([]Post, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	var posts []Post
	var warnings []string
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, err
		}
		p, warn, err := parsePost(name, raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		posts = append(posts, p)
	}
	return posts, warnings, nil
}

func postSummaries(posts []Post) []PostSummary {
	out := make([]PostSummary, 0, len(posts))
	for _, p := range posts {
		out = append(out, PostSummary{Slug: p.Slug, Title: p.Title, Cadence: p.Cadence})
	}
	return out
}

func buildLanding(landingMD, baseURL string, posts []PostSummary) Landing {
	l := Landing{}
	if raw, err := readFile(landingMD); err == nil {
		l = parseLanding(raw)
	}
	l.Site = SiteMeta{Name: "0ops", BaseURL: baseURL}
	l.CanonicalURL = baseURL + "/"
	l.RootPath = ""
	l.AssetPath = "assets/"
	l.Posts = posts
	return l
}

// parsePageTemplate loads layout.html.tmpl plus one page template into an
// isolated set, so the shared "content"/"layout" definitions never collide
// across page types.
func parsePageTemplate(tmplDir, page string) (*template.Template, error) {
	files := []string{
		filepath.Join(tmplDir, "layout.html.tmpl"),
		filepath.Join(tmplDir, page+".html.tmpl"),
	}
	return template.New(page).ParseFiles(files...)
}

func writePage(path string, tmpl *template.Template, name string, data any) error {
	html, err := renderTemplate(tmpl, name, data)
	if err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	if strings.Contains(html, "{{") {
		return fmt.Errorf("render %s: output still contains a {{ placeholder", name)
	}
	return os.WriteFile(path, []byte(html), 0o644)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// defaultRepoRoot resolves the repo root assuming the binary is run from src/
// (as manage.sh does). It falls back to the current directory.
func defaultRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	// manage.sh runs `cd src && go run ...`, so wd is <repo>/src.
	if filepath.Base(wd) == "src" {
		return filepath.Dir(wd)
	}
	return wd
}
