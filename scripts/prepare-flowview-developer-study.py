#!/usr/bin/env python3
"""Prepare real saved FlowViews for the four developer study tasks."""

import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen


REPOSITORY = Path(__file__).resolve().parents[1]


def write_source(root: Path, name: str, source: str) -> None:
    destination = root / name
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(source, encoding="utf-8")


def read_json(base_url: str, token: str, route: str) -> dict:
    request = Request(base_url + route, headers={"X-CodeFlow-Token": token})
    try:
        with urlopen(request, timeout=60) as response:
            return json.load(response)
    except HTTPError as error:
        details = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{route}: HTTP {error.code}: {details}") from error


def analyze(base_url: str, token: str, entry: str) -> dict:
    view = read_json(base_url, token, "/api/task/view?" + urlencode({"entrySymbol": entry}))
    if not view.get("viewId") or not view.get("semanticMap", {}).get("steps"):
        raise RuntimeError(f"{entry}: analysis did not create a readable saved view")
    return view


def require_source(view: dict, path: str) -> None:
    steps = view["semanticMap"]["steps"]
    if not any(step.get("anchor", {}).get("repoRelativePath") == path for step in steps):
        raise RuntimeError(f"{path}: source steps are missing")
    contexts = view.get("flowContexts", {})
    for step in steps:
        if step.get("anchor", {}).get("repoRelativePath") == path:
            if contexts.get(step["stepId"], {}).get("precision") not in ("exact", "verified"):
                raise RuntimeError(f"{path}: selected code is not exact or verified")


def prepare_views(root: Path, base_url: str, token: str) -> dict[str, dict]:
    write_source(root, "package.json", '{"name":"flowview-developer-study","private":true,"type":"module"}\n')
    write_source(root, "pubspec.yaml", "name: flowview_developer_study\nenvironment:\n  sdk: ^3.0.0\n")

    write_source(root, "app/payment-flow.ts", """import { chargeCard } from './card';
import { transferFunds } from './bank';
import { receipt } from './receipt';
export function checkoutPayment(card: boolean, amount: number) {
  let payment;
  if (card) { payment = chargeCard(amount); }
  else { payment = transferFunds(amount); }
  return receipt(payment);
}
""")
    write_source(root, "app/card.ts", "export function chargeCard(amount: number) { return { method: 'card', amount }; }\n")
    write_source(root, "app/bank.ts", "export function transferFunds(amount: number) { return { method: 'bank', amount }; }\n")
    write_source(root, "app/receipt.ts", "export function receipt(payment: unknown) { return { payment }; }\n")
    payment = analyze(base_url, token, "app/payment-flow.ts#checkoutPayment")
    for path in ("app/payment-flow.ts", "app/card.ts", "app/bank.ts", "app/receipt.ts"):
        require_source(payment, path)
    step_names = {step["name"] for step in payment["semanticMap"]["steps"]}
    if not {"if (card)", "chargeCard(amount)", "transferFunds(amount)", "receipt(payment)"}.issubset(step_names):
        raise RuntimeError("payment alternatives or shared receipt are missing")
    by_name = {step["name"]: step["stepId"] for step in payment["semanticMap"]["steps"]}
    for outcome, target in (("truthy", "chargeCard(amount)"), ("falsy", "transferFunds(amount)")):
        if not any(
            edge.get("fromStepId") == by_name["if (card)"]
            and edge.get("toStepId") == by_name[target]
            and any(condition.get("outcome") == outcome for condition in edge.get("conditions", []))
            for edge in payment["semanticMap"]["edges"]
        ):
            raise RuntimeError(f"payment {outcome} branch is missing")
    memberships = [step for frame in payment["flowSequence"]["frames"] for step in frame["stepRefs"]]
    if memberships.count(by_name["receipt(payment)"]) != 1:
        raise RuntimeError("shared receipt does not belong to exactly one gateway")

    write_source(root, "app/cross-file-flow.ts", """import { evaluatePayment } from './payment-helper';
export async function checkoutCrossFile(request: {allowed: boolean}) {
  return await evaluatePayment(request);
}
""")
    write_source(root, "app/payment-helper.ts", """export async function evaluatePayment(request: {allowed: boolean}) {
  if (!request.allowed) return 'denied';
  return 'approved';
}
""")
    cross_file = analyze(base_url, token, "app/cross-file-flow.ts#checkoutCrossFile")
    require_source(cross_file, "app/payment-helper.ts")

    before = """export function updateStatus(setStatus: (value: string) => void) {
  setStatus('completed');
}
"""
    after = before.replace("setStatus('completed')", "setStatus('confirmed')")
    write_source(root, "app/status.ts", before)
    baseline = analyze(base_url, token, "app/status.ts#updateStatus")
    write_source(root, "app/status.ts", after)
    current = analyze(base_url, token, "app/status.ts#updateStatus")
    if baseline["viewId"] == current["viewId"]:
        raise RuntimeError("status source edit did not create a second saved analysis")
    for expected, view in (("setStatus('completed')", baseline), ("setStatus('confirmed')", current)):
        if not any(expected in step["name"] for step in view["semanticMap"]["steps"]):
            raise RuntimeError(f"status change {expected} is missing from the saved analysis")
    require_source(current, "app/status.ts")
    read_json(base_url, token, "/api/view/compare?" + urlencode({"viewId": current["viewId"], "baselineId": baseline["viewId"]}))

    write_source(root, "lib/callback.dart", """import 'dart:async';
bool done = false;
class Service {
  void run() {
    final timer = Timer(const Duration(milliseconds: 10), () {
      if (done) return;
    });
    final finished = true;
  }
}
""")
    callback = analyze(base_url, token, "lib/callback.dart#Service.run")
    require_source(callback, "lib/callback.dart")
    if not any(item.get("code") == "invocation_unproven" for item in callback.get("flowSequence", {}).get("summaryLimitations", [])):
        raise RuntimeError("callback delivery has no explicit unproven-invocation boundary")

    return {"task1": payment, "task2": cross_file, "task3-current": current, "task3-baseline": baseline, "task4": callback}


