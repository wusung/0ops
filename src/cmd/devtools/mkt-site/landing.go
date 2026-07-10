package main

import (
	"os"
	"strings"
)

// Landing is the template data model for the landing page (index.html).
type Landing struct {
	View
	Hero              Hero
	CapabilitiesTitle string
	Capabilities      []Capability
	Posts             []PostSummary
}

// Hero is the top-of-page value proposition + install CTA copy.
type Hero struct {
	ZHHeadline string
	ENHeadline string
	ZHSub      string
	ENSub      string
	ZHCTANote  string
	ENCTANote  string
}

// Capability is one bilingual capability card.
type Capability struct {
	ZHTitle string
	ZHBody  string
	ENTitle string
	ENBody  string
}

// PostSummary is a post entry shown in listings.
type PostSummary struct {
	Slug    string
	Title   string
	Cadence string
}

// parseLanding parses docs/marketing/site/landing.md into a Landing model. The
// file uses front-matter (title/description) plus `key: value` lines grouped
// under `## hero` and `## capabilities` (each `### capability` block). Kept
// deliberately small so the copy stays editable without a data format.
func parseLanding(raw []byte) Landing {
	src := string(raw)
	var l Landing

	if m := frontMatterRe.FindStringSubmatch(src); m != nil {
		for _, line := range strings.Split(m[1], "\n") {
			k, v, ok := kvLine(line)
			if !ok {
				continue
			}
			switch k {
			case "title":
				l.Title = v
			case "description":
				l.Description = v
			}
		}
		src = src[len(m[0]):]
	}

	heroBlock := sectionBody(src, "## hero")
	for _, line := range strings.Split(heroBlock, "\n") {
		k, v, ok := kvLine(line)
		if !ok {
			continue
		}
		switch k {
		case "zh_headline":
			l.Hero.ZHHeadline = v
		case "en_headline":
			l.Hero.ENHeadline = v
		case "zh_sub":
			l.Hero.ZHSub = v
		case "en_sub":
			l.Hero.ENSub = v
		case "zh_cta_note":
			l.Hero.ZHCTANote = v
		case "en_cta_note":
			l.Hero.ENCTANote = v
		}
	}

	capBlock := capabilitiesSection(src)
	l.CapabilitiesTitle = "Why 0ops / 為什麼選 0ops"
	for _, chunk := range strings.Split(capBlock, "### capability") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		var c Capability
		for _, line := range strings.Split(chunk, "\n") {
			k, v, ok := kvLine(line)
			if !ok {
				continue
			}
			switch k {
			case "zh_title":
				c.ZHTitle = v
			case "zh_body":
				c.ZHBody = v
			case "en_title":
				c.ENTitle = v
			case "en_body":
				c.ENBody = v
			}
		}
		if c.ZHTitle != "" || c.ENTitle != "" {
			l.Capabilities = append(l.Capabilities, c)
		}
	}
	return l
}

// capabilitiesSection returns everything under `## capabilities` to end of file.
func capabilitiesSection(src string) string {
	idx := strings.Index(src, "## capabilities")
	if idx < 0 {
		return ""
	}
	rest := src[idx+len("## capabilities"):]
	return strings.TrimSpace(rest)
}

// kvLine parses a `key: value` line, returning ok=false for blanks/headings.
func kvLine(line string) (k, v string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
		return "", "", false
	}
	k, v, ok = strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}

// mustReadFile reads a file or exits with a clear message.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
