#!/usr/bin/env python3
"""Committed, isolated fixed-synthetic account presentation qualification."""
import argparse
from collections import Counter
from decimal import Decimal
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import uuid

METHOD = Path(__file__).absolute().parent
STATES = ["overview-basic-en", "overview-guardian-ro", "overview-ward-connections-en", "overview-escaped-ro", "settings-no-token-en", "settings-token-ro", "settings-expired-record-en", "settings-revoke-error-ro"]
METHOD_NAMES = ["tools/gui-native-account-center/" + name for name in ("qualify.py", "desktop-qa.mjs", "README.md", "export_test.go.txt", "source-bindings.json")]
BASELINE_KEEPER = "docs/reviews/gui-native-notification-inbox-dates/evidence.tar.gz"
BASELINE_SHA = "bc10a80ae1a9b0f404e9434f2a0065d941a0cb4a336d5ea06eb39032da09c7ea"
BASELINE_MANIFEST_SHA = "2787ff123c6b333d5aeed9650c22cdab92b8e2a76f189085a4eb52c5f3bc21d4"
BASELINE_UI = "/home/dobo/work/_temp/ni-ui/run-0afd29b79fec-5cec58a3-f1b8-4e1a-b683-d959e6b9ca59"
LANES = {"web", "app", "accounts", "recommendations", "social", "messaging", "notifications"}

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


def accepted_review(spec, required):
    need(isinstance(spec, dict) and set(spec) == {"path", "sha256", "field", "expected"}, "exact independently accepted source review binding")
    path = Path(spec["path"])
    need(path.is_absolute() and ("_temp" in path.parts or "docs" in path.parts), "owned or canonical review path")
    raw = regular(path); value = json.loads(raw)
    need(digest(raw) == spec["sha256"] and spec["field"] in ("status", "verdict")
         and spec["expected"].startswith("ACCEPT") and "NOT_ACCEPTED" not in spec["expected"]
         and value.get(spec["field"]) == spec["expected"] and value.get("findings") == [], "actual independent accepted review required")
    def scalars(item):
        if isinstance(item, str): return {item}
        if isinstance(item, dict): return set().union(*(scalars(x) for x in item.values()), set())
        if isinstance(item, list): return set().union(*(scalars(x) for x in item), set())
        return set()
    need(set(required) <= scalars(value), "source review must directly contain each exact code digest")


def binding_admission(repo, source):
    # Pure bounded reads before any Git or caller-provided dependency process.
    need(repo.is_absolute() and repo.resolve() == repo and METHOD == repo / "tools/gui-native-account-center"
         and re.fullmatch("[0-9a-f]{40}", source), "canonical committed method/source required")
    binding = json.loads(regular(METHOD / "source-bindings.json"))
    need(binding["status"] == "BOUND_REVIEWED_COMMITTED_METHODS" and binding["producer_source"] == source
         and re.fullmatch("[0-9a-f]{40}", binding["date_publication_source"] or ""),
         "unbound draft refuses before output, child command or dependency import")
    return binding


def dependency_admission(paths, binding):
    # Retain the exact bounded readers. Hash every explicit input once and
    # compare the entire receipt BEFORE creating output or executing Go/Node.
    need(all(paths[name].is_dir() for name in ("cache", "modcache", "playwright_package")),
         "existing caches and installed Playwright directory required")
    rows = [{"name": name, "path": str(paths[name]),
             "sha256": digest(regular(paths[name])) if name == "axe_file" else executable_digest(paths[name])}
            for name in ("go", "node", "axe_file", "chrome")]
    package = paths["playwright_package"] / "package.json"
    rows.append({"name": "playwright_package", "path": str(package), "sha256": digest(regular(package))})
    need({row["name"]: row["sha256"] for row in rows} == binding["dependency_sha256"],
         "all exact reviewed Go/Node/Chrome/Axe/Playwright input identities required before process/output")
    return rows


