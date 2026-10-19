#!/usr/bin/env python3
"""Committed isolated notification inbox export/desktop method; no service credentials."""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import uuid

METHOD = Path(__file__).absolute().parent
STATES = ["empty-en", "one-unread-ro", "mixed-en", "all-read-ro", "escaped-ro", "unsafe-links-en"]
METHOD_NAMES = ["tools/gui-native-notification-inbox/" + name for name in (
    "qualify.py", "desktop-qa.mjs", "README.md", "export_test.go.txt", "extract-projection.py")]
BASELINE_KEEPER = "docs/reviews/gui-native-notification-preferences/evidence.tar.gz"
BASELINE_SHA = "29a9ff0d2e420d5454aaebd324d0bc686bc22cf14b6f04ce86e4b8ce7e71d7e5"


def need(ok, message):
    if not ok:
        raise ValueError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def regular(path, maximum=64 << 20):
    need(path.is_absolute() and path.resolve() == path, "canonical unaliased input required")
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and 0 <= before.st_size <= maximum, "bounded regular input required")
    with path.open("rb") as stream:
        raw = stream.read(maximum + 1)
        after = os.fstat(stream.fileno())
    need(len(raw) == after.st_size <= maximum and
         all(getattr(before, key) == getattr(after, key) for key in
             ("st_dev", "st_ino", "st_mode", "st_size", "st_mtime_ns", "st_ctime_ns")),
         "input changed during read")
    return raw


def executable_digest(path):
    """Hash an explicit installed executable without loading it as a payload."""
    need(path.is_absolute() and path.resolve() == path, "canonical executable required")
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and before.st_size > 0 and os.access(path, os.X_OK),
         "existing regular executable required")
    hashed, count = hashlib.sha256(), 0
    with path.open("rb") as stream:
        opened = os.fstat(stream.fileno())
        need((before.st_dev, before.st_ino) == (opened.st_dev, opened.st_ino),
             "executable replaced before read")
        while chunk := stream.read(1 << 20):
            count += len(chunk)
            need(count <= before.st_size, "executable grew during read")
            hashed.update(chunk)
        after = os.fstat(stream.fileno())
    final = path.lstat()
    keys = ("st_dev", "st_ino", "st_mode", "st_size", "st_mtime_ns", "st_ctime_ns")
    need(count == before.st_size and
         all(getattr(before, key) == getattr(opened, key) == getattr(after, key) == getattr(final, key)
             for key in keys), "executable changed during streamed read")
    return hashed.hexdigest()


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args],
                                   env={"PATH": os.defpath, "LANG": "C.UTF-8"})


def source_bridge(repo, source):
    need(repo.is_absolute() and repo.resolve() == repo and METHOD == repo / "tools/gui-native-notification-inbox"
         and re.fullmatch("[0-9a-f]{40}", source), "canonical committed method/source required")
    need(not git(repo, "status", "--porcelain"), "clean committed source required")
    git(repo, "merge-base", "--is-ancestor", source, "HEAD")
    allowed = set(METHOD_NAMES) | {"STATUS.md", "docs/execplans/gui-native-notification-inbox.md"}
    changed = [x.decode() for x in git(repo, "diff", "--no-renames", "--name-only", "-z", source, "HEAD").split(b"\0") if x]
    need(set(changed) <= allowed, "change outside the five explicit UI methods/two docs; native/capture/guard inputs must remain exact")
    names = [x.decode() for x in git(repo, "ls-files", "-z").split(b"\0") if x]
    for name in METHOD_NAMES:
        need(name in names and git(repo, "show", "HEAD:" + name) == regular(repo / name), "exact committed five-method bytes required")
    pg_driver = "tools/gui-native-notification-inbox/qualify-pg.py"
    need(pg_driver in names and git(repo, "show", "HEAD:" + pg_driver) == regular(repo / pg_driver), "new seven-package PG driver must be committed and protected")
    products = sorted(set(names) - allowed)
    need(len(products) > 2000, "full tracked source closure required")
    return {"producer_source": source, "ui_head": git(repo, "rev-parse", "HEAD").decode().strip(),
            "UI_only_changed_paths": changed, "product_paths": products,
            "source_paths": sorted(set(products) | set(METHOD_NAMES)),
            "scope": "Full tracked product closure, excluding only five named UI methods and two nonruntime docs; no original helper bridge"}


