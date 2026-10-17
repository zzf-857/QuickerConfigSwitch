#!/usr/bin/env python3
"""Check QCS-generated catalogs with an installed Codex app-server, offline.

Requires Python 3.11+ and explicit executable paths. Only temporary configuration,
fake credentials, custom providers and local model catalogs are used. A fake
loopback Responses endpoint also checks Fast request forwarding without a local
OpenAI login. This does not exercise a real account, desktop UI, model inference
or CPA deployment.

Example:
    python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex
"""

import argparse
from contextlib import contextmanager
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import queue
import subprocess
import tempfile
import threading
import time
import tomllib


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(command, env):
    result = subprocess.run(
        command, env=env, cwd=env["HOME"], capture_output=True, text=True,
        timeout=30, check=False,
    )
    require(result.returncode == 0, f"{command[1]} failed: {result.stderr.strip()}")
    return result.stdout


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def client_models(codex, env, log_path, *, run_turn=False):
    """Read actual metadata; optionally run one turn against a fake endpoint."""
    # The custom provider uses only the fixture endpoint. A generated catalog
    # skips refresh; absent one, Codex uses its bundled API-provider catalog.
    config = tomllib.loads((Path(env["CODEX_HOME"]) / "config.toml").read_text())
    require(config["model_provider"] == "qcs-fixture", "expected fixture provider")
    if config.get("model_catalog_json"):
        require(Path(config["model_catalog_json"]).is_file(), "expected local catalog")
    replies = queue.Queue()
    notifications = []
    with log_path.open("w") as error_log:
        process = subprocess.Popen(
            [codex, "app-server", "--stdio", "--strict-config"],
            env=env, cwd=env["HOME"], stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=error_log, text=True, bufsize=1,
        )

        def read_lines():
            for line in process.stdout:
                replies.put(line)
            replies.put(None)

        reader = threading.Thread(target=read_lines, daemon=True)
        reader.start()

        def send(value):
            process.stdin.write(json.dumps(value) + "\n")
            process.stdin.flush()

        def receive(ids):
            results = {}
            deadline = time.monotonic() + 30
            while set(results) != set(ids):
                remaining = deadline - time.monotonic()
                require(remaining > 0, "Codex app-server response timed out")
                try:
                    line = replies.get(timeout=remaining)
                except queue.Empty as exc:
                    raise RuntimeError("Codex app-server response timed out") from exc
                require(line is not None, "Codex app-server exited before replying")
                reply = json.loads(line)
                if reply.get("id") not in ids:
                    notifications.append(reply)
                    continue
                require("error" not in reply, f"Codex RPC error: {reply.get('error')}")
                results[reply["id"]] = reply["result"]
            return results

        try:
            send({"id": 1, "method": "initialize", "params": {
                "clientInfo": {"name": "qcs-contract-check", "version": "1.0"},
            }})
            initialized = receive({1})[1]
            send({"method": "initialized"})
            send({"id": 2, "method": "config/read", "params": {}})
            send({"id": 3, "method": "model/list", "params": {}})
            results = receive({2, 3})
            if run_turn:
                send({"id": 4, "method": "thread/start", "params": {
                    "cwd": env["HOME"], "approvalPolicy": "never",
                    "sandbox": "read-only", "ephemeral": True,
                    "baseInstructions": "Temporary fixture. Reply with fixture text only.",
                }})
                thread_id = receive({4})[4]["thread"]["id"]
                send({"id": 5, "method": "turn/start", "params": {
                    "threadId": thread_id,
                    "input": [{"type": "text", "text": "Reply with fixture text only."}],
                }})
                receive({5})
                deadline = time.monotonic() + 30
                while True:
                    if notifications:
                        notification = notifications.pop(0)
                    else:
                        remaining = deadline - time.monotonic()
                        require(remaining > 0, "Codex fixture turn timed out")
                        try:
                            line = replies.get(timeout=remaining)
                        except queue.Empty as exc:
                            raise RuntimeError("Codex fixture turn timed out") from exc
                        require(line is not None, "Codex exited before completing fixture turn")
                        notification = json.loads(line)
                    if notification.get("method") != "turn/completed":
                        continue
                    params = notification["params"]
                    if params["threadId"] != thread_id:
                        continue
                    require(params["turn"]["status"] == "completed",
                            f"Codex fixture turn failed: {params['turn'].get('error')}")
                    break
            return initialized, results[2]["config"], results[3]["data"]
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            process.stdin.close()
            process.stdout.close()
            reader.join(timeout=1)


