import base64
import pytest

from agentserver_sdk.client import WSClient
from agentserver_sdk.env import Env
from agentserver_sdk.errors import ToolError
from agentserver_sdk.types import ShellResult, ToolMetadata


async def _connected_client(stub):
    c = WSClient(stub.url, token="t", workspace_id="ws", user_id="u")
    await c.connect()
    return c


def _tool(name, desc=""):
    return ToolMetadata(name=name, description=desc, input_schema={}, kind="core" if name in {
        "shell", "read_file", "write_file", "apply_patch", "exec_command",
        "write_stdin", "read_output", "terminate", "copy_path",
    } else "custom")


async def test_env_call_injects_environment_id(stub):
    stub.on("mcpServer/tool/call", lambda p: {"content": [], "isError": False})
    c = await _connected_client(stub)
    env = Env(name="alpha", type="shell", tools=[_tool("shell")], _client=c)
    try:
        await env.call("shell", {"command": "ls"})
        call = next(m for m in stub.received if m.get("method") == "mcpServer/tool/call")
        assert call["params"]["arguments"]["environment_id"] == "alpha"
        assert call["params"]["arguments"]["command"] == "ls"
        assert call["params"]["tool"] == "shell"
    finally:
        await c.close()


async def test_env_shell_returns_shell_result(stub):
    stub.on("mcpServer/tool/call", lambda p: {
        "content": [{"type": "text", "text": "hi"}],
        "structuredContent": {"stdout": "hi", "stderr": "", "exit_code": 0},
        "isError": False,
    })
    c = await _connected_client(stub)
    env = Env(name="alpha", type="shell", tools=[_tool("shell")], _client=c)
    try:
        r = await env.shell("echo hi")
        assert isinstance(r, ShellResult)
        assert r.stdout == "hi"
        assert r.exit_code == 0
    finally:
        await c.close()


async def test_env_read_file_returns_bytes(stub):
    payload = b"hello binary"
    stub.on("mcpServer/tool/call", lambda p: {
        "content": [{"type": "text", "text": base64.b64encode(payload).decode()}],
        "structuredContent": {"encoding": "base64"},
        "isError": False,
    })
    c = await _connected_client(stub)
    env = Env(name="alpha", type="shell", tools=[_tool("read_file")], _client=c)
    try:
        data = await env.read_file("/x")
        assert data == payload
    finally:
        await c.close()


async def test_env_is_error_raises_tool_error(stub):
    stub.on("mcpServer/tool/call", lambda p: {
        "content": [{"type": "text", "text": "boom"}],
        "isError": True,
    })
    c = await _connected_client(stub)
    env = Env(name="alpha", type="shell", tools=[_tool("shell")], _client=c)
    try:
        with pytest.raises(ToolError) as ei:
            await env.shell("badcmd")
        assert ei.value.env == "alpha"
        assert ei.value.tool == "shell"
        assert "boom" in ei.value.message
    finally:
        await c.close()


async def test_env_write_file_passes_bytes_as_b64(stub):
    stub.on("mcpServer/tool/call", lambda p: {"content": [], "isError": False})
    c = await _connected_client(stub)
    env = Env(name="alpha", type="shell", tools=[_tool("write_file")], _client=c)
    try:
        await env.write_file("/x", b"\x00\x01\x02")
        call = next(m for m in stub.received if m.get("method") == "mcpServer/tool/call")
        args = call["params"]["arguments"]
        assert args["path"] == "/x"
        assert base64.b64decode(args["content_b64"]) == b"\x00\x01\x02"
    finally:
        await c.close()


async def test_env_apply_patch_passes_through(stub):
    stub.on("mcpServer/tool/call", lambda p: {"content": [], "isError": False})
    c = await _connected_client(stub)
    env = Env(name="alpha", type="shell", tools=[_tool("apply_patch")], _client=c)
    try:
        await env.apply_patch("*** Patch...")
        call = next(m for m in stub.received if m.get("method") == "mcpServer/tool/call")
        assert call["params"]["arguments"]["patch"] == "*** Patch..."
    finally:
        await c.close()