def wait_for_server(process: subprocess.Popen, base_url: str, token: str) -> None:
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"CodeFlow server exited with status {process.returncode}")
        try:
            read_json(base_url, token, "/api/views")
            return
        except (URLError, RuntimeError):
            time.sleep(0.2)
    raise RuntimeError("CodeFlow server did not become ready within 90 seconds")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=4594)
    parser.add_argument("--check", action="store_true", help="verify all study views and then stop the server")
    args = parser.parse_args()
    if not 1 <= args.port <= 65535:
        parser.error("port must be between 1 and 65535")

    token = secrets.token_urlsafe(24)
    base_url = f"http://127.0.0.1:{args.port}"
    with tempfile.TemporaryDirectory(prefix="codeflow-developer-study-") as temporary:
        root = Path(temporary)
        log_path = root / "server.log"
        environment = os.environ.copy()
        environment.setdefault("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:" + str(REPOSITORY / "adapters/typescript"))
        environment.setdefault("CODEFLOW_ADAPTER_DART_BIN", "dartrun:" + str(REPOSITORY / "adapters/dart"))
        with log_path.open("wb") as log:
            process = subprocess.Popen(
                ["go", "run", "./cmd/codeflow", "view", str(root), "--port", str(args.port), "--token", token],
                cwd=REPOSITORY,
                env=environment,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            try:
                wait_for_server(process, base_url, token)
                views = prepare_views(root, base_url, token)
                if args.check:
                    print("All four saved study cases and the comparison baseline passed preparation checks.")
                    return 0
                print("FlowView developer study is ready. Keep this process running during the study.", flush=True)
                for task, view in views.items():
                    address = base_url + "/?" + urlencode({"token": token, "viewId": view["viewId"]})
                    if task != "task3-baseline":
                        print(f"{task} default: {address}", flush=True)
                        print(f"{task} code-focused: {address}&studyLayout=code-focused", flush=True)
                    else:
                        print(f"{task}: {address}", flush=True)
                print("Press Ctrl+C after the participant session.", flush=True)
                while process.poll() is None:
                    time.sleep(0.5)
                raise RuntimeError(f"CodeFlow server exited with status {process.returncode}")
            except KeyboardInterrupt:
                return 0
            except Exception:
                print(log_path.read_text(encoding="utf-8", errors="replace")[-4000:], file=sys.stderr)
                raise
            finally:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()


if __name__ == "__main__":
    raise SystemExit(main())
