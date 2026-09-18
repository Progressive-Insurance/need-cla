# Task for the local model

Fix the bugs in this repository using `audit/FIX_PLAN.md` as the task specification. Work from the existing implementation. The original audit baseline is commit `d5221423a4e0476e4fe8379238c86301a6c0033b`.

1. Read the plan and inspect the cited source. Explain any finding you believe is incorrect before excluding it.
2. Implement B01-B18 in the recommended order, preserving the public API where practical. Add deterministic regression tests alongside the fixes.
3. Treat the R-items as explicit scope/maintenance decisions. Address dependencies, supported toolchain/CI, and minor diagnostics; document which other improvements you defer and why. Avoid silently changing what the heuristics mean.
4. Keep failed/unknown checks distinguishable from negative results. Do not swallow errors to make tests pass.
5. Run appropriate tests and vet. Run `python audit/run_probes.py` for additional offline regressions, and race tests where supported. Preserve the fixture behaviors while adapting private helper names and HTTP layouts as necessary. If implementing successful truncated-tree fallback, replace the old failure-path fixture with fallback-aware success/error tests as described in the plan; do not require an error after successful recovery.
6. Do not edit the audit's historical report or baseline logs, weaken the existing tests, skip failing cases, or hardcode fixture strings. If the architecture changes, write equivalent tests separately and explain the adaptation.
7. Finish with the changed files, fixed finding IDs, validation results, remaining limitations, and any public API or Go-version compatibility decisions. Do not claim all bugs are fixed solely because the original suite passes.

No publishing, pushing, or external service changes are required. The objective is a reviewable local patch that demonstrates correct behavior.
