"""Run the independent review probes against a temporary source snapshot.

Usage: python audit/run_review_probes.py [--source-root PATH] [--output PATH]
Requires the current Go modules in the local cache. The working Go files and
audit fixtures are never modified. Results go to stdout; --output also writes
a new results file and refuses to overwrite an existing file.
"""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-root", type=Path,
        help="Git worktree to test (default: the repository containing this script)",
    )
    parser.add_argument(
        "--output", type=Path,
        help="Also save results to a new file; existing files are never overwritten",
    )
    args = parser.parse_args()
    audit_dir = Path(__file__).resolve().parent
    source_root = (args.source_root or audit_dir.parent).resolve()
    if not source_root.is_dir():
        parser.error("--source-root must identify an existing Git worktree directory")
    try:
        repo = Path(subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"], cwd=source_root,
            stderr=subprocess.PIPE,
        ).decode().strip()).resolve()
    except subprocess.CalledProcessError:
        parser.error("--source-root must identify a Git worktree")
    output_path = args.output.resolve() if args.output is not None else None
    if output_path is not None:
        if output_path.exists():
            parser.error("--output must be a new file; existing results are preserved")
        if not output_path.parent.is_dir():
            parser.error("--output parent directory must already exist")
    fixture_map = {
        "review_api_test.go.txt": "review_api_test.go",
        "review_heuristics_test.go.txt": "review_heuristics_test.go",
        "review_cli_test.go.txt": "cmd/need-cla/review_cli_test.go",
    }
    for fixture in fixture_map:
        if not (audit_dir / fixture).is_file():
            parser.error("missing review fixture: " + fixture)
    if not (repo / "audit_repro_test.go").is_file():
        parser.error(
            "review probes require the model implementation and its audit_repro_test.go "
            "helpers; select that checkout with --source-root (see audit/README.md)"
        )
    env = os.environ.copy()
    env.update(GOCACHE=str(Path(tempfile.gettempdir()) / "need-cla-audit-go-cache"),
               GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
    names = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=repo
    ).decode().split("\0")
    with tempfile.TemporaryDirectory(prefix="need-cla-review-") as tmp:
        snapshot = Path(tmp)
        for name in names:
            if not name or name.startswith("audit/") or not (repo / name).is_file():
                continue
            dest = snapshot / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(repo / name, dest)
        for fixture, dest in fixture_map.items():
            target = snapshot / dest
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(audit_dir / fixture, target)
        command = ["go", "test", "-count=1", "-short", "-timeout=45s", "-run", "^TestReview", "-v", "./..."]
        result = subprocess.run(command, cwd=snapshot, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        output = result.stdout.decode("utf-8", errors="replace")
        report = "Command: " + " ".join(command) + "\nExit code: " + str(result.returncode) + "\n\n" + output
        if output_path is not None:
            # Exclusive creation also protects against a file appearing during the run.
            with output_path.open("x", encoding="utf-8") as results_file:
                results_file.write(report)
        print(report.encode("ascii", errors="backslashreplace").decode("ascii"))
        return result.returncode


if __name__ == "__main__":
    raise SystemExit(main())
