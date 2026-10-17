#!/usr/bin/env python3
"""Exercise QCS-generated settings in an installed Claude Code, fixture only.

Python 3.11+, explicit executable paths, no package installation. Uses temporary
homes, fake OAuth/API keys and a loopback Anthropic SSE backend. This checks the
CLI's actual config/request contract, not a real account or remote inference.

    python3 scripts/check_claude_contract.py --qcs ./qcs --claude /path/to/claude
"""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import time


CLOUDS = [
    "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
    "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD",
    "CLAUDE_CODE_USE_MANTLE",
]


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(command, env, success=True):
    result = subprocess.run(command, env=env, cwd=env["HOME"], capture_output=True,
                            text=True, timeout=30, check=False)
    require((result.returncode == 0) == success,
            f"{Path(command[0]).name} command returned an unexpected status")
    return result


class Backend(BaseHTTPRequestHandler):
    requests = []

    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"data":[],"has_more":false}')

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        self.requests.append({"path": self.path, "body": body,
                              "headers": {k.lower(): v for k, v in self.headers.items()}})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        message = {"id": "msg_fixture", "type": "message", "role": "assistant",
                   "model": body["model"], "content": [], "stop_reason": None,
                   "stop_sequence": None, "usage": {"input_tokens": 1, "output_tokens": 0}}
        events = [
            {"type": "message_start", "message": message},
            {"type": "content_block_start", "index": 0,
             "content_block": {"type": "text", "text": ""}},
            {"type": "content_block_delta", "index": 0,
             "delta": {"type": "text_delta", "text": "OK"}},
            {"type": "content_block_stop", "index": 0},
            {"type": "message_delta", "delta": {"stop_reason": "end_turn",
             "stop_sequence": None}, "usage": {"output_tokens": 1}},
            {"type": "message_stop"},
        ]
        for event in events:
            self.wfile.write((f"event: {event['type']}\ndata: {json.dumps(event)}\n\n").encode())
            self.wfile.flush()


def isolated_env(root):
    # Keep the session's proxy/CA/auth transport, remove only application
    # account/provider selectors and redirect every client configuration path.
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(("ANTHROPIC_", "CLAUDE_", "AWS_", "GOOGLE_", "CLOUD_ML_"))}
    env.update({
        "HOME": str(root), "USERPROFILE": str(root),
        "CLAUDE_CONFIG_DIR": str(root / ".claude"), "QCS_HOME": str(root / "qcs"),
        "CODEX_HOME": str(root / ".codex"), "APPDATA": str(root / "AppData"),
        "LOCALAPPDATA": str(root / "LocalAppData"),
        "XDG_CONFIG_HOME": str(root / ".config"), "XDG_CACHE_HOME": str(root / ".cache"),
        "XDG_DATA_HOME": str(root / ".data"), "XDG_STATE_HOME": str(root / ".state"),
        "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1",
        "DISABLE_NON_ESSENTIAL_MODEL_CALLS": "1",
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
        "CLAUDE_CODE_ENABLE_TELEMETRY": "0", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
        "CLAUDE_CODE_SKIP_MODEL_ACCESS_MEMORY": "1",
    })
    return env


def client(claude, env):
    before = len(Backend.requests)
    result = run([claude, "--print", "--output-format", "json", "--setting-sources", "user",
                  "--tools", "", "--no-session-persistence", "--max-turns", "1", "Say OK"], env)
    require(json.loads(result.stdout)["result"] == "OK", "fixture client turn did not complete")
    messages = [r for r in Backend.requests[before:] if r["path"].startswith("/v1/messages")]
    require(len(messages) == 1, "expected one request to the fixture Messages endpoint")
    return messages[0]


