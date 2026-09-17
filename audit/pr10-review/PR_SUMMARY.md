# Attempt 1: Qwen independently discovers and fixes bugs

This PR is the first attempt in the comparison: **Qwen working without the supplied audit plan**. PR #11 is the separate attempt where Qwen implemented the full plan in one batch. The step-by-step attempt will be evaluated separately.

Reviewed PR #10 at `325201f1d0f7c7f05463f08383325e9c05cb6501` against main at `d5221423a4e0476e4fe8379238c86301a6c0033b`.

## What improved

Qwen independently found several real problems: a panic after repository API transport failures, broken CLA acronym matching caused by Go string escaping, case-sensitive known-owner detection, and spurious errors for repositories without workflows. Those fixes pass targeted review tests. Missing CLI arguments now produce usage and exit 2; agreement text matching, some PR-label cases, and diagnostic wording also improve. Six library tests were added without weakening existing assertions.

## What failed or remains incomplete

- **Failed scans can still look successful.** A PR-list API failure still produces exit 0 and a statement that the repository does not need a CLA.
- **Four regressions were demonstrated against main:** a CLA workflow after the first 50 entries is silently missed; a differently cased directory can hide the actual workflow directory; raising the quota threshold rejects scans that previously succeeded; malformed blob metadata can become a clean negative. The last is a defensive malformed-response case.
- **New pagination is incorrect:** ten requests visit pages `1, 1, 2, ... 9`, so the first page is repeated and page 10 is never inspected. Its test only counts requests.
- **Detection remains incomplete:** workflow regex matching confuses comments, shell text, and non-workflow files with executable actions; quoted/current action references are missed; label suffixes such as `cla: yes-extra` still match.
- **Other original issues remain:** branch URL escaping, inspectable error chains, authentication classification, extra CLI arguments, request deadlines, `.clabot` entry types, documentation, and maintenance gaps.
- **The explanation overclaims one fix.** Baseline already returned base64 decoding errors. The PR's statement that those errors were discarded is incorrect. Its description also says the 100-PR sample was unchanged, contradicting the final implementation.

## Validation and rating

The exact PR commit passes its existing short-mode tests, build, and vet. Its own tests cover **31.8% of library statements and 0% of CLI statements**; all four Check/Detail entry points remain uncovered. The original audit suite yields **5 passing and 15 failing behavioral groups**, with partial improvements inside two failing groups. Independent testing confirms the four introduced regressions by running identical cases against main and PR10.

**Overall: 5/10.** Qwen shows useful independent discovery and can repair local defects. It struggles with API assumptions, incomplete-result handling, and tests that challenge its implementation. The delivered patch needs substantial review and follow-up before acceptance.

PR #11 received **7/10** and passes all 20 original groups, but it had the plan and visible fixtures and still has independent review findings. These are evaluations of two differently supported attempts, not a controlled model benchmark. PR10 also avoids PR11's help-exit regression.

The [full review](REVIEW.md), reproducible fixtures, and captured logs are included in `audit/pr10-review/` as a separate review commit. The evaluated implementation remains pinned to `325201f1`; the review does not alter its production code or model-written tests.
