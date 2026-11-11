---
status: accepted
date: 2026-11-09
---

# Agent context integration: the CLI first, plus a small committed skill and two hooks

Based on [the research note](../research/2026-11-agent-context-integration.md). We adopt only what current Anthropic guidance recommends, and nothing it recommends against.

1. **Agent-grade CLI** (no standing context): example-rich `--help` on every command; `tess` with no arguments prints ~10 lines of orientation; JSON errors carry a stable `code` and a one-line `hint`; short factual notes on stderr only when true (e.g. "3 local changes not yet synced").
2. **Actor detection in the binary**: if `TESS_ACTOR` is unset or empty and `CLAUDECODE=1`, `changed_by` is `claude:<first 8 of CLAUDE_CODE_SESSION_ID>`. Supersedes the plugin-set identity in [ADR-0013](0013-field-level-event-log.md).
3. **One committed skill**, `.claude/skills/tess/SKILL.md`: ~300-character description, 40–60 line body (work loop, JSON fields, "run `tess sync` when you finish a unit of work"), `allowed-tools: Bash(tess *)`.
4. **SessionStart hook** in `.claude/settings.json`: on startup/resume runs `tess sync` with a short timeout and prints 3–6 lines of facts, never instructions (command-style injected text can trip prompt-injection defences); after compaction, prints the facts again without syncing.
5. **SessionEnd hook**: starts a detached `tess sync` and exits (SessionEnd gets ~1.5 s). Best effort; the skill's "sync when done" is the main path.
6. **`tess setup claude [--check|--remove]`** writes 3–5 and a `Bash(tess *)` allow rule. Re-running is a no-op: it never duplicates hooks or rules, and changes no file that is already current.

Standing context is ~150–250 tokens, against 1–2k for the old `bd prime`.

## Not used (recommended against or not useful for tess)

An MCP server (a CLI is cheaper and pipeable); a `CLAUDE.md` block; output styles; a subagent; Stop, UserPromptSubmit, and PreCompact hooks. A plugin may come later as a second install channel for local-only use; it doesn't load in cloud sessions, which are deferred anyway ([ADR-0004](0004-sync-over-unchecked-out-git-ref.md)).

## Scope notes

- Installing the `tess` binary on machines is out of scope; the user handles it.
- Hooks fail quietly: if `tess` is missing, or sync is slow or offline, the session starts normally with at most one line saying so.
- No `AGENTS.md` mention for other agents.