def source_bridge(repo, source):
    need(repo.is_absolute() and repo.resolve() == repo and METHOD == repo / "tools/gui-native-account-center"
         and re.fullmatch("[0-9a-f]{40}", source), "canonical committed method/source required")
    binding = binding_admission(repo, source)
    need(not git(repo, "status", "--porcelain", "--untracked-files=all"), "clean committed source required")
    git(repo, "merge-base", "--is-ancestor", binding["date_publication_source"], source)
    git(repo, "merge-base", "--is-ancestor", source, "HEAD")
    allowed = set(METHOD_NAMES) | {"STATUS.md", "docs/execplans/gui-native-account-center.md"}
    changed = [x.decode() for x in git(repo, "diff", "--no-renames", "--name-only", "-z", source, "HEAD").split(b"\0") if x]
    need(set(changed) <= allowed, "only five account UI methods/two explicit docs may bridge; product and old helpers exact")
    names = [x.decode() for x in git(repo, "ls-files", "-z").split(b"\0") if x]
    for name in METHOD_NAMES:
        need(name in names and git(repo, "show", "HEAD:" + name) == regular(repo / name), "exact committed five methods required")
    driver = "tools/gui-native-notification-inbox/qualify-pg.py"
    need(binding["complete7_driver_sha256"] == digest(regular(repo / driver))
         and git(repo, "show", source + ":" + driver) == regular(repo / driver), "untouched original complete7 qualifier required")
    for row in binding["unchanged_overlay_helpers"]:
        need(digest(regular(repo / row["path"])) == row["sha256"]
             and git(repo, "show", source + ":" + row["path"]) == regular(repo / row["path"]), "all original virtual overlay helpers remain byte exact")
    accepted_review(binding["method_source_review"], [digest(regular(METHOD / name)) for name in ("qualify.py", "desktop-qa.mjs", "export_test.go.txt")])
    accepted_review(binding["native_account_source_review"], [digest(regular(repo / name)) for name in (
        "services/server/internal/web/account_center_native.go", "services/server/internal/web/pages/account-center.gohtml",
        "services/server/internal/web/account_center_native_test.go", "static/css/account-center-native.css")])
    products = sorted(set(names) - allowed)
    need(len(products) > 2000 and len(binding["unchanged_overlay_helpers"]) == 5, "full product source/five old helpers required")
    return {"producer_source": source, "ui_head": git(repo, "rev-parse", "HEAD").decode().strip(),
            "UI_only_changed_paths": changed, "product_paths": products,
            "source_paths": sorted(set(products) | set(METHOD_NAMES)),
            "scope": "Full tracked product closure excluding only five account methods and two named nonruntime docs; no old helper/qualifier bridge"}


def qualified_pg(repo, run, source, binding):
    need(run.is_absolute() and run.resolve() == run and run.is_dir() and run.stat().st_uid == os.getuid()
         and "_temp" in run.parts, "actual owned successful complete7 PG directory required")
    raw = regular(run / "result.json")
    need(digest(raw) == binding["complete7_result_sha256"], "exact independently reviewed complete7 result required")
    value = json.loads(raw)
    need(value["source"] == source and value["tree"] == git(repo, "rev-parse", source + "^{tree}").decode().strip()
         and value["method_pass"] is True and value["errors"] == [] and set(value["lanes"]) == LANES
         and value["method_sha256"] == binding["complete7_driver_sha256"], "actual exact-source complete seven lanes")
    need(all(row["state"] == "PASS" and row["tests"]["fail"] == row["tests"]["skip"] == 0
             and row["tests"]["top_level"] > 0 and row["tests"]["named_passes"] == len(row["tests"]["identities"])
             for row in value["lanes"].values()), "all declared roots/named identities passed without omission")
    need(len(value["cleanup"]) == 15 and all(row["stopped"] and row["id_absent"] and row["name_absent"] for row in value["cleanup"].values())
         and set(value["retired_databases"]) == LANES
         and all(row["checkpoint_completed"] and row["name_and_oid_absent"] for row in value["retired_databases"].values())
         and value["pg_cleanup"]["stop_exit"] == 0 and value["pg_cleanup"]["status_exit"] == 3
         and value["pg_cleanup"]["pid_probe_exit"] == 1 and value["pg_cleanup"]["pid_and_socket_files_absent"] == 1,
         "all15 CIDs/seven DB retirements/private PG shutdown required")
    for row in value["artifact_rows"]:
        relative = Path(row["path"])
        need(not relative.is_absolute() and ".." not in relative.parts, "bounded literal artifact path")
        artifact = regular(run / relative)
        need(len(artifact) == row["bytes"] and digest(artifact) == row["sha256"], "actual raw complete7 artifact differs")
    for row in value["method_inputs"]:
        need(digest(regular(repo / row["path"])) == row["sha256"], "current eight PG method inputs differ")
    review = binding["complete7_review"]
    actual = json.loads(regular(Path(review["path"])))
    need(digest(regular(Path(review["path"]))) == review["sha256"]
         and actual.get(review["field"]) == review["expected"] and review["expected"].startswith("ACCEPT")
         and actual.get("findings") == [] and source in json.dumps(actual) and binding["complete7_result_sha256"] in json.dumps(actual),
         "independent exact actual complete7 acceptance required")
    return {"source": source, "result_sha256": digest(raw), "lanes": sorted(LANES),
            "roots": sum(row["tests"]["top_level"] for row in value["lanes"].values()),
            "named": sum(row["tests"]["named_passes"] for row in value["lanes"].values()),
            "actual_artifact_rows": len(value["artifact_rows"]), "new_account_capture": "NONE; fixed synthetic display maps only"}


