#!/usr/bin/env python3
"""Check retained ChatGPT client state and CPA-only model traffic, offline.

Requires a POSIX host, Python 3.11+, QCS, Codex app-server and the test-only
relay fixture. Validated on Linux; Windows termination does not run the
fixture's signal cleanup and is not supported by this standalone check.
The login is fabricated, the official and CPA servers are loopback fixtures,
and the Responses stream is canned. This checks the real client's local auth,
model and request contracts, not a real subscription, desktop UI or CPA server.

Build the fixture from the repository root:
    go build -tags nogui -o /tmp/qcs-relay-fixture ./scripts/fixtures/codex-relay
Run:
    python3 scripts/check_codex_relay_contract.py --qcs ./qcs --codex /path/to/codex \
        --relay-fixture /tmp/qcs-relay-fixture
"""

import argparse
import base64
from collections import deque
from contextlib import contextmanager
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import tempfile
import threading
import time
import tomllib

# Running the standalone check should not leave import artifacts in the checkout.
sys.dont_write_bytecode = True
from check_codex_contract import fake_cpa, fixture_env, require, run


@contextmanager
def fake_official():
    catalog = Path(__file__).resolve().parents[1] / "internal/codexcat/bundled_models.json"
    model = next(entry for entry in json.loads(catalog.read_text())["models"]
                 if entry["slug"] == "gpt-5.5")
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            requests.append((self.path, {key.lower(): value for key, value in self.headers.items()}))
            path = self.path.split("?")[0]
            if path == "/backend-api/codex/models":
                data, status = {"models": [model]}, 200
            elif path == "/api/codex/accounts/check":
                # Modern Codex requires a valid HTTPS workspace origin. This is
                # routing metadata only: the client's HTTP proxy cannot reach it.
                data, status = {"accounts": [{
                    "id": "fixture-account", "plan_type": "pro",
                    "workspace_backend_origin": "https://chatgpt.com",
                    "account_routing_override": "NO_CONSTRAINT",
                }], "account_ordering": ["fixture-account"],
                    "default_account_id": "fixture-account"}, 200
            else:
                data, status = {}, 404
            payload = json.dumps(data).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            try:
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError):
                pass  # The app-server can cancel optional background reads.

        def do_POST(self):
            requests.append((self.path, {key.lower(): value for key, value in self.headers.items()}))
            self.send_error(500, "fixture refuses official model requests")

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}", requests
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)


