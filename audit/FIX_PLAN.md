# Repository bug audit and fix plan

Audit date: 2026-09-16. Baseline: `d5221423a4e0476e4fe8379238c86301a6c0033b`.

The original working tree was clean. All production Go files, existing tests, manifests, documentation, and CI configuration were reviewed. No original files were changed; the audit artifacts are under `audit/`. The plan contains 18 prioritized fix items (B01-B18) and 16 additional risk, maintenance, or scope items (R01-R16).

This is a comprehensive review of this small repository, not a guarantee that every possible bug has been found. The findings below distinguish reproduced behavior, inspection findings, and scope or policy decisions. In particular, this library advertises heuristics; a missed detection method is not automatically a violation of its advertised behavior.

## Evidence and how to reproduce

The repository's existing checks passed:

- `go test -short -coverprofile=<temporary-path> ./...`: PASS. Library coverage 14.7%; CLI coverage 0.0%; combined coverage 12.2%. All checker and public API functions had 0% coverage.
- `go vet ./...`: PASS.
- `go test -race -short ./...`: could not run: the installed Windows environment has cgo disabled. This is an audit limitation, not a repository defect.

Toolchain: `go1.25.12 windows/amd64`. A temporary writable `GOCACHE` was necessary because the default cache was outside the sandbox. Existing module downloads were available. The normal network-dependent example was not run against live GitHub; repository behavior was exercised with deterministic local transports instead.

Run the added offline probes from the repository root:

```text
python audit/run_probes.py
```

The script copies the working source into a temporary directory, injects the two `.go.txt` test fixtures there, and runs only `TestAudit*` tests. It never modifies the original Go files. It disables module/toolchain downloads; populate the module cache separately if needed. On the audited baseline, 20 top-level behavioral test groups fail, while positive-control subcases and the subprocess helper pass. These are test groups, not 20 distinct bug claims. See `probe-results.txt` for the captured baseline output.

The tests are illustrative regression cases, not a complete benchmark or a requirement to retain private implementation details. If a model refactors the checker, the evaluator should port the fixtures while preserving their behavioral assertions. The truncated-tree sentinel fixture covers the existing failure path; a successful new fallback should instead be tested for successful discovery, with sentinel assertions reserved for incomplete fallback. Do not weaken assertions merely to obtain a green result.

Severity: **P1** = crash or substantially misleading central result; **P2** = incorrect behavior for supported input or important reliability failure; **P3** = narrower correctness issue. These priorities describe this project, not CVSS security scores.

## Confirmed defects and validation gaps

### B01 — Repository metadata errors are discarded, including a panic path — P1

**Location:** `cla.go:45-54`.

`Repositories.Get` returns an error that is assigned to `_`. A network/transport failure can return a nil response, which is immediately dereferenced as `resp.StatusCode`. The offline transport failure probe panicked. An HTTP 500 does have a response, but the code proceeds with a nil repository; the SDK's nil-safe `GetDefaultBranch()` returns an empty string. The subsequent tree request fails and replaces the original explanation with an unrelated error. JSON decoding failures follow the same discarded-error path.

**Fix:** inspect the error before reading response or repository fields. Preserve its cause, classify only recognized cases, and stop dependent requests when metadata retrieval failed. Validate required metadata before using it.

**Acceptance:** transport failure, context cancellation during metadata retrieval, malformed metadata JSON, and HTTP 500 return errors without panic; `errors.Is`/`errors.As` reach the original cause; no tree request occurs after failure. A valid 404 still exposes `ErrNotFound`. Probes: `TestAuditRepositoryTransportFailureReturnsErrorWithoutPanic`, `TestAuditRepositoryHTTPFailureStopsAndPreservesCause`.

### B02 — Authentication and permission failures are misclassified — P2

**Location:** `cla.go:36-38,49-50`.

