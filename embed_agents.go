package main

import "embed"

// agentsFS holds the capataz-native Claude Code subagent definitions shipped
// with the binary. `capataz install` copies them into ~/.claude/agents so
// orchestration skills can spawn tailored per-role agents (right model + tool
// scope) instead of the default general-purpose type.
//
//go:embed agents
var agentsFS embed.FS
