package main

import "embed"

// skillsFS holds the capataz-native Claude Code skills shipped with the binary.
// `capataz install` copies them into ~/.claude/skills so agents can run
// /capataz-work on issues from the local .scratch backlog.
//
//go:embed skills
var skillsFS embed.FS
