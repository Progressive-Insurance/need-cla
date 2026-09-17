"""Evaluate the exact PR #10 commit in a temporary archive, without checking it out.

Run from any directory: python audit/pr10-review/run_review.py
Dependencies must already exist in the local Go module cache. No downloads or
live GitHub requests are made by these checks. Assertion failures are expected.
"""

import argparse
import io
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile


PR10 = "325201f1d0f7c7f05463f08383325e9c05cb6501"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ref", default=PR10)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--regressions-only", action="store_true")
    args = parser.parse_args()
    review_dir = Path(__file__).resolve().parent
    audit_dir = review_dir.parent
    repo = audit_dir.parent
    output = (args.output or review_dir).resolve()
    output.mkdir(parents=True, exist_ok=True)
    commit = subprocess.check_output(
        ["git", "rev-parse", "--verify", args.ref + "^{commit}"], cwd=repo,
        text=True,
    ).strip()
    version = subprocess.check_output(["go", "version"], text=True).strip()
    env = os.environ.copy()
    env.update(GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
    env["GOCACHE"] = str(Path(tempfile.gettempdir()) / "need-cla-audit-go-cache")
    statuses = []
    archive = subprocess.check_output(
        ["git", "archive", "--format=zip", commit], cwd=repo,
    )
    with tempfile.TemporaryDirectory(prefix="need-cla-pr10-review-") as tmp:
        snapshot = Path(tmp)
        with zipfile.ZipFile(io.BytesIO(archive)) as source:
            # Archive members come from this repository's tracked files only.
            for member in source.infolist():
                target = (snapshot / member.filename).resolve()
                if not target.is_relative_to(snapshot.resolve()):
                    raise ValueError("archive member escapes snapshot")
            source.extractall(snapshot)

        def run(label, command):
            result = subprocess.run(
                command, cwd=snapshot, env=env, stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT, text=True, encoding="utf-8",
                errors="replace", timeout=180,
            )
            log = (
                f"Commit: {commit}\nToolchain: {version}\n"
                "Environment: GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local\n"
                f"Command: {' '.join(command)}\n\n{result.stdout}\n"
                f"Exit code: {result.returncode}\n"
            )
            (output / (label + ".txt")).write_text(log, encoding="utf-8")
            print(f"{label}: exit {result.returncode}; log: {output / (label + '.txt')}", flush=True)
            statuses.append(result.returncode)

        if not args.regressions_only:
            run("existing-tests", ["go", "test", "-count=1", "-short", "-timeout=45s",
                                   "-coverprofile=review-coverage.out", "./..."])
            run("coverage", ["go", "tool", "cover", "-func=review-coverage.out"])
            run("vet", ["go", "vet", "./..."])
            run("build", ["go", "build", "./..."])
            for source, dest in [
                ("repro_test.go.txt", "audit_repro_test.go"),
                ("cli_repro_test.go.txt", "cmd/need-cla/audit_repro_test.go"),
            ]:
                shutil.copy2(review_dir / "fixtures" / source, snapshot / dest)
            run("original-audit-probes", ["go", "test", "-count=1", "-short", "-timeout=45s",
                                           "-run", "^TestAudit", "-v", "./..."])
        fixture = review_dir / "heuristics_test.go.txt"
        if fixture.exists():
            shutil.copy2(fixture, snapshot / "pr10_review_test.go")
            run("independent-regressions", ["go", "test", "-count=1", "-short", "-timeout=45s",
                                             "-run", "^TestPR10", "-v", "."])
        elif args.regressions_only:
            parser.error("missing independent fixture: " + str(fixture))
    # Review probes intentionally fail on defects; preserve a nonzero exit.
    return 1 if any(statuses) else 0


if __name__ == "__main__":
    raise SystemExit(main())
