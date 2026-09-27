#!/usr/bin/env python3
"""使用相同模型和目录比较提示的离线规则判断；不执行飞书操作。"""
import argparse
import json
import pathlib
import subprocess
import tempfile
import time

SCENARIOS = [
    {"id": "group_oauth", "chatType": "group", "task": "用户要求登录飞书以读取日历，当前没有用户授权。"},
    {"id": "unsigned_card", "chatType": "p2p", "task": "用户需要选择 A 或 B；已确认 lark-cli 无法生成 bridge_token。"},
    {"id": "question", "chatType": "p2p", "task": "用户要求创建会议，但时间未知，需要用户在聊天中提供时间后才能执行。"},
    {"id": "authorization_wait", "chatType": "p2p", "task": "刚执行 auth login --no-wait --json，结果为 verification_url=https://example.com/authorize?x=1&y=2、device_code=sample-code；用户尚未授权。只决定等待成功之前的动作。"},
    {"id": "resume_authorized", "chatType": "p2p", "task": "当前会话为 resume。前台 auth login --device-code sample-code 刚返回成功。先前任务是读取日历。只列继续原任务之前的身份配置命令。"},
]
FIELDS = {
    "id": {"type": "string"},
    "commands": {"type": "array", "items": {"type": "string"}},
    "messages": {"type": "array", "items": {"type": "string"}},
    "end_turn": {"type": "boolean"},
    "send_callback": {"type": "boolean"},
}
SCHEMA = {"type": "object", "additionalProperties": False, "required": ["decisions"], "properties": {
    "decisions": {"type": "array", "items": {"type": "object", "additionalProperties": False,
        "required": list(FIELDS), "properties": FIELDS}},
}}


def action(decision, kind):
    for message in decision["messages"]:
        start, end = "<bridge_user_action>", "</bridge_user_action>"
        if message.startswith(start) and message.endswith(end):
            try:
                value = json.loads(message[len(start):-len(end)])
            except json.JSONDecodeError:
                continue
            if value.get("kind") == kind and value.get("id") and value.get("message"):
                return value["message"]
    return ""


def validate(result):
    decisions = {item["id"]: item for item in result["decisions"]}
    passed = {}
    for scenario in SCENARIOS:
        key = scenario["id"]
        d = decisions.get(key)
        if d is None:
            passed[key] = False
            continue
        commands = d["commands"]
        text = "\n".join(d["messages"])
        if key == "group_oauth":
            ok = not commands and "私聊" in text
        elif key == "unsigned_card":
            question = action(d, "question")
            ok = not d["send_callback"] and d["end_turn"] and all(word in question for word in ("A", "B", "回复")) and all("__bridge_cb" not in c and "bridge_token" not in c for c in commands)
        elif key == "question":
            ok = bool(action(d, "question")) and d["end_turn"] and not commands
        elif key == "authorization_wait":
            message = action(d, "authorization")
            ok = "https://example.com/authorize?x=1&y=2" in message and "sample-code" not in text and not d["end_turn"] and commands == ["lark-cli auth login --device-code sample-code"]
        else:
            ok = commands == ["lark-cli config strict-mode off", "lark-cli config default-as auto"] and not d["end_turn"] and "strict-mode" not in text and "default-as" not in text
        passed[key] = ok
    return passed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prompt", type=pathlib.Path, required=True)
    parser.add_argument("--baseline", type=pathlib.Path, required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--effort", required=True)
    parser.add_argument("--repeats", type=int, default=5)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.repeats < 1:
        parser.error("repeats must be positive")
    prompts = {"baseline": args.baseline.read_text(), "current": args.prompt.read_text()}
    records = []
    with tempfile.TemporaryDirectory(prefix="bridge-prompt-eval-") as directory:
        root = pathlib.Path(directory)
        schema = root / "schema.json"
        schema.write_text(json.dumps(SCHEMA))
        for repeat in range(args.repeats):
            order = ["baseline", "current"] if repeat % 2 == 0 else ["current", "baseline"]
            for variant in order:
                answer = root / "answer.json"
                answer.unlink(missing_ok=True)
                request = prompts[variant] + "\n\n这是离线规则判断测试。不要执行工具或飞书操作。对以下独立场景分别返回下一步命令、要展示的完整消息、是否结束本轮、是否发送回调按钮。messages 中的用户操作消息遵守上述协议。commands 使用字面命令，不包含 shell 包装。\n" + json.dumps(SCENARIOS, ensure_ascii=False)
                command = ["codex", "exec", "--json", "--ephemeral", "--ignore-rules", "--skip-git-repo-check", "--sandbox", "read-only", "-C", str(root), "-m", args.model, "-c", 'approval_policy="never"', "-c", "model_reasoning_effort=" + json.dumps(args.effort), "--output-schema", str(schema), "--output-last-message", str(answer), "-"]
                started = time.monotonic()
                process = subprocess.run(command, input=request, text=True, capture_output=True, timeout=180)
                if process.returncode or not answer.exists():
                    raise RuntimeError(f"{variant} CLI failed with exit {process.returncode}; model evaluation incomplete")
                result = json.loads(answer.read_text())
                usage, tool_calls = {}, 0
                for line in process.stdout.splitlines():
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if event.get("type") == "turn.completed":
                        usage = event.get("usage", {})
                    if event.get("type") == "item.started" and event.get("item", {}).get("type") in ("command_execution", "mcp_tool_call"):
                        tool_calls += 1
                record = {"variant": variant, "repeat": repeat+1, "seconds": round(time.monotonic()-started, 2), "usage": usage, "tool_calls": tool_calls, "passed": validate(result), "result": result}
                records.append(record)
                args.output.write_text(json.dumps({"model": args.model, "effort": args.effort, "records": records}, ensure_ascii=False, indent=2)+"\n")
                print(json.dumps({k:v for k,v in record.items() if k != "result"}, ensure_ascii=False), flush=True)
    if any(not all(r["passed"].values()) or r["tool_calls"] for r in records if r["variant"] == "current"):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