def command(argv, cwd, env, output, label, timeout):
    stdout, stderr = output / (label + ".stdout"), output / (label + ".stderr")
    with stdout.open("xb") as out, stderr.open("xb") as err:
        child = subprocess.Popen([str(value) for value in argv], cwd=cwd, env=env,
                                 stdout=out, stderr=err, start_new_session=True)
        timed_out = False
        try:
            exit_code = child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            os.killpg(child.pid, signal.SIGTERM)
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait(timeout=5)
            exit_code = child.returncode
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    return {"argv": [str(value) for value in argv], "pid": child.pid,
            "owned_process_group": child.pid, "exit_code": exit_code,
            "timed_out": timed_out, "stdout": stdout.name, "stderr": stderr.name,
            "stdout_sha256": digest(regular(stdout)), "stderr_sha256": digest(regular(stderr))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("repo", "scratch", "go", "node", "cache", "modcache",
                 "playwright-package", "axe-file", "chrome", "pg-run"):
        parser.add_argument("--" + name, type=Path)
    parser.add_argument("--producer-source", required=True)
    parser.add_argument("--check-bridge", action="store_true", help="read-only exact source bridge, no export/browser")
    parser.add_argument("--benchmark", action="store_true",
                        help="retain separate warmed matched-body measurements; no speedup acceptance")
    args = parser.parse_args()
    repo = args.repo
    need(repo is not None, "explicit repo required")
    bridge_before = source_bridge(repo, args.producer_source)
    if args.check_bridge:
        print(json.dumps(bridge_before)); return
    need(all(getattr(args, name) is not None for name in ("scratch", "go", "node", "cache", "modcache", "playwright_package", "axe_file", "chrome", "pg_run")), "all explicit existing dependency/run paths required")
    need(repo.is_absolute() and repo.resolve() == repo and METHOD == repo / "tools/gui-native-notification-inbox",
         "invoke the committed method inside its exact owning checkout")
    need(re.fullmatch("[0-9a-f]{40}", args.producer_source), "literal complete PG producer SHA required")
    need(not git(repo, "status", "--porcelain").strip(), "clean committed checkout including all methods required")
    head = git(repo, "rev-parse", "HEAD").decode().strip()
    git(repo, "merge-base", "--is-ancestor", args.producer_source, head)
    method_rows = []
    for name in METHOD_NAMES:
        raw = regular(repo / name)
        need(git(repo, "show", head + ":" + name) == raw, "method must equal committed Git bytes")
        method_rows.append({"path": name, "bytes": len(raw), "sha256": digest(raw)})
    baseline = regular(repo / BASELINE_KEEPER)
    need(digest(baseline) == BASELINE_SHA, "accepted notification-preferences31 baseline keeper differs")
    scratch = args.scratch
    need(scratch.is_absolute() and scratch.resolve() == scratch and scratch.is_dir() and
         scratch.stat().st_uid == os.getuid() and "_temp" in scratch.parts and
         not scratch.is_relative_to(repo), "existing owned canonical task scratch outside source required")
    pg_run = args.pg_run
    need(pg_run.is_absolute() and pg_run.resolve() == pg_run and pg_run.is_dir() and
         "_temp" in pg_run.parts and pg_run.stat().st_uid == os.getuid(),
         "literal actual owned successful PG result directory required")
    paths = {}
    for name in ("go", "node", "cache", "modcache", "playwright_package", "axe_file", "chrome"):
        value = getattr(args, name)
        need(value.is_absolute(), "absolute existing dependency path required: " + name)
        paths[name] = value.resolve(strict=True)
    for name in ("go", "node", "chrome"):
        executable_digest(paths[name])
    regular(paths["axe_file"])
    need(all(paths[name].is_dir() for name in ("cache", "modcache", "playwright_package")),
         "existing caches and installed Playwright directory required")
    output = scratch / ("run-" + args.producer_source[:12] + "-" + str(uuid.uuid4()))
    output.mkdir(mode=0o700)
    # Short, exclusively created HOME/TMPDIR sibling: Chromium's Unix socket
    # must not inherit the deeply nested UUID result-directory path.
    temp = Path(tempfile.mkdtemp(prefix="ni-", dir=scratch))
    env = {"PATH": os.defpath, "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
           "HOME": str(temp), "TMPDIR": str(temp), "GOCACHE": str(paths["cache"]),
           "GOMODCACHE": str(paths["modcache"]), "GOTOOLCHAIN": "local", "GOENV": "off",
           "GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-p=2",
           "GOMAXPROCS": "2"}
    report = {
        "schema": 1, "status": "RUNNING", "PG_producer_source": args.producer_source,
        "UI_method_source": head, "committed_method": method_rows, "source_bridge": bridge_before,
        "baseline_keeper": {"path": BASELINE_KEEPER, "sha256": BASELINE_SHA},
        "scope": "Isolated actual native rendering, six desktop/six no-JS projection fixtures, strict Axe/current assets/isolated readonly endpoints and thirty-one prior HTML equalities; no authenticated HTTP, production transport, codec, provider, authority or release claim.",
        "disk_before": dict(zip(("total", "used", "free"), shutil.disk_usage(scratch))),
        "commands": [], "failures": [], "owned_temp": {"path": str(temp), "removed": False},
        "benchmark_requested": args.benchmark,
        "benchmark": {"status": "REQUESTED_NOT_REACHED" if args.benchmark else "NOT_REQUESTED"},
    }
    (output / "status.json").write_text(json.dumps(report, indent=2) + "\n")
    desktop_attempted = False
    desktop_cleanup_proven = False

    def run(argv, cwd, label, timeout):
        row = command(argv, cwd, env, output, label, timeout)
        report["commands"].append(row)
        need(row["exit_code"] == 0 and not row["timed_out"], label + " failed; retain exact streams")
        return row

    try:
        go_version = subprocess.check_output([str(paths["go"]), "version"], env=env, timeout=10).decode().strip()
        node_version = subprocess.check_output([str(paths["node"]), "--version"], env=env, timeout=10).decode().strip()
        need(re.search(r"\bgo1\.27\.1\b", go_version) and re.match(r"^v24\.", node_version),
             "installed Go1.27.1 and actual Node24 executable required")
        report["runtime"] = {"go": go_version, "node": node_version}
        report["explicit_dependencies"] = [{"name": name, "path": str(paths[name]),
                                             "sha256": (digest(regular(paths[name])) if name == "axe_file"
                                                        else executable_digest(paths[name]))}
                                            for name in ("go", "node", "axe_file", "chrome")]
        package = paths["playwright_package"] / "package.json"
        report["explicit_dependencies"].append({"name": "playwright_package",
                                                "path": str(package), "sha256": digest(regular(package))})
        capture, overlay, fixtures = output / "capture.json", output / "overlay.json", output / "fixtures"
        run(["python3", "-I", METHOD / "extract-projection.py", "--repo", repo,
             "--run", pg_run, "--source", args.producer_source, "--output", capture],
            repo, "extract", 60)
        report["capture_sha256"] = digest(regular(capture))
        replacements = {
            "notification_inbox_ui_qa_overlay_test.go": METHOD / "export_test.go.txt",
            "notification_preferences_ui_qa_overlay_test.go": repo / "tools/gui-native-notification-preferences/export_test.go.txt",
            "group_detail_ui_qa_overlay_test.go": repo / "tools/gui-native-group-detail/fixture-export_test.go.txt",
            "group_ui_qa_overlay_test.go": repo / "tools/gui-native-group/fixture-export_test.go.txt",
            "family_ui_qa_overlay_test.go": repo / "tools/gui-native-family/ui/fixture-export_test.go.txt",
        }
        mapping = {}
        for name, implementation in replacements.items():
            virtual = repo / "services/server/internal/web" / name
            need(not virtual.exists() and not virtual.is_symlink(), "overlay may only add absent virtual sources")
            regular(implementation)
            mapping[str(virtual)] = str(implementation)
        with overlay.open("x") as file:
            json.dump({"Replace": mapping}, file, indent=2); file.write("\n")
        run([paths["go"], "test", "-mod=readonly", "-overlay=" + str(overlay), "-count=1", "-v",
             "./internal/web", "-run", "^TestSocialNotificationInboxQAExport$", "-args",
             "-notification-inbox-ui-producer-source", args.producer_source,
             "-notification-inbox-ui-qa-output-dir", fixtures,
             "-notification-inbox-benchmark-capture", capture],
            repo / "services/server", "export", 180)
        export = regular(output / "export.stdout").decode()
        need("=== RUN   TestSocialNotificationInboxQAExport" in export and
             "--- PASS: TestSocialNotificationInboxQAExport (" in export and
             not re.search(r"--- (FAIL|SKIP):", export) and
             re.search(r"^ok\s+\S+\s+[0-9.]+s$", export, re.M),
             "actual unskipped export root and package PASS required")
        manifest_path = fixtures / "fixtures.json"
        manifest = json.loads(regular(manifest_path))
        need(manifest["PG_producer_source"] == args.producer_source and
             manifest["UI_method_source"] == head and manifest["source_unchanged"] is True and
             manifest["sources_before"] == manifest["sources_after"] and
             [row["name"] for row in manifest["states"]] == STATES,
             "complete exact-source six-state export differs")
        for row in manifest["states"]:
            raw = regular(fixtures / (row["name"] + ".html"))
            need(len(raw) == row["bytes"] and digest(raw) == row["sha256"],
                 "actual native fixture bytes differ")
        previous = manifest["previous_body_equality"]
        need(len(previous) == 31 and len({(row["kind"], row["name"]) for row in previous}) == 31 and
             Counter(row["kind"] for row in previous) == Counter({"family": 8, "group-create": 10, "group-detail": 7, "notification-preferences": 6}) and
             all(row["equal"] is True for row in previous),
             "all family8, Group-create10, Group-detail7 and preferences6 bodies must remain exact")
        for row in previous:
            need(re.fullmatch(r"(?:family|group-create|group-detail|notification-preferences)/[a-z-]+\.html", row["path"]),
                 "finite prior-body relative path required")
            raw = regular(fixtures / row["path"])
            need(len(raw) == row["bytes"] and digest(raw) == row["sha256"],
                 "retained prior native body differs")
        report["previous_body_equality"] = previous
        report["normalization"] = manifest["normalization"]
        report["fixture_manifest_sha256"] = digest(regular(manifest_path))
        desktop = output / "desktop"
        desktop_attempted = True
        run([paths["node"], METHOD / "desktop-qa.mjs", "--repo", repo, "--producer-source", args.producer_source,
             "--fixtures", fixtures, "--output", desktop, "--playwright-package", paths["playwright_package"],
             "--axe-file", paths["axe_file"], "--chrome", paths["chrome"]],
            repo, "desktop", 600)
        actual_path = desktop / "desktop-results.json"
        actual = json.loads(regular(actual_path))
        need(actual["status"] == "pass" and not actual["failures"] and actual["sourceUnchanged"] is True and
             [row["name"] for row in actual["states"]] == STATES and
             [row["name"] for row in actual["noJavaScriptStates"]] == STATES and
             actual["ownership"]["serverStopped"] is True and
             len(actual["ownership"]["profiles"]) == 2 and
             all(row["closed"] is True and row["removed"] is True for row in actual["ownership"]["profiles"]),
             "all six desktop/no-JS cases and actual owned cleanup must pass")
        desktop_cleanup_proven = True
        report["desktop_report_sha256"] = digest(regular(actual_path))
        if args.benchmark:
            report["benchmark"] = {"status": "REQUESTED_NOT_ADMITTED"}
            run([paths["go"], "test", "-mod=readonly", "-overlay=" + str(overlay), "-run", "^$",
                 "-bench", "^BenchmarkSocialNotificationInboxBodyRender$", "-benchmem", "-benchtime=100x",
                 "-count=3", "-cpu=1", "./internal/web", "-args",
                 "-notification-inbox-ui-producer-source", args.producer_source,
                 "-notification-inbox-benchmark-capture", capture],
                repo / "services/server", "benchmark", 180)
            raw = regular(output / "benchmark.stdout")
            text = raw.decode()
            rows = re.findall(r"^(BenchmarkSocialNotificationInboxBodyRender/(?:en|ro)/(?:classic|native))(?:-[0-9]+)?\s+100\s+", text, re.M)
            expected = Counter({"BenchmarkSocialNotificationInboxBodyRender/" + language + "/" + engine: 3
                                for language in ("en", "ro") for engine in ("classic", "native")})
            need(Counter(rows) == expected and not re.search(r"--- (FAIL|SKIP):", text) and
                 re.search(r"^ok\s+\S+\s+[0-9.]+s$", text, re.M),
                 "actual matched four groups/three samples/100 iterations receipt required")
            report["benchmark"] = {"status": "EXECUTED_SEPARATE_NOT_ADMITTED", "rows": len(rows),
                                   "stdout_sha256": digest(raw),
                                   "scope": "Same-call forwarded registered API display projection, warmed classic/native body render only. Native projection/copy cost included; markup differs. No HTTP, DB, media, throughput or generalized speedup acceptance."}
        need(source_bridge(repo, args.producer_source) == bridge_before, "source bridge changed")
        need(not git(repo, "status", "--porcelain").strip() and
             git(repo, "rev-parse", "HEAD").decode().strip() == head,
             "owning committed source changed during method")
        report["status"] = "PASS"
    except Exception as error:
        report["status"] = "FAIL"
        report["failures"].append(str(error))
    finally:
        report["git_after"] = {"head": git(repo, "rev-parse", "HEAD").decode().strip(),
                               "porcelain_empty": not git(repo, "status", "--porcelain").strip()}
        report["disk_after"] = dict(zip(("total", "used", "free"), shutil.disk_usage(scratch)))
        # Only this method's exclusively created HOME/TMPDIR is eligible.
        # Node independently records both owned contexts/profiles closed/removed.
        try:
            if desktop_attempted and not desktop_cleanup_proven:
                # A failed result may still independently prove both browser
                # shutdowns. If it does not, retain its exact profile namespace;
                # never delete a potentially live browser's profile blindly.
                desktop_record = output / "desktop/desktop-results.json"
                if desktop_record.exists():
                    custody = json.loads(regular(desktop_record))["ownership"]
                    desktop_cleanup_proven = (custody["serverStopped"] is True and
                        all(row["closed"] is True and row["removed"] is True for row in custody["profiles"]))
            if not desktop_attempted or desktop_cleanup_proven:
                shutil.rmtree(temp)
                report["owned_temp"]["removed"] = not temp.exists()
            else:
                report["failures"].append("browser shutdown/profile cleanup unproven; exact owned temp retained")
        except Exception as error:
            report["failures"].append("owned temporary cleanup failed: " + str(error))
        artifacts = []
        for file in sorted(output.rglob("*")):
            if file.is_symlink():
                report["failures"].append("unexpected output symlink; retained for inspection")
                continue
            if file.is_file() and file.name not in ("status.json", "result.json"):
                raw = regular(file)
                artifacts.append({"path": file.relative_to(output).as_posix(),
                                  "bytes": len(raw), "sha256": digest(raw)})
        report["artifacts"] = artifacts
        if report["failures"]:
            report["status"] = "FAIL"
        (output / "result.json").write_text(json.dumps(report, indent=2) + "\n")
        result_sha = digest(regular(output / "result.json"))
        (output / "status.json").write_text(json.dumps({"status": report["status"],
                                                       "result_sha256": result_sha}) + "\n")
        print(json.dumps({"status": report["status"], "output": str(output), "result_sha256": result_sha}))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
