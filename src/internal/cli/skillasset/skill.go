// Package skillasset embeds the 0ops Claude Code skill so the CLI can install
// it on an end user's machine without them cloning this repo.
//
// spec: docs/features/agent-skill/skill-distribution-spec.md § 3
//
// The canonical file is the repo-level .claude/skills/0ops/SKILL.md; this copy
// exists only because go:embed cannot reach outside its own package directory.
// TestEmbeddedSkillMatchesRepoSkill keeps the two byte-identical, and
// `./manage.sh skill-sync` refreshes this one.
package skillasset

import _ "embed"

//go:embed SKILL.md
var skillMarkdown string

// Markdown returns the skill document shipped inside the binary.
func Markdown() string { return skillMarkdown }