@contextmanager
def fake_cpa():
    """Serve only a canned Responses stream; no model or tool is executed."""
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            if self.path != "/v1/models":
                self.send_error(404)
                return
            payload = json.dumps({"data": [{"id": "gpt-5.5", "object": "model"}]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            requests.append((self.path, dict(self.headers), json.loads(body)))
            message = {
                "type": "message", "id": "msg_fixture", "role": "assistant",
                "status": "completed", "content": [{
                    "type": "output_text", "text": "Fixture complete.", "annotations": [],
                }],
            }
            response = {
                "id": "resp_fixture", "object": "response", "created_at": 1,
                "status": "completed", "model": "gpt-5.5", "output": [message],
                "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
            }
            events = [
                {"type": "response.created", "response": {
                    **response, "status": "in_progress", "output": [],
                }},
                {"type": "response.output_item.added", "output_index": 0,
                 "item": {**message, "status": "in_progress", "content": []}},
                {"type": "response.output_text.delta", "item_id": "msg_fixture",
                 "output_index": 0, "content_index": 0, "delta": "Fixture complete."},
                {"type": "response.output_item.done", "output_index": 0, "item": message},
                {"type": "response.completed", "response": response},
            ]
            payload = "".join("event: " + event["type"] + "\ndata: " + json.dumps(event)
                              + "\n\n" for event in events).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/v1", requests
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)


def fixture_env(root):
    # Keep proxy/CA and OS runtime settings, while removing parent account,
    # provider and Codex configuration selectors from the child process only.
    account_prefixes = (
        "OPENAI_", "CODEX_", "CHATGPT_", "QCS_", "CLAUDE_", "ANTHROPIC_",
        "AZURE_OPENAI_", "OPENROUTER_", "OLLAMA_", "LMSTUDIO_", "LITELLM_",
    )
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(account_prefixes)}
    for name, relative in {
        "HOME": "home", "USERPROFILE": "home", "QCS_HOME": "qcs",
        "CODEX_HOME": "codex", "CLAUDE_CONFIG_DIR": "claude",
        "XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache",
        "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state",
        "XDG_RUNTIME_DIR": "runtime", "APPDATA": "appdata",
        "LOCALAPPDATA": "localappdata", "TMPDIR": "tmp",
    }.items():
        path = root / relative
        path.mkdir(parents=True, mode=0o700, exist_ok=True)
        env[name] = str(path)
    return env