Every repository HTTP 403 becomes `ErrInvalidToken`, losing the distinction between invalid credentials, insufficient permission, and rate limiting. Conversely, a 401 from the initial rate-limit request bypasses that sentinel entirely. Both inconsistencies are reproduced by the fixtures. GitHub documents rate-limit responses as 403 or 429 and separately describes permission failures. [GitHub troubleshooting](https://docs.github.com/en/rest/using-the-rest-api/troubleshooting-the-rest-api).

**Fix:** use consistent error classification across requests. Preserve typed GitHub failures and rate-limit/reset information. Expose invalid credentials for actual authentication failures; do not equate all 403s with invalid credentials. Keep 404 ambiguous where GitHub deliberately hides private resources.

**Acceptance:** 401 at startup and during later requests has the chosen consistent authentication behavior; permission 403, secondary-rate-limit 403, and primary-rate-limit/429 remain distinguishable. Probes: `TestAuditForbiddenIsNotAutomaticallyInvalidToken`, `TestAuditRateLimitUnauthorizedExposesInvalidToken`.

### B03 — Repositories without workflows produce a spurious error — P2

**Location:** `checks.go:187-197`, `checks.go:256-272`.

On a complete tree, an absent `.github/workflows` returns `(nil, nil)`. The action checker does not stop. `workflowsEntry.GetSHA()` is nil-safe and returns `""`, so the code requests `/git/trees/` and converts the resulting 404 into `ActionErr`. This is **not** a nil-pointer panic; the defect is an unnecessary invalid request and a false scan error for an ordinary repository.

**Fix:** an absent workflow directory in a complete tree should return `(false, nil)` without making a request. Treat a non-directory entry at that path according to an explicit policy; it cannot contain active workflow files.

**Acceptance:** complete tree with no workflow directory yields no `ActionErr` and no follow-up workflow request; an incomplete tree remains distinguishable from confirmed absence. Probe: `TestAuditMissingWorkflowsIsNegativeResultWithoutRequest`.

### B04 — The CLA acronym matcher contains backspaces — P1

**Location:** `cla.go:18`, `checks.go:307-320`.

The interpreted Go string `"\bCLA\b"` contains backspace characters, not regex word boundaries. `Please sign the CLA.`, `CLA`, and `Sign our **CLA**.` all return false. Only the expanded-phrase matcher can rescue such a document if that exact phrase also appears.

**Fix:** use a raw regex string or escape the backslashes correctly. Preserve word boundaries so unrelated words do not match.

**Acceptance:** acronym-only examples and `Contributor License Agreement` match; `CLASS` and `CLAMP` do not. Decide lowercase behavior separately. Probe: `TestAuditStandaloneCLAAcronymMatches`.

### B05 — PR label regex accepts unrelated labels — P2

**Location:** `cla.go:20`, `checks.go:81-112`.

`[yes|no]` is a character class, not an alternative between two words. The expression also lacks whole-label anchors. Probes show `cla: yellow`, `cla: nope`, `cla: n`, and `cla: |` all count as CLA evidence. Prefixes such as `not-cla: yes` can also match.

**Fix:** match the entire intended label with actual alternatives. Document whether case and surrounding whitespace are normalized.

**Acceptance:** `cla: yes` and `cla: no` match; unrelated status words, partial words, literal pipes, extra prefixes, and suffixes do not. Probe: `TestAuditPRLabelMatcherRequiresCompleteYesOrNo`.

### B06 — Known-owner detection is case-sensitive — P2

**Location:** `checks.go:60-66`, `known.go:12-42`.

`google` matches, but `Google`, `GOOGLE`, and the repository's own `Progressive-Insurance` capitalization fail. GitHub's owner parameter is case insensitive. [Repository endpoint](https://docs.github.com/en/rest/repos/repos#get-a-repository).

**Fix:** compare case insensitively or normalize the owner once. Consider using canonical metadata for redirects as a separate improvement.

**Acceptance:** capitalization does not change `Details.Known`; unrelated owners remain false. Probe: `TestAuditKnownOwnerIsCaseInsensitive`.

### B07 — Workflow regex confuses source text with executable YAML — P2

**Location:** `cla.go:19`, `checks.go:207-214`.

The regex misses quoted `uses` values but accepts commented-out steps, strings inside `run` blocks, and an unrelated repository named `cla-assistant/github-action-unrelated`. These false positives and negatives are reproduced.

**Fix:** parse YAML and inspect actual action-step `uses` values. Compare the action repository exactly after separating the reference. Avoid scanning comments, shell text, and unrelated keys. Preserve per-file parse errors so malformed files are not reported as clean negatives.

**Acceptance:** quoted/unquoted values and quoted keys work; comments, examples in strings, different action repositories, and malformed files have the intended results. Include both `.yml` and `.yaml`. Probe: `TestAuditActionDetectionRespectsYAMLSyntaxAndExactRepository`. [Workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax).

### B08 — The current CLA Assistant action name is missed — P2

**Location:** `cla.go:19`.

Only `cla-assistant/github-action` is recognized. The upstream project's current installation example uses `contributor-assistant/github-action`, which the existing code rejects. [Upstream action](https://github.com/contributor-assistant/github-action#configure-contributor-license-agreement-within-two-minutes).

**Fix:** recognize the current repository and retain the legacy alias deliberately. Handle the DCO-only configuration separately under R05.

**Acceptance:** both action identities work with valid refs; similarly named repositories do not. Covered by the `current_owner_alias` subcase of the workflow probe.

### B09 — Non-workflow entries are treated as workflow blobs — P2

**Location:** `checks.go:200-207`.

Every directory entry is downloaded as a blob, with no extension/type check. A `.github/workflows/README.md` containing an example sets `Action=true`; a child directory causes a blob lookup on a tree SHA and a spurious error. Disabled backups such as `cla.yml.bak` also qualify under the current code.

**Fix:** inspect eligible workflow files only: GitHub uses `.yml`/`.yaml` files in the workflow directory. Skip unrelated files, directories, and gitlinks without blob requests; define symlink handling. [Workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax).

**Acceptance:** both YAML extensions are scanned; README files, backups, and directories are ignored without request errors. Probes: `TestAuditNonWorkflowFilesCannotEstablishAction`, `TestAuditWorkflowSubdirectoriesAreSkipped`.

### B10 — A directory named `.clabot` is reported as a config file — P3

**Location:** `checks.go:127-132`.

The method checks only that a path exists. A tree entry of type `tree` or `commit` is not the root configuration file documented by the library, but currently produces `BotFile=true`.

**Fix:** require an eligible file entry. Make a deliberate decision about symlinks rather than assuming every blob is a regular file.

**Acceptance:** a regular `.clabot` file is positive; absent path, directory, and submodule are negative; incomplete-tree uncertainty is preserved. Probe: `TestAuditCLABotDirectoryDoesNotCountAsConfigFile`.

### B11 — The fixed quota threshold rejects feasible scans and cannot guarantee completion — P2

**Location:** `cla.go:36-42`, `checks.go:200-215`.

The probe supplies nine remaining requests and a repository needing four requests after the rate lookup. The scan refuses before checking anything. Conversely, ten available requests need not suffice when there are many workflow blobs. With both documentation files present, the normal scan uses approximately `6 + workflow-file-count` metered requests, excluding the initial rate endpoint and any retries/fallbacks.

**Fix:** remove the arbitrary requirement or budget according to actual work. Preserve real rate-limit errors and partial evidence. Do not infer that a successful preflight reserves quota shared with other callers. GitHub recommends using response rate-limit headers when possible. [Rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).

**Acceptance:** a small scan can finish below ten remaining calls; actual quota exhaustion is returned with useful typed context; a large workflow directory cannot be reported as a complete negative after only part of it was inspected. Probe: `TestAuditSmallScanAcceptsSufficientQuotaBelowTen`.

### B12 — Valid default branch names are misinterpreted as URL syntax — P2

**Location:** `cla.go:52-54`, `checks.go:37`; pinned SDK `github/git_trees.go:97-100`.

The branch is passed into an SDK method that interpolates it into a URL without escaping. `git check-ref-format --branch` accepts `release#1` and `release%stable`. Using the actual pinned SDK, `release#1` requests branch `release` and moves the recursive query into a fragment; `release%stable` fails with an invalid URL escape; `release%61` becomes `releasea`. The first and third can silently inspect the wrong branch if it exists.

**Fix:** encode the branch as path data or resolve it safely to an immutable tree SHA. Account for the selected SDK's escaping behavior; do not double-escape after a dependency upgrade.

**Acceptance:** `main`, `release/foo`, `release#1`, `release%stable`, and `release%61` preserve branch identity, have no URL fragment, and retain `recursive=1`. Include competing similarly named branches to catch silent misselection. Probe: `TestAuditDefaultBranchIsEncodedAsPathData`.

### B13 — Wrapping and aggregation destroy inspectable error causes — P2

**Location:** `checks.go:40,90,150,182,191,222,279,288,299`, `errors.go:23-79`.

Underlying errors are formatted with `%v`, truncation sentinels are replaced with new string-only errors, workflow errors are concatenated, and `Errors` offers no traversal. The probes show `errors.Is` cannot find `ErrTruncatedTree` or a transport cause, and `errors.As` cannot reach a contained GitHub failure. Callers cannot reliably distinguish cancellation, rate limiting, permissions, and incomplete data without parsing text or manually inspecting specific fields.

**Fix:** preserve causes at every wrapping layer, including the per-workflow collection and public aggregate. Retain descriptive context and the existing per-check fields. If retaining Go 1.18 compatibility, implement compatible `Is`/`As` delegation or a suitable chain; modern multi-error traversal and `errors.Join` require a later Go version.

**Acceptance:** wrapped cancellation/deadline errors and GitHub errors remain inspectable through the final returned error. A successful aggregate returns nil. Probes: `TestAuditTruncatedTreeCauseRemainsInspectable`, `TestAuditTreeFetchPreservesUnderlyingError`, `TestAuditAggregateSupportsErrorsIsAndAs`.

### B14 — CLI does not validate its required arguments — P2

**Location:** `cmd/need-cla/main.go:33,46-49`.

Zero arguments, one argument, or extra arguments all reach the network. Missing values become empty owner/repo strings; extras are ignored. This can waste quota and return unrelated authentication/network errors instead of usage help. All three invalid argument counts are reproduced without accessing GitHub.

**Fix:** validate exactly two nonempty positional arguments before constructing or using the client. Return a documented usage exit code and direct usage errors to stderr. Keep token/environment precedence intact.

**Acceptance:** invalid input fails locally without any request; valid `owner repo`, `-h`, token flag, and `CLA_TOKEN` continue to work. Probe: `TestAuditCLIRejectsInvalidArgumentsBeforeNetwork`.

### B15 — CLI reports incomplete scans as successful negative findings — P1

**Location:** `cmd/need-cla/main.go:50-69`.

For `*needcla.Errors`, the CLI prints the error and continues with boolean output, then exits 0. With only a PR API failure, the probe prints both `DOES NOT need a CLA` and `PRs DO NOT have "cla" tags`. Neither negative was established. The error also goes to stdout, making result/error separation difficult for automation.

**Fix:** distinguish positive, negative, and unknown/failed checks in presentation. Retain valid positive evidence even if another check fails. Use stderr for diagnostics and document an exit-status contract that makes incomplete scans observable. A scan with no positive evidence and any failed heuristic must not be presented as a completed negative scan.

**Acceptance:** clean positive, clean negative, partial positive, partial unknown, and fatal startup failure have explicit output/exit behavior. Probe: `TestAuditCLIPartialFailureIsNotSuccessfulNegative`.

### B16 — CLI has no request deadline — P2

**Location:** `cmd/need-cla/main.go:34-49`; `cla.go:31-32`.

The CLI uses default HTTP clients and calls `Detail`, which uses `context.Background()`. No overall deadline is established. The probe confirms requests have no context deadline. A connection that remains open without completing its response can keep the command waiting indefinitely; the public context-aware API already provides a way to prevent this.

**Fix:** give the CLI a reasonable, preferably configurable, timeout and pass its context through `DetailWithContext`; support cancellation where practical. Library callers should retain control of their own context/client policies.

**Acceptance:** stalled response headers and bodies, cancellation, and deadline expiry terminate within a bounded test time with nonzero status and useful diagnostics. The fixture checks the proposed context-deadline policy, not an actual hang. Probe: `TestAuditCLIRequestsHaveDeadline`.

### B17 — Published usage instructions are broken — P2

**Location:** `README.md:39`, `cmd/need-cla/README.md:10`.

The library example says its client comes from `go-github/v38`; the public signature uses `go-github/v43`. Go treats those package types as distinct, so following the import guidance does not compile. The CLI clone command `git clone github.com/Progressive/need-cla` lacks a URL scheme and uses a different owner. It was reproduced as an immediate local repository-not-found failure.

**Fix:** provide an actually compiling example using the selected SDK major version and the repository's correct clone URL. Keep requirements aligned with any chosen Go baseline update.

**Acceptance:** compile the documented example in isolation; verify the corrected clone URL identifies the intended repository. No credentials should be necessary to follow public build instructions.

### B18 — Existing tests and CI provide little protection for the actual checker — P2 validation gap

**Location:** `cla_test.go:17-30`, `details_test.go:20-45`, `.github/workflows/test.yml:11` and the absence of checker/CLI tests.

The sole example that calls `Check` prints its expected answer without exercising the library in short mode. CI uses short mode. Normal mode relies on live GitHub and ignores the returned error. Other tests mostly cover merge/format helpers; the randomized `Required` test does not deterministically prove each flag independently works or cover all-true input. The baseline can therefore pass while all core checker functions remain untested.

**Fix:** add deterministic mocked tests for the public API, each heuristic, CLI behavior, cancellation, and error paths. Replace the random flag test with deterministic single-flag cases or all 64 combinations. Make the example offline and useful, or clearly separate an optional live integration test. Keep the audit's deliberately failing fixture files separate until translated into actual regression tests alongside fixes.

**Acceptance:** routine tests work offline and fail when the reproduced bugs are reintroduced. Exercise all `Errors` fields, merging, `ErrOrNil`, partial results, malformed base64, missing docs, API failures, and all details flags. Run tests and vet in CI; run race checks in a compatible environment.

## Potential issues and explicit scope decisions

These are not included as additional proven runtime bugs. They deserve a decision or targeted validation, rather than blind changes by the local model.

| ID | Evidence / issue | Recommended decision or validation |
|---|---|---|
| R01 | `checks.go:256-270` explicitly leaves truncated recursive trees unsupported. Missing paths produce incomplete results. The workflow subtree's own `Truncated` flag is not checked at `checks.go:200-225`, so an incomplete list there could look complete. | Prefer targeted nonrecursive traversal or Contents requests with clear absence/error semantics. Test truncated root/subtree, target found by fallback, true absence, failed fallback, and cancellation. GitHub documents limits and fallback guidance in the [trees API](https://docs.github.com/en/rest/git/trees#get-a-tree). |
| R02 | `cla.go:54-57` makes every heuristic depend on successfully loading the Git tree. Empty repositories/no default-branch commit and tree permission failures prevent even known-owner and PR checks. | Decide whether these are fatal or partial scans. If partial, preserve independent evidence and explicit unavailability of file checks; distinguish an empty repository from a transient server failure. |
| R03 | `checks.go:148,180,263` checks only exact root `CONTRIBUTING.md` and `README.md`. Alternate locations, case/extensions, and inherited community files are not considered. | Expand discovery only with documented precedence and a request budget. Test `.github/`, `docs/`, lowercase/alternate formats, and `owner/.github` defaults. See [contribution guidelines](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/setting-guidelines-for-repository-contributors), [README discovery](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-readmes), and [community defaults](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/creating-a-default-community-health-file). |
| R04 | Exact text heuristics can miss case/whitespace variations and match negations, examples, or unrelated mentions: `No Contributor License Agreement is required` is still positive. | Keep the result explicitly probabilistic. Decide what textual evidence counts; add a corpus of positive/negative examples before changing matching semantics. Do not present a negative as proof that no agreement exists. |
| R05 | The recognized action can be configured for DCO using `use-dco-flag: true`. Merely finding its identity does not establish CLA use. | Decide whether DCO counts for this tool. If it does not, inspect relevant configuration and test DCO-only, CLA, and ambiguous expression-valued settings. [Upstream action options](https://github.com/contributor-assistant/github-action). |
| R06 | Root-only `.clabot` detection does not cover shared `owner/clabot-config/.clabot` configuration. A file's existence also does not prove the bot is installed or active. | Document the limitation or add evidence-aware support; do not mark every repository positive merely because a shared config exists. [cla-bot configuration](https://colineberhardt.github.io/cla-bot/#configuration-options). |
| R07 | Git symlinks are blobs containing target text; `contentAtPath` reads that text rather than the linked document. Workflow symlink handling is also undefined. | Document unsupported symlinks or resolve allowed repository-local targets with cycle/depth limits. Test file modes, dangling links, and cycles. The [Contents API](https://docs.github.com/en/rest/repos/contents#get-repository-content) documents dereferencing behavior for ordinary in-repository file targets. |
| R08 | `knownOwners` is a static owner-wide guess. Individual repositories can have exceptions or policy changes; redirected/transferred repositories keep the caller's owner at `cla.go:54`. | Preserve source/provenance and canonical owner when appropriate. Review policies before updating entries; do not assume an owner-wide rule is universally true. |
| R09 | Custom GitHub Enterprise clients can have unrelated organizations with names such as `google` or `microsoft`, but the same public known-owner table is applied. | Scope public-owner evidence to the intended host or document/support host-specific lists. Test a custom client base URL. |
| R10 | `go.mod` pins old direct/transitive dependencies. A vulnerability scan reports affected symbols/modules, with important reachability caveats. | Review `dependency-scan.txt`; update dependencies deliberately, reassess advisories with the chosen compiler, and rerun behavioral tests. A scanner finding is not proof of exploitability in this utility. See the scan interpretation below. |
| R11 | `.github/workflows/test.yml` pins Go 1.18 and checkout/setup-go v2. Go 1.18 is outside Go's supported release window; the old action definitions use obsolete runtimes. | Select and document a supported Go baseline, update actions, restrict workflow token permissions, and consider immutable action pins. Verify a real CI run. Do not claim the current job necessarily fails merely because runners may remap an old runtime. [Go support policy](https://go.dev/doc/devel/release#policy). |
| R12 | `go.mod:20` includes a `replace` directive. Version-suffixed `go install module/cmd@version` rejects modules with applicable replace directives under Go's install rules. The repository only documents a source build. | Treat versioned installation as an optional distribution target. If supporting it, remove/resolve the replacement through the actual dependency graph and test installation from a tagged version in a clean environment. [Go install reference](https://go.dev/ref/mod#go-install). |
| R13 | The checker downloads the full recursive repository tree, full base64 blobs, and sequential workflow contents. Regexes are compiled repeatedly. Blob sizes/workflow count are not bounded locally. | Profile large repositories; prefer targeted discovery, compiled patterns where still needed, cancellation, and explicit resource budgets. If a budget prevents completion, return incomplete status rather than a clean negative. No memory-exhaustion exploit was demonstrated. |
| R14 | `cla.go:40` assumes nonnil rate-limit data and `Core`. A malformed successful response such as `{}` can panic. Nil public client/context arguments and empty owner/repo also have no stated contract. | Add defensive validation if these inputs are in scope; document preconditions otherwise. Test malformed rate metadata without claiming normal GitHub responses omit required data. Avoid turning malformed/missing information into a zero-valued clean result. |
| R15 | `checks.go:197` hardcodes `master` in errors even when another branch was selected; `checks.go:219` formats a map in nondeterministic order. The CLI labels the detected action `cla-bot`, and omits a final newline. | Use actual branch/context, deterministic error ordering, correct action naming, and a trailing newline. These are minor diagnostic/presentation defects, not independent central algorithm failures. |
| R16 | `CONTRIBUTING.md:3-6,30,50-55` simultaneously forbids external contributions, welcomes them, and requires a CLA process described as unavailable. | Flag the contradiction and seek an actual maintainer policy when updating contribution guidance. The local model must not invent permission to accept contributions or claim an unavailable process exists. This does not block local bug fixes. |

Other possible CLA mechanisms, older PRs beyond the most recent 100, organization-level policies outside these heuristics, and disabled workflows remain detection limits. Inspecting only 100 PRs matches the documented contract and should not be reported as a pagination bug. The `checkAll` goroutines are drained by their caller; this review did not establish a send-channel leak. The UTF-8 check/cross symbols are valid; PowerShell's default file decoding can display mojibake but that is not corruption in the source. Ignoring `ff.Parse` is worth cleaning up, but no currently reachable parse-return bug was established with `ExitOnError` and the single string flag.

## Dependency scan interpretation

`govulncheck` v1.4.0 was run against this source using the installed Go 1.25.12 compiler. Its captured output is in `dependency-scan.txt`.

- Four reported symbol findings concern the **installed standard library**, rather than four independent defects in the repository's source: [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218), [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090), [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972), and [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026). The scanner reports fixes in Go 1.25.13. These are additional reasons to use a supported, patched compiler; they do not establish what compiler a published binary used.
- One symbol finding concerns unmaintained `golang.org/x/crypto/openpgp`, brought in by the pinned GitHub SDK. Its scanner trace reaches package initialization. This repository does not call the OpenPGP parsing functions directly; initialization reachability does not demonstrate exploitation of those functions. [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932).
- Package-only and module-only findings need separate reachability review. Do not add all scanner counts to the bug count or claim the repository has that many exploitable vulnerabilities.

The raw output retains the advisory IDs, traces, and fix information so the local model can propose an evidence-based dependency update. Recheck current advisories when performing fixes; their status can change.

## Implementation order for the local model

1. **Make failures safe and results honest:** B01, B02, B03, B13, B15. Add deterministic public-API and CLI error tests. Preserve partial positive evidence and make unknown results visible.
2. **Repair bounded detection mistakes:** B04, B05, B06, B10, B12. These are small changes with clear input/output regressions. Keep positive and negative controls.
3. **Repair workflow discovery and parsing:** B07, B08, B09. Choose a maintained YAML parser compatible with the selected Go baseline, inspect actual step values, and define per-file errors. Decide R05 DCO behavior explicitly.
4. **Make execution predictable:** B11, B14, B16. Validate input before requests, bound CLI execution, and preserve real quota errors. Consider R01/R02 if expanding robustness beyond the confirmed fixes.
5. **Finish validation and maintenance:** B17, B18, R10, R11, and the minor R15 diagnostics. Update examples, add stable tests, choose a supported toolchain/dependency policy, and run CI. Evaluate the remaining R-items separately rather than silently expanding scope.

Some tasks interact: fixing B03 changes request counts for B11; adopting YAML affects dependencies; SDK upgrades can change URL escaping; a Go baseline change affects error aggregation. Prefer focused patches, but review the final combined behavior.

## Completion and evaluation criteria

- Existing public entry points (`Check`, `CheckWithContext`, `Detail`, `DetailWithContext`) and per-check result/error fields remain usable, unless a documented compatibility change is explicitly chosen.
- Every confirmed runtime defect has a deterministic regression test with positive/negative controls. At minimum exercise complete-positive, complete-negative, partial-positive, and incomplete-no-evidence scans end to end.
- Test transport errors, 401/403/404/429/500, invalid JSON/base64, missing paths, cancellation/deadlines, malformed YAML, special branch characters, workflow file types, and all six detail flags.
- Run `go test -short ./...`, `go vet ./...`, and race tests where cgo/race support is available. Ensure ordinary examples do not require network access. Run with the documented minimum Go version and a current supported version if they differ.
- Re-run `python audit/run_probes.py`. If internal APIs were intentionally changed, port equivalent fixtures rather than deleting failing requirements. Dependency upgrades need locally cached modules before this offline runner can work.
- Re-run the vulnerability scan with the selected toolchain, distinguish module presence from reachable risky behavior, and document remaining findings.
- Do not introduce hidden network dependence, hardcoded answers, broad error suppression, arbitrary sleeps, skipped tests, or a fallback that converts unknown into false.
- Report which B-items were fixed, which R-items were implemented/deferred with reasons, what commands passed, and any compatibility decisions. A green pre-existing test suite alone is not a sufficient result.

For evaluation, give the local model `LOCAL_MODEL_PROMPT.md` and this plan. Keep the baseline output as evidence, and independently check the behavioral assertions after the model finishes. No production fixes are included in this audit.
