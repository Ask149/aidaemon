#!/usr/bin/env python3
"""Probe Claude Opus model IDs against Copilot API."""
import json, os, urllib.request, socket

# Short timeout — we just need HTTP status, not full response
socket.setdefaulttimeout(15)

auth = json.load(open(os.path.expanduser("~/.config/aidaemon/auth.json")))
gh_token = auth["github_token"]

req = urllib.request.Request("https://api.github.com/copilot_internal/v2/token")
req.add_header("Authorization", "Bearer " + gh_token)
req.add_header("User-Agent", "aidaemon/0.1")
resp = urllib.request.urlopen(req)
cp_token = json.loads(resp.read())["token"]
print("Got copilot token OK")

models = [
    # Anthropic Claude (from screenshot + known variants)
    "claude-haiku-4.5", "claude-haiku-4", "claude-haiku-3.5", "claude-haiku-3",
    "claude-opus-4.6", "claude-opus-4.6-fast", "claude-opus-4.5", "claude-opus-4.1", "claude-opus-4",
    "claude-sonnet-4.5", "claude-sonnet-4", "claude-sonnet-3.5", "claude-sonnet-3.7",
    "claude-3.5-sonnet", "claude-3.7-sonnet", "claude-3-opus", "claude-3.5-haiku", "claude-3-haiku",
    # OpenAI GPT-5 series (from screenshot)
    "gpt-5", "gpt-5-mini", "gpt-5.1", "gpt-5.2", "gpt-5.3",
    "gpt-5-codex", "gpt-5-codex-preview",
    "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini", "gpt-5.1-codex-mini-preview",
    "gpt-5.2-codex", "gpt-5.3-codex",
    # OpenAI GPT-4 series
    "gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano",
    "gpt-4-turbo", "gpt-4-turbo-preview", "gpt-4-0125-preview", "gpt-4-1106-preview",
    "gpt-4", "gpt-3.5-turbo", "gpt-4-32k",
    # OpenAI Codex (legacy)
    "code-davinci-002", "code-cushman-001", "codex",
    # OpenAI o-series (reasoning)
    "o1", "o1-mini", "o1-preview", "o1-pro", "o2", "o2-mini",
    "o3", "o3-mini", "o4", "o4-mini",
    # Google Gemini (from screenshot + known)
    "gemini-3-pro", "gemini-3-pro-preview", "gemini-3-flash", "gemini-3-flash-preview",
    "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-8b",
    "gemini-2.0-flash", "gemini-2.0-flash-exp", "gemini-2.0-pro",
    "gemini-1.5-pro", "gemini-1.5-pro-002", "gemini-1.5-flash", "gemini-1.5-flash-002",
    "gemini-pro", "gemini-flash", "gemini-ultra",
    "gemini-exp-1206", "gemini-exp-1121",
    # Special/Internal
    "goldeneye",
]
for m in models:
    data = json.dumps({"model": m, "messages": [{"role": "user", "content": "say hi"}], "max_tokens": 5, "stream": False}).encode()
    req2 = urllib.request.Request("https://api.githubcopilot.com/chat/completions", data=data)
    req2.add_header("Authorization", "Bearer " + cp_token)
    req2.add_header("Content-Type", "application/json")
    req2.add_header("Editor-Version", "vscode/1.105.1")
    req2.add_header("Editor-Plugin-Version", "copilot-chat/0.32.4")
    req2.add_header("Copilot-Integration-Id", "vscode-chat")
    req2.add_header("Openai-Intent", "conversation-panel")
    try:
        r = urllib.request.urlopen(req2, timeout=15)
        body = json.loads(r.read())
        print(f"  {m} -> 200 OK  model={body.get('model','?')}")
    except urllib.error.HTTPError as e:
        print(f"  {m} -> HTTP {e.code}")
    except Exception as e:
        print(f"  {m} -> ERROR {e}")
