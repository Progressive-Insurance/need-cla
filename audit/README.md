# Audit and model evaluation

This directory records two stages of the same exercise:

- [FIX_PLAN.md](FIX_PLAN.md) and [LOCAL_MODEL_PROMPT.md](LOCAL_MODEL_PROMPT.md) describe the original audit and the task given to the local model.
- [MODEL_REVIEW.md](MODEL_REVIEW.md) evaluates the model's resulting patch, including remaining defects and independent acceptance checks.

The original baseline is commit `d5221423a4e0476e4fe8379238c86301a6c0033b`. The reviewed Qwen implementation is commit [`a65e26459fea72e4cda73ae041a0b40c841dd2bb`](https://github.com/deeto15/need-cla/commit/a65e26459fea72e4cda73ae041a0b40c841dd2bb) in [attempt 2, PR #11](https://github.com/Progressive-Insurance/need-cla/pull/11). That PR contains the model's code together with this audit plan, evaluation, and evidence. It preserves the reviewed attempt, including the limitations recorded in the review.

The experiment compares distinct attempts:

- **Attempt 1 — [PR #10](https://github.com/Progressive-Insurance/need-cla/pull/10):** Qwen worked without the Codex fix plan.
- **Attempt 2 — this PR:** Qwen received the complete Codex plan and implemented it as one batch.
- **Attempt 3 — not started:** a future step-by-step attempt, separate from this PR.

The dated reports describe the state at the time of each audit. Statements about untracked files, available model identity, or observed CI runs are historical observations. The code commit above identifies the implementation that was reviewed.

During publication, the implementation commit passed the fork's Linux CI, including `go vet`, ordinary short-mode tests, and race tests: [CI run](https://github.com/deeto15/need-cla/actions/runs/35226261439). This updates the historical review's lack of an observed remote race run; it does not resolve the independently reproduced findings. The later packaging commit adds audit artifacts and runner conveniences without changing Qwen's production implementation or its tests.

## Run the probes

Install Python 3, Git, and a Go toolchain compatible with the selected source checkout. Populate that checkout's Go module cache before running the probes, for example with `go mod download` in the source checkout. Both runners disable module and toolchain downloads while testing.

Both scripts take `--source-root` to select a separate Git worktree. They copy its current tracked and nonignored untracked files into a temporary directory and load fixtures from the audit directory containing the script. They test the working files, including uncommitted changes, rather than automatically checking out the documented commit. For an exact historical reproduction, use a clean worktree at that commit.

From the audit checkout, run the original probes against the original baseline:

```text
python audit/run_probes.py --source-root ../need-cla-baseline
```

Here `../need-cla-baseline` must be a Git worktree at `d5221423a4e0476e4fe8379238c86301a6c0033b`. The baseline is expected to fail 20 top-level behavioral groups. Those failures reproduce the original defects; they are not a broken installation. Captured results are in [probe-results.txt](probe-results.txt). The original runner also accepts `--race` when the installed toolchain supports it.

Run both sets of probes against the Qwen code checkout:

```text
python audit/run_probes.py --source-root ../need-cla-qwen
python audit/run_review_probes.py --source-root ../need-cla-qwen
```

Here `../need-cla-qwen` must contain the reviewed code commit above. The original probes pass on that attempt. The independent review probes expose six remaining top-level failure groups; see [review-probe-results.txt](review-probe-results.txt) for the captured output and [MODEL_REVIEW.md](MODEL_REVIEW.md) for interpretation. Some individual cases test an explicitly chosen policy, rather than an independently established runtime defect.

The review fixtures require the model's private implementation functions and the test helpers in its root `audit_repro_test.go`. They cannot run against the original baseline or the unassisted attempt without adaptation. The optional `--source-root` flag is useful when comparing checkouts: invoke the scripts from the attempt 2 checkout and select the source worktree to test. No copying or merging of audit files into that source worktree is required.

If `--source-root` is omitted, each script tests the repository containing that script. Both return the Go test exit status. The original runner selects `TestAudit*`; the review runner selects `TestReview*`. Run the source checkout's ordinary `go test -short ./...` and `go vet ./...` separately to exercise its complete suite.

Results go to stdout. The review runner optionally saves a new log:

```text
python audit/run_review_probes.py --source-root ../need-cla-qwen --output ../review-rerun.txt
```

`--output` refuses to overwrite an existing file. Neither runner changes the selected source files or replaces the historical audit logs by default.

## Evidence

The baseline and review dependency scans are retained in [dependency-scan.txt](dependency-scan.txt) and [review-dependency-scan.txt](review-dependency-scan.txt). Their vulnerability findings depend on the recorded compiler, dependency versions, and advisory database; the reports distinguish those findings from demonstrated exploitable defects. The individual CLI and heuristic reviewer logs provide supplementary evidence alongside the combined review output.

The `.go.txt` files are probe source, stored outside the ordinary Go test suite. They are copied only into temporary snapshots by the runners. Generated coverage profiles and compiled binaries are not part of the audit deliverables.
