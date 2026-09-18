"""Run audit fixtures against an isolated copy; never edit the original Go files.

Usage: python audit/run_probes.py [--source-root PATH] [--race]
The fixtures assert proposed corrected behavior, so failures are expected on
the audited baseline. They are examples, not a complete or refactor-proof grader.
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
    parser.add_argument("--race", action="store_true")
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
    fixtures = {
        "repro_test.go.txt": "audit_repro_test.go",
        "cli_repro_test.go.txt": "cmd/need-cla/audit_repro_test.go",
    }
    for fixture in fixtures:
        if not (audit_dir / fixture).is_file():
            parser.error("missing audit fixture: " + fixture)
    env = os.environ.copy()
    env["GOCACHE"] = str(Path(tempfile.gettempdir()) / "need-cla-audit-go-cache")
    # Force offline resolution: this suite must not fetch modules or a toolchain.
    env["GOPROXY"] = "off"
    env["GOSUMDB"] = "off"
    env["GOTOOLCHAIN"] = "local"
    with tempfile.TemporaryDirectory(prefix="need-cla-audit-") as tmp:
        snapshot = Path(tmp)
        files = subprocess.check_output(["git", "ls-files", "-z"], cwd=repo).decode().split("\0")
        for name in files:
            if not name or name.startswith("audit/") or not (repo / name).is_file():
                continue
            dest = snapshot / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(repo / name, dest)
        # Include nonignored new source/support files, including testdata and
        # embedded assets. Keep the audit directory out of the source snapshot.
        untracked = subprocess.check_output(
            ["git", "ls-files", "--others", "--exclude-standard", "-z"], cwd=repo
        ).decode().split("\0")
        for name in untracked:
            if name and not name.startswith("audit/") and (repo / name).is_file():
                dest = snapshot / name
                dest.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(repo / name, dest)
        # Fixtures belong to this audit checkout, even when testing another worktree.
        for fixture, name in fixtures.items():
            dest = snapshot / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(audit_dir / fixture, dest)
        cmd = ["go", "test", "-count=1", "-short", "-timeout=45s", "-run", "^TestAudit", "-v"]
        if args.race:
            cmd.append("-race")
        cmd.append("./...")
        print("Running:", " ".join(cmd), flush=True)
        completed = subprocess.run(cmd, cwd=snapshot, env=env)
        return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
