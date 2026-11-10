---
status: accepted
date: 2026-11-04
---

# The CLI is compatible with `bd` in spirit, not in syntax

The new CLI keeps `bd`'s concepts and capabilities but is free to change command names, flags, human output, and `--json` shapes. Matching `bd` exactly would preserve old agent instructions, but it would also carry its structural quirks into a tool that exists to be simpler. Agent instructions get rewritten instead, as part of agent context integration. The old `.beads/issues.jsonl` import is unaffected: data compatibility stays.
