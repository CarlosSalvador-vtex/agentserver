"""Ctx — the workspace-scoped SDK handle bundled into every notebook kernel.

`Ctx.from_env()` constructs a lazy handle (no I/O). The first `await
ctx.envs()` calls the gateway's REST endpoint.
"""

from __future__ import annotations

import asyncio
import os

from .client import HTTPClient
from .env import Env
from .errors import SdkConfigError
from .types import ToolMetadata


class Ctx:
    def __init__(self, client: HTTPClient) -> None:
        self._client = client
        self._envs_lock = asyncio.Lock()
        self._envs_cache: list[Env] | None = None

    @classmethod
    def from_env(cls) -> "Ctx":
        url = os.environ.get("AGENTSERVER_GATEWAY_URL")
        token = os.environ.get("AGENTSERVER_WORKSPACE_TOKEN", "")
        if not url:
            raise SdkConfigError("AGENTSERVER_GATEWAY_URL is required")
        if not token:
            raise SdkConfigError("AGENTSERVER_WORKSPACE_TOKEN is required")
        return cls(HTTPClient(url, token))

    async def envs(self) -> list[Env]:
        """List envs in the workspace. Caches inside Ctx for the kernel
        lifetime — call `refresh()` to clear the cache."""
        async with self._envs_lock:
            if self._envs_cache is None:
                self._envs_cache = await self._fetch_envs()
        return list(self._envs_cache)

    async def _fetch_envs(self) -> list[Env]:
        listing = await self._client.post("/api/sdk/envs/list", {})
        envs: list[Env] = []
        for e in listing.get("envs", []):
            tools = [ToolMetadata.from_dict(t) for t in e.get("tools", [])]
            envs.append(Env(
                name=e["name"],
                type=e.get("type", "executor"),
                tools=tools,
                _client=self._client,
            ))
        return envs

    async def env(self, name: str) -> Env:
        for e in await self.envs():
            if e.name == name:
                return e
        raise KeyError(f"env not found: {name}")

    async def refresh(self) -> None:
        """Clear the env cache so the next `envs()` call refetches from the gateway."""
        async with self._envs_lock:
            self._envs_cache = None

    async def close(self) -> None:
        await self._client.close()
