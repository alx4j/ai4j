---
name: repository-review
description: Review a repository change against its stated requirements and report only evidence-backed findings.
---

# Repository review

Review the requested change against the explicit requirements and acceptance criteria.

1. Read the changed code and the tests that exercise it.
2. Prioritize correctness, data loss, credential exposure, and destructive behavior.
3. Report each actionable finding with a precise file location and a concrete failure scenario.
4. Put adjacent improvements in a backlog instead of expanding the current task.

Use [review checklist](references/checklist.md) for the final pass.

When the user asks to check the current Git diff, `scripts/check-diff.sh` provides an optional whitespace check. AI4J only packages this script and never runs it.