def fixture(root, endpoint):
    cfg = root / ".claude"
    cfg.mkdir()
    credentials = cfg / ".credentials.json"
    credentials.write_text(json.dumps({"claudeAiOauth": {
        "accessToken": "FAKE-OFFICIAL-ACCESS", "refreshToken": "FAKE-OFFICIAL-REFRESH",
        "expiresAt": int(time.time() * 1000 + 86400000),
        "scopes": ["user:inference", "user:profile"], "subscriptionType": "max",
        "rateLimitTier": "default_claude_max_5x",
    }}))
    credentials.chmod(0o600)
    (cfg / ".claude.json").write_text(json.dumps({"hasCompletedOnboarding": True,
        "oauthAccount": {"accountUuid": "fixture", "emailAddress": "fixture@example.test",
                         "organizationUuid": "fixture"}}))
    settings = cfg / "settings.json"
    settings.write_text(json.dumps({"model": "claude-opus-5-5", "theme": "dark", "env": {
        "ANTHROPIC_BASE_URL": endpoint, "ANTHROPIC_AUTH_TOKEN": "",
    }}))
    return settings, credentials


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--qcs", required=True)
    parser.add_argument("--claude", required=True)
    args = parser.parse_args()
    qcs, claude = str(Path(args.qcs).resolve()), str(Path(args.claude).resolve())
    server = ThreadingHTTPServer(("127.0.0.1", 0), Backend)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    endpoint = f"http://127.0.0.1:{server.server_port}"
    results = []
    try:
        cases = [
            ("exact dotted ID", "claude-opus-4.6", "", "fixture-provider-key", {}),
            ("native effort", "claude-opus-5-5", "high", "fixture-provider-key", {}),
            ("custom effort", "vendor/model-2.5", "medium", "fixture-provider-key", {}),
            ("max effort", "claude-opus-5-5", "max", "fixture-provider-key", {}),
            ("header-only auth", "claude-opus-5-5", "", "",
             {"Authorization": "Bearer fixture-header-key"}),
        ]
        for name, model, effort, key, headers in cases:
            with tempfile.TemporaryDirectory(prefix="qcs-claude-contract-") as td:
                root = Path(td)
                env = isolated_env(root)
                settings, credentials = fixture(root, endpoint)
                version = run([claude, "--version"], env).stdout.strip()
                credential_bytes, credential_stat = credentials.read_bytes(), credentials.stat()
                # Establish the actual native fallback using fake OAuth only.
                if not results:
                    observed = client(claude, env)
                    require(observed["headers"].get("authorization") == "Bearer FAKE-OFFICIAL-ACCESS",
                            "native empty-token fallback contract changed; review credential guard")
                original = {"model": "sonnet", "effortLevel": "low", "theme": "dark", "env": {
                    **{c: "1" for c in CLOUDS}, "ANTHROPIC_MODEL": "original-model",
                    "ANTHROPIC_BASE_URL": "https://original.invalid",
                    "ANTHROPIC_CUSTOM_HEADERS": "Authorization: Bearer ORIGINAL-HEADER",
                    "ANTHROPIC_SMALL_FAST_MODEL": "original-small",
                    "CLAUDE_CODE_SUBAGENT_MODEL": "original-subagent",
                    "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "Original label",
                }}
                settings.write_text(json.dumps(original, indent=2) + "\n")
                original_bytes = settings.read_bytes()
                env.update({"ANTHROPIC_API_KEY": "FAKE-ORIGINAL-API-KEY",
                            "ANTHROPIC_MODEL": "original-shell-model",
                            "CLAUDE_CODE_SUBAGENT_MODEL": "original-shell-subagent"})
                command = [qcs, "provider", "add", "fixture", "--anthropic", endpoint,
                           "--key", key, "--header", "X-QCS-Fixture=chosen"]
                for header, value in headers.items():
                    command.extend(["--header", f"{header}={value}"])
                run(command, env)
                command = [qcs, "use", "claude", f"fixture/{model}", "--yes"]
                if effort:
                    command.extend(["--effort", effort])
                run(command, env)
                observed = client(claude, env)
                require(observed["body"]["model"] == model, "endpoint model ID was changed")
                expected_auth = f"Bearer {key}" if key else headers["Authorization"]
                require(observed["headers"].get("authorization") == expected_auth,
                        "third-party request did not use the explicit provider credential")
                require(not observed["headers"].get("x-api-key"), "original API key reached the provider")
                require(observed["headers"].get("x-qcs-fixture") == "chosen", "provider header ignored")
                if effort:
                    require(observed["body"].get("output_config", {}).get("effort") == effort,
                            "requested effort did not reach the backend")
                run([qcs, "use", "claude", "official", "--yes"], env)
                require(settings.read_bytes() == original_bytes, "original settings not restored exactly")
                after = credentials.stat()
                require(credentials.read_bytes() == credential_bytes and
                        (after.st_ino, after.st_mtime_ns, after.st_mode) ==
                        (credential_stat.st_ino, credential_stat.st_mtime_ns, credential_stat.st_mode),
                        "saved official credential file changed")
                results.append(name)
        with tempfile.TemporaryDirectory(prefix="qcs-claude-contract-cap-") as td:
            root = Path(td)
            env = isolated_env(root)
            settings, _credentials = fixture(root, endpoint)
            capped = {"model": "claude-opus-5-5", "effortLevel": "high",
                      "maxEffortLevel": "low",
                      "modelSettings": {"claude-opus-5-5": {"effortLevel": "high"}},
                      "env": {"ANTHROPIC_BASE_URL": endpoint,
                              "ANTHROPIC_AUTH_TOKEN": "fixture-provider-key"}}
            settings.write_text(json.dumps(capped))
            observed = client(claude, env)
            require(observed["body"].get("output_config", {}).get("effort") == "low",
                    "native effort-cap contract changed; review cap guard")
            original = settings.read_bytes()
            run([qcs, "provider", "add", "fixture", "--anthropic", endpoint,
                 "--key", "fixture-provider-key"], env)
            refusal = run([qcs, "use", "claude", "fixture/claude-opus-5-5",
                           "--effort", "high", "--yes"], env, success=False)
            require("maxEffortLevel" in refusal.stderr + refusal.stdout,
                    "refused request did not identify the client cap")
            require(settings.read_bytes() == original, "cap refusal changed settings")
            results.append("existing effort cap respected")
        print(json.dumps({"client": version, "passed": results,
                          "real_account": False, "backend": "loopback fixture"}, indent=2))
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)


if __name__ == "__main__":
    main()