def client_turn(codex, env, log_path):
    replies, notifications = queue.Queue(), deque()
    with log_path.open("w") as error_log:
        process = subprocess.Popen(
            [codex, "app-server", "--stdio", "--strict-config"], env=env,
            cwd=env["HOME"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=error_log, text=True, bufsize=1,
        )

        def read_lines():
            for line in process.stdout:
                replies.put(line)
            replies.put(None)

        reader = threading.Thread(target=read_lines, daemon=True)
        reader.start()
        request_id = 0

        def next_reply(deadline):
            remaining = deadline - time.monotonic()
            require(remaining > 0, "Codex app-server response timed out")
            try:
                line = replies.get(timeout=remaining)
            except queue.Empty as exc:
                raise RuntimeError("Codex app-server response timed out") from exc
            require(line is not None, "Codex app-server exited before replying")
            return json.loads(line)

        def request(method, params):
            nonlocal request_id
            request_id += 1
            process.stdin.write(json.dumps({"id": request_id, "method": method,
                                            "params": params}) + "\n")
            process.stdin.flush()
            deadline = time.monotonic() + 30
            while True:
                reply = next_reply(deadline)
                if reply.get("id") != request_id:
                    notifications.append(reply)
                    continue
                require("error" not in reply, f"Codex RPC error: {reply.get('error')}")
                return reply["result"]

        try:
            initialized = request("initialize", {"clientInfo": {
                "name": "qcs-relay-contract-check", "version": "1.0",
            }})
            process.stdin.write('{"method":"initialized"}\n')
            process.stdin.flush()
            account = request("account/read", {"refreshToken": False})
            config = request("config/read", {})["config"]
            models = request("model/list", {})["data"]
            thread_id = request("thread/start", {
                "cwd": env["HOME"], "approvalPolicy": "never", "sandbox": "read-only",
                "ephemeral": True, "baseInstructions": "Temporary fixture. Reply with fixture text only.",
            })["thread"]["id"]
            request("turn/start", {"threadId": thread_id, "input": [{
                "type": "text", "text": "Reply with fixture text only.",
            }]})
            deadline = time.monotonic() + 30
            while True:
                reply = notifications.popleft() if notifications else next_reply(deadline)
                if reply.get("method") != "turn/completed":
                    continue
                if reply["params"]["threadId"] != thread_id:
                    continue
                turn = reply["params"]["turn"]
                require(turn["status"] == "completed", f"Codex fixture turn failed: {turn.get('error')}")
                return initialized, account, config, models
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


def auth_identity(path):
    stat = path.stat()
    return path.read_bytes(), stat.st_ino, stat.st_mtime_ns, stat.st_mode


def check(qcs, codex, relay_fixture, root):
    env = fixture_env(root)
    with fake_official() as (official_url, official_requests), fake_cpa() as (cpa_url, cpa_requests):
        # Any unintended external HTTP/TLS request reaches a refusing loopback
        # proxy. Only the two fixtures and the production relay bypass it.
        for name in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
            env[name] = official_url
        env["NO_PROXY"] = env["no_proxy"] = "127.0.0.1,localhost"
        env.update({"QCS_RELAY_FIXTURE_ROOT": str(root),
                    "QCS_RELAY_FIXTURE_OFFICIAL": official_url, "QCS_RELAY_FIXTURE_CPA": cpa_url})
        config_path, auth_path = Path(env["CODEX_HOME"]) / "config.toml", Path(env["CODEX_HOME"]) / "auth.json"
        original = (
            '# original ChatGPT subscription fixture\nmodel_provider = "openai"\n'
            'model = "gpt-5.5"\nservice_tier = "fast"\nmodel_reasoning_effort = "high"\n'
            'cli_auth_credentials_store = "file"\ncheck_for_update_on_startup = false\n'
            f'chatgpt_base_url = {json.dumps(official_url)}\n'
            '[features]\nfast_mode = true\n[analytics]\nenabled = false\n'
        ).encode()
        config_path.write_bytes(original)
        claims = {"sub": "fixture-user", "email": "fixture@example.invalid", "exp": 4102444800,
                  "https://api.openai.com/auth": {"chatgpt_account_id": "fixture-account",
                  "chatgpt_user_id": "fixture-user", "chatgpt_plan_type": "pro"}}
        encoded = base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip("=")
        auth_path.write_text(json.dumps({"auth_mode": "chatgpt", "OPENAI_API_KEY": None, "tokens": {
            "id_token": "eyJhbGciOiJIUzI1NiJ9." + encoded + ".fixture-signature",
            "access_token": "fixture-chatgpt-access", "refresh_token": "fixture-refresh",
            "account_id": "fixture-account",
        }, "last_refresh": datetime.now(timezone.utc).isoformat()}))
        auth_path.chmod(0o600)
        auth_before = auth_identity(auth_path)
        run([qcs, "provider", "add", "fixture", "--base", cpa_url, "--key", "fixture-cpa-key"], env)
        run([qcs, "provider", "models", "fixture", "--add", "gpt-5.5", "--fast", "gpt-5.5=true"], env)
        with (root / "relay.stderr").open("w") as relay_log:
            relay = subprocess.Popen([relay_fixture], env=env, cwd=env["HOME"],
                                     stdout=subprocess.PIPE, stderr=relay_log, text=True)
            try:
                ready = queue.Queue()
                threading.Thread(target=lambda: ready.put(relay.stdout.readline()), daemon=True).start()
                try:
                    first = ready.get(timeout=30)
                except queue.Empty as exc:
                    raise RuntimeError("relay fixture did not become ready") from exc
                require(bool(first), "relay fixture exited before becoming ready")
                relay_url = json.loads(first)["url"]
                written = tomllib.loads(config_path.read_text())
                require(written.pop("openai_base_url") == relay_url, "Codex points to another relay")
                require(written == tomllib.loads(original.decode()), "relay changed other original settings")
                initialized, account, config, models = client_turn(codex, env, root / "client.stderr")
                require(account["requiresOpenaiAuth"] and account["account"]["type"] == "chatgpt"
                        and account["account"]["planType"] == "pro", "Codex lost its ChatGPT account state")
                require(config["model_provider"] == "openai" and config["service_tier"] == "fast",
                        "Codex lost the built-in provider or Fast selection")
                model = next((entry for entry in models if entry["model"] == "gpt-5.5"), None)
                require(model is not None and model["displayName"] == "GPT-5.5"
                        and any(tier["id"] == "priority" for tier in model["serviceTiers"]),
                        "Codex lost its native GPT name or Fast metadata")
                require(len(cpa_requests) == 1, "expected exactly one CPA Responses request")
                path, raw_headers, body = cpa_requests[0]
                headers = {key.lower(): value for key, value in raw_headers.items()}
                require(path == "/v1/responses" and body["model"] == "gpt-5.5"
                        and body["service_tier"] == "priority", "CPA received the wrong model, route or Fast tier")
                require(headers.get("authorization") == "Bearer fixture-cpa-key"
                        and not any(name in headers for name in (
                            "chatgpt-account-id", "openai-account-id", "cookie",
                            "x-openai-actor-authorization", "x-oai-attestation",
                        )), "official-account authentication leaked to CPA")
                require(any(path.startswith("/backend-api/codex/models")
                            and headers.get("authorization") == "Bearer fixture-chatgpt-access"
                            and headers.get("chatgpt-account-id") == "fixture-account"
                            for path, headers in official_requests), "official model-list auth was not preserved")
                require(not any("/responses" in path for path, _ in official_requests),
                        "model inference escaped to the official backend")
                require(auth_identity(auth_path) == auth_before, "client or relay changed login identity")
            finally:
                relay.terminate()
                try:
                    relay.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    relay.kill()
                    relay.wait(timeout=5)
                relay.stdout.close()
        require(relay.returncode == 0, "relay fixture failed to close cleanly")
        require(config_path.read_bytes() == original, "relay did not restore the exact original config")
        require(auth_identity(auth_path) == auth_before, "relay cleanup changed login identity")
        print("PASS: fixture ChatGPT account remains active with built-in OpenAI/native GPT/Fast")
        print("PASS: one CPA Responses request with priority and CPA-only authentication; "
              "official model listing keeps original auth")
        print("PASS: login bytes/inode/mtime/mode preserved and original config restored exactly")
        print("Client: " + initialized["userAgent"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--qcs", required=True)
    parser.add_argument("--codex", required=True)
    parser.add_argument("--relay-fixture", required=True)
    args = parser.parse_args()
    if os.name != "posix":
        parser.error("this check requires POSIX signal cleanup; Windows is not supported")
    paths = [str(Path(value).resolve()) for value in (args.qcs, args.codex, args.relay_fixture)]
    require(all(Path(path).is_file() for path in paths), "executables must exist")
    with tempfile.TemporaryDirectory(prefix="qcs-codex-relay-contract-") as temporary:
        check(*paths, Path(temporary).resolve())
    print("Only fabricated login and loopback servers were used; no real subscription or desktop acceptance.")


if __name__ == "__main__":
    main()