def check_case(qcs, codex, root, *, model, listed, label):
    env = fixture_env(root)
    codex_home = Path(env["CODEX_HOME"])
    config_path = codex_home / "config.toml"
    auth_path = codex_home / "auth.json"
    auth_path.write_text('{"auth_mode":"chatgpt","tokens":'
                         '{"access_token":"fixture-only-no-real-credential"}}\n')
    auth_path.chmod(0o600)
    auth_before = digest(auth_path), auth_path.stat().st_mode
    original_catalog = codex_home / "original-models.json"
    original_bytes = (json.dumps({"models": [{
        "slug": "original-model", "display_name": "Original fixture model",
        "base_instructions": "Temporary contract fixture.",
        "supported_reasoning_levels": [], "shell_type": "unified_exec",
        "visibility": "list", "supported_in_api": True, "priority": 1,
        "support_verbosity": False,
        "truncation_policy": {"mode": "tokens", "limit": 10000},
        "experimental_supported_tools": [], "input_modalities": ["text"],
        "supports_reasoning_summaries": False, "supports_parallel_tool_calls": False,
        "service_tiers": [{"id": "priority", "name": "Fast", "description": "Fixture"}],
    }]}) + "\n").encode()
    original_catalog.write_bytes(original_bytes)
    # This original file is restored byte for byte; it is never loaded by Codex.
    original = (
        '# original user configuration\nmodel = "original-model"\n'
        'service_tier = "fast"\nmodel_reasoning_effort = "high"\n'
        'cli_auth_credentials_store = "file"\n'
        'check_for_update_on_startup = false\n'
        f'model_catalog_json = {json.dumps(str(original_catalog))}\n'
        '[features]\nfast_mode = true\n'
        '[analytics]\nenabled = false\n'
        '[mcp_servers.fixture]\ncommand = "fixture-never-executed"\n'
    )
    config_path.write_text(original)
    run([qcs, "provider", "add", "fixture", "--base", "http://127.0.0.1:9/v1",
         "--key", "fixture-only"], env)
    if listed:
        run([qcs, "provider", "models", "fixture", "--add", model,
             "--fast", model + "=true"], env)
    run([qcs, "use", "codex", "fixture/" + model, "--yes"], env)
    written = tomllib.loads(config_path.read_text())
    require(written["service_tier"] == "fast", "QCS changed user's Fast choice")
    require(written["features"] == {"fast_mode": True}, "QCS changed features")
    require(written["model_reasoning_effort"] == "high", "QCS changed effort")
    require(written["mcp_servers"]["fixture"]["command"] == "fixture-never-executed",
            "QCS changed unrelated MCP settings")
    initialized, effective, models = client_models(codex, env, root / "client.stderr")
    require(effective["service_tier"] == "fast", "Codex did not load Fast choice")
    require(effective["features"]["fast_mode"] is True, "Codex did not load Fast feature")
    require(effective["model_provider"] == "qcs-fixture", "Codex loaded another provider")
    match = next((entry for entry in models if entry["model"] == model), None)
    require(match is not None, "Codex did not list generated custom model")
    require(any(tier["id"] == "priority" for tier in match["serviceTiers"]),
            "Codex did not expose Fast metadata")
    require((digest(auth_path), auth_path.stat().st_mode) == auth_before,
            "switch/client read modified fixture auth file")
    run([qcs, "use", "codex", "official", "--yes"], env)
    restored = tomllib.loads(config_path.read_text())
    for key in ("model", "service_tier", "model_reasoning_effort", "model_catalog_json",
                "features", "analytics", "check_for_update_on_startup", "mcp_servers"):
        require(restored[key] == tomllib.loads(original)[key], f"restore changed {key}")
    require(original_catalog.read_bytes() == original_bytes, "original catalog was modified")
    require((digest(auth_path), auth_path.stat().st_mode) == auth_before,
            "restore modified fixture auth file")
    table = restored["model_providers"]["qcs-fixture"]
    require("experimental_bearer_token" not in table, "restore retained fixture API key")
    print(f"PASS: {label}; Fast metadata, original settings/catalog and auth preserved")
    return initialized["userAgent"]


