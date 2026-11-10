---
status: accepted
date: 2026-11-02
---

# Rewrite with a narrow scope instead of refactoring the beads fork

About half of the beads fork's ~119K non-test Go lines is machinery this tool doesn't need: Gastown features, JSONL/git sync, the daemon and RPC layer, and `bd doctor`. Nearly every bug found during evaluation lives in those areas. Refactoring would mean keeping that machinery working while changing it, so we're rewriting and carrying over only the sound half (data model, ID generation, readiness rules, CLI shape). Out of scope: Dolt, Gastown and multi-agent orchestration, a daemon, git hooks and merge drivers, multi-repo routing, compaction.

Evidence: `REWRITE_HANDOFF.md` and, in the old repo, `docs/dev-notes/STORAGE_EVALUATION.md` and `BACKEND_PLUGIN_EVALUATION.md`.
