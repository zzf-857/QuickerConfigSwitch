#!/usr/bin/env python3
"""Check QCS-generated catalogs with an installed Codex app-server, offline.

Requires Python 3.11+ and explicit executable paths. Only temporary configuration,
fake credentials, custom providers and local model catalogs are used. This checks
the client's configuration/catalog contract; it does not exercise a real account,
the desktop UI, model inference or a CPA server.

Example:
    python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex
"""

import argparse
import hashlib
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


def client_models(codex, env, log_path):
    """Read actual client metadata without creating a thread or running a turn."""
    # Both model_provider and model_catalog_json refer to the local fixture. The
    # custom provider does not require OpenAI auth, and the catalog skips refresh.
    config = tomllib.loads((Path(env["CODEX_HOME"]) / "config.toml").read_text())
    require(config["model_provider"] == "qcs-fixture", "expected fixture provider")
    require(Path(config["model_catalog_json"]).is_file(), "expected local catalog")
    replies = queue.Queue()
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
        print("Client: " + version)
        print("Only temporary custom-provider catalogs were read; no inference or real-account check.")


if __name__ == "__main__":
    main()