def check_no_login_case(qcs, codex, root, *, fetch_models):
    env = fixture_env(root)
    codex_home = Path(env["CODEX_HOME"])
    config_path = codex_home / "config.toml"
    auth_path = codex_home / "auth.json"
    original = (
        '# original configuration without an OpenAI login\nmodel = "gpt-5.5"\n'
        'service_tier = "fast"\nmodel_reasoning_effort = "high"\n'
        'cli_auth_credentials_store = "file"\n'
        'check_for_update_on_startup = false\n'
        '[features]\nfast_mode = true\n'
        '[analytics]\nenabled = false\n'
    )
    config_path.write_text(original)
    require(not auth_path.exists(), "no-login fixture unexpectedly has auth.json")
    with fake_cpa() as (base_url, requests):
        run([qcs, "provider", "add", "fixture", "--base", base_url,
             "--key", "fixture-cpa-key"], env)
        if fetch_models:
            run([qcs, "provider", "models", "fixture", "--fetch"], env)
        run([qcs, "use", "codex", "fixture/gpt-5.5", "--yes"], env)
        written = tomllib.loads(config_path.read_text())
        require(written["service_tier"] == "fast", "QCS changed user's Fast choice")
        require(written["model_reasoning_effort"] == "high", "QCS changed reasoning effort")
        if fetch_models:
            require("model_catalog_json" in written,
                    "CPA model list needs a catalog even when every model is native")
        require(not auth_path.exists(), "QCS created local login credentials")
        initialized, effective, models = client_models(
            codex, env, root / "client.stderr", run_turn=True,
        )
        require(effective["model_provider"] == "qcs-fixture", "Codex loaded another provider")
        require(effective["service_tier"] == "fast", "Codex did not load Fast choice")
        require(effective["features"]["fast_mode"] is True, "Codex did not load Fast feature")
        match = next((entry for entry in models if entry["model"] == "gpt-5.5"), None)
        require(match is not None and match["displayName"] == "GPT-5.5",
                "Codex did not preserve the native GPT name")
        require(any(tier["id"] == "priority" for tier in match["serviceTiers"]),
                "Codex did not expose Fast without a local OpenAI login")
        require({entry["reasoningEffort"] for entry in match["supportedReasoningEfforts"]}
                == {"low", "medium", "high", "xhigh"},
                "Codex lost native GPT reasoning levels")
        if fetch_models:
            require({entry["model"] for entry in models} == {"gpt-5.5"},
                    "Codex picker includes models absent from the CPA list")
        require(len(requests) == 1, "expected one canned Responses request")
        path, raw_headers, body = requests[0]
        headers = {key.lower(): value for key, value in raw_headers.items()}
        require(path == "/v1/responses", "Codex used an unexpected CPA route")
        require(body["model"] == "gpt-5.5", "Codex changed the upstream model ID")
        require(body["service_tier"] == "priority", "Fast did not reach CPA as priority")
        require(headers.get("authorization") == "Bearer fixture-cpa-key",
                "Codex did not use the CPA key")
        require(not any(name in headers for name in (
            "chatgpt-account-id", "openai-account-id", "x-openai-actor-authorization",
            "cookie", "x-oai-attestation",
        )), "Codex forwarded unexpected official-account authentication")
        require(not auth_path.exists(), "Codex created local login credentials")
    run([qcs, "use", "codex", "official", "--yes"], env)
    restored = tomllib.loads(config_path.read_text())
    table = restored.pop("model_providers")["qcs-fixture"]
    require(restored == tomllib.loads(original), "restore changed original settings")
    require(config_path.read_text().startswith(original), "restore changed original config bytes")
    require("experimental_bearer_token" not in table, "restore retained the CPA key")
    require(not auth_path.exists(), "restore created local login credentials")
    label = "fetched CPA native-model subset" if fetch_models else "new CPA without fetched models"
    print(f"PASS: no local login, {label}; native GPT/Fast, priority request and restore")
    return initialized["userAgent"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--qcs", required=True, help="path to a built QCS executable")
    parser.add_argument("--codex", required=True, help="path to an installed Codex executable")
    args = parser.parse_args()
    qcs, codex = str(Path(args.qcs).resolve()), str(Path(args.codex).resolve())
    require(Path(qcs).is_file() and Path(codex).is_file(), "executables must exist")
    with tempfile.TemporaryDirectory(prefix="qcs-codex-contract-") as temporary:
        root = Path(temporary)
        cases = [
            ("new-provider", "vendor/gpt-5.5", False, "new provider without fetched models"),
            ("listed-provider", "contract-check-custom", True, "listed custom model"),
            ("bundled-model", "gpt-5.5", False, "bundled model over original custom catalog"),
        ]
        for name, model, listed, label in cases:
            version = check_case(qcs, codex, root / name, model=model, listed=listed, label=label)
        for fetch_models in (False, True):
            version = check_no_login_case(qcs, codex, root / ("no-login-" + str(fetch_models)),
                                          fetch_models=fetch_models)
        print("Client: " + version)
        print("Only temporary settings and a fake loopback Responses stream were used; "
              "no real inference, account or CPA deployment check.")


if __name__ == "__main__":
    main()