def physical_baseline(repo, fixtures, manifest):
    root = repo / Path(BASELINE_KEEPER).parent
    archive_raw = regular(repo / BASELINE_KEEPER)
    outer_raw = regular(root / "archive-manifest.json")
    need(digest(archive_raw) == BASELINE_SHA and digest(outer_raw) == BASELINE_MANIFEST_SHA,
         "accepted date canonical baseline exact archive/manifest required")
    outer = json.loads(outer_raw)
    need(outer["archive_sha256"] == BASELINE_SHA and outer["complete_bytes_mode_UID_GID_nsmtime_roundtrip"] is True,
         "accepted date object manifest required")
    index, physical = {}, {}
    with tarfile.open(fileobj=io.BytesIO(archive_raw), mode="r:gz") as archive:
        members = archive.getmembers()
        need([member.name for member in members] == [row["member"] for row in outer["members"]], "entire baseline physical inventory")
        for member, row in zip(members, outer["members"]):
            need(member.isfile() and member.size == row["bytes"] and member.mode == row["mode"]
                 and member.uid == row["uid"] and member.gid == row["gid"]
                 and int(Decimal(member.pax_headers["mtime"]) * 10**9) == row["mtime_ns"], "original portable baseline metadata")
            raw = archive.extractfile(member).read()
            need(digest(raw) == row["sha256"], "baseline member bytes differ")
            index.setdefault(row["original"], []).append(row)
            if row["original"].startswith(BASELINE_UI + "/fixtures/"): physical[row["original"]] = raw
    fixture_rows = index.get(BASELINE_UI + "/fixtures/fixtures.json", [])
    need(len(fixture_rows) == 1, "one actual accepted date export manifest")
    prior_manifest = json.loads(physical[BASELINE_UI + "/fixtures/fixtures.json"])
    prior = prior_manifest["previous_body_equality"] + [dict(kind="notification-inbox", name=row["name"], path="notification-inbox/"+row["name"]+".html", bytes=row["bytes"], sha256=row["sha256"], equal=True) for row in prior_manifest["states"]]
    current = manifest["previous_body_candidates"]
    need(len(prior) == len(current) == 37 and [(r["kind"], r["name"]) for r in current] == [(r["kind"], r["name"]) for r in prior], "all37 prior identities/order")
    compared = []
    for row, accepted in zip(current, prior):
        name = row["name"] + ".html" if row["kind"] == "notification-inbox" else accepted["path"]
        original = BASELINE_UI + "/fixtures/" + name
        need(len(index.get(original, [])) == 1 and original in physical, "each accepted baseline body physically present once")
        want, raw = physical[original], regular(fixtures / row["path"])
        need(raw == want and len(raw) == row["bytes"] == accepted["bytes"]
             and digest(raw) == row["sha256"] == accepted["sha256"], "literal prior body equality; no extra normalization")
        compared.append(dict(row, equal=True, baseline_member=index[original][0]["member"], baseline_original=original))
    return {"archive_sha256": BASELINE_SHA, "archive_manifest_sha256": BASELINE_MANIFEST_SHA,
            "actual_export_manifest_sha256": digest(physical[BASELINE_UI + "/fixtures/fixtures.json"]),
            "literal_byte_equal": True, "count": len(compared), "rows": compared,
            "scope": "Physical37 baseline bodies referenced in accepted date keeper; no historical tree duplicated/extracted"}


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
    binding = binding_admission(repo, args.producer_source)
    if args.check_bridge:
        print(json.dumps(source_bridge(repo, args.producer_source))); return
    need(all(getattr(args, name) is not None for name in ("scratch", "go", "node", "cache", "modcache", "playwright_package", "axe_file", "chrome", "pg_run")), "all explicit existing dependency/run paths required")
    paths = {}
    for name in ("go", "node", "cache", "modcache", "playwright_package", "axe_file", "chrome"):
        value = getattr(args, name)
        need(value.is_absolute(), "absolute existing dependency path required: " + name)
        paths[name] = value.resolve(strict=True)
    admitted_dependencies = dependency_admission(paths, binding)
    bridge_before = source_bridge(repo, args.producer_source)
    need(repo.is_absolute() and repo.resolve() == repo and METHOD == repo / "tools/gui-native-account-center",
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
    binding = json.loads(regular(METHOD / "source-bindings.json"))
    need(digest(regular(repo / BASELINE_KEEPER)) == BASELINE_SHA, "accepted canonical date baseline required")
    scratch = args.scratch
    need(scratch.is_absolute() and scratch.resolve() == scratch and scratch.is_dir() and
         scratch.stat().st_uid == os.getuid() and "_temp" in scratch.parts and
         not scratch.is_relative_to(repo), "existing owned canonical task scratch outside source required")
    pg_run = args.pg_run
    need(pg_run.is_absolute() and pg_run.resolve() == pg_run and pg_run.is_dir() and
         "_temp" in pg_run.parts and pg_run.stat().st_uid == os.getuid(),
         "literal actual owned successful PG result directory required")
    pg_receipt = qualified_pg(repo, pg_run, args.producer_source, binding)
    output = scratch / ("run-" + args.producer_source[:12] + "-" + str(uuid.uuid4()))
    output.mkdir(mode=0o700)
    # Short, exclusively created HOME/TMPDIR sibling: Chromium's Unix socket
    # must not inherit the deeply nested UUID result-directory path.
    temp = Path(tempfile.mkdtemp(prefix="ac-", dir=scratch))
    env = {"PATH": os.defpath, "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
           "HOME": str(temp), "TMPDIR": str(temp), "GOCACHE": str(paths["cache"]),
           "GOMODCACHE": str(paths["modcache"]), "GOTOOLCHAIN": "local", "GOENV": "off",
           "GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-p=2",
           "GOMAXPROCS": "2"}
    report = {
        "schema": 1, "status": "RUNNING", "PG_producer_source": args.producer_source,
        "UI_method_source": head, "committed_method": method_rows, "source_bridge": bridge_before,
        "baseline_keeper": {"path": BASELINE_KEEPER, "sha256": BASELINE_SHA},
        "scope": "Eight fixed synthetic AccountView presentation states, eight no-JS/24 strict whole-document Axe audits and37 literal physical baseline bodies. No token/session/export capture, action submission, authenticated HTTP, language-save or production claim.",
        "qualified_PG": pg_receipt,
        "admitted_dependencies_before_output_or_process": admitted_dependencies,
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
        report["explicit_dependencies"] = dependency_admission(paths, binding)
        need(report["explicit_dependencies"] == admitted_dependencies,
             "admitted dependencies changed during version probes")
        overlay, fixtures = output / "overlay.json", output / "fixtures"
        replacements = {
            "account_center_ui_qa_overlay_test.go": METHOD / "export_test.go.txt",
            "notification_inbox_ui_qa_overlay_test.go": repo / "tools/gui-native-notification-inbox/export_test.go.txt",
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
             "./internal/web", "-run", "^TestSocialAccountCenterQAExport$", "-args",
             "-account-center-ui-producer-source", args.producer_source,
             "-account-center-ui-qa-output-dir", fixtures],
            repo / "services/server", "export", 180)
        export = regular(output / "export.stdout").decode()
        need("=== RUN   TestSocialAccountCenterQAExport" in export and
             "--- PASS: TestSocialAccountCenterQAExport (" in export and
             not re.search(r"--- (FAIL|SKIP):", export) and
             re.search(r"^ok\s+\S+\s+[0-9.]+s$", export, re.M),
             "actual unskipped export root and package PASS required")
        manifest_path = fixtures / "fixtures.json"
        manifest = json.loads(regular(manifest_path))
        need(manifest["PG_producer_source"] == args.producer_source and
             manifest["UI_method_source"] == head and manifest["source_unchanged"] is True and
             manifest["sources_before"] == manifest["sources_after"] and
             [row["name"] for row in manifest["states"]] == STATES,
             "complete exact-source eight-state export differs")
        for row in manifest["states"]:
            raw = regular(fixtures / (row["name"] + ".html"))
            need(len(raw) == row["bytes"] and digest(raw) == row["sha256"],
                 "actual native fixture bytes differ")
        baseline_proof = physical_baseline(repo, fixtures, manifest)
        baseline_path = output / "prior-body-equality.json"
        baseline_path.write_text(json.dumps(baseline_proof, indent=2) + "\n")
        report["previous_body_equality"] = baseline_proof
        report["normalization"] = manifest["normalization"]
        report["fixture_manifest_sha256"] = digest(regular(manifest_path))
        desktop = output / "desktop"
        desktop_attempted = True
        run([paths["node"], METHOD / "desktop-qa.mjs", "--repo", repo, "--producer-source", args.producer_source,
             "--fixtures", fixtures, "--baseline-proof", baseline_path, "--output", desktop, "--playwright-package", paths["playwright_package"],
             "--axe-file", paths["axe_file"], "--chrome", paths["chrome"], "--go", paths["go"], "--node", paths["node"]],
            repo, "desktop", 600)
        actual_path = desktop / "desktop-results.json"
        actual = json.loads(regular(actual_path))
        need(actual["status"] == "pass" and not actual["failures"] and actual["sourceUnchanged"] is True and
             [row["name"] for row in actual["states"]] == STATES and
             [row["name"] for row in actual["noJavaScriptStates"]] == STATES and
             actual["ownership"]["serverStopped"] is True and
             len(actual["ownership"]["profiles"]) == 2 and
             all(row["closed"] is True and row["removed"] is True for row in actual["ownership"]["profiles"]),
             "all eight desktop/no-JS cases and actual owned cleanup must pass")
        desktop_cleanup_proven = True
        report["desktop_report_sha256"] = digest(regular(actual_path))
        if args.benchmark:
            report["benchmark"] = {"status": "REQUESTED_NOT_ADMITTED"}
            run([paths["go"], "test", "-mod=readonly", "-overlay=" + str(overlay), "-run", "^$",
                 "-bench", "^BenchmarkSocialAccountCenterBodyRender$", "-benchmem", "-benchtime=100x",
                 "-count=3", "-cpu=1", "./internal/web", "-args",
                 "-account-center-ui-producer-source", args.producer_source],
                repo / "services/server", "benchmark", 180)
            raw = regular(output / "benchmark.stdout")
            text = raw.decode()
            rows = re.findall(r"^(BenchmarkSocialAccountCenterBodyRender/(?:overview-basic-en|overview-guardian-ro|overview-ward-connections-en|overview-escaped-ro|settings-no-token-en|settings-token-ro|settings-expired-record-en|settings-revoke-error-ro)/(?:classic|native))(?:-[0-9]+)?\s+100\s+", text, re.M)
            expected = Counter({"BenchmarkSocialAccountCenterBodyRender/" + state + "/" + engine: 3
                                for state in STATES for engine in ("classic", "native")})
            need(Counter(rows) == expected and not re.search(r"--- (FAIL|SKIP):", text) and
                 re.search(r"^ok\s+\S+\s+[0-9.]+s$", text, re.M),
                 "actual matched16groups/three samples/100 iterations receipt required")
            report["benchmark"] = {"status": "EXECUTED_SEPARATE_NOT_ADMITTED", "rows": len(rows),
                                   "stdout_sha256": digest(raw),
                                   "scope": "Same fixed synthetic AccountView source per state, warmed classic/native body render only; native typed validation/projection/copy included. Eight states/16groups/48samples100iterations. Markup differs; no genuine token, issuance, HTTP, DB, CPU-profile or generalized speedup acceptance."}
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
