"""RivetPanel telemetry for discord.py 2.x (uses aiohttp, which discord.py already needs).

    from rivetpanel import RivetPanel

    bot = commands.Bot(...)
    panel = RivetPanel(bot)

    @bot.event
    async def setup_hook():
        panel.start()

    @bot.listen("on_app_command_completion")
    async def _count(interaction, command):
        panel.command(command.qualified_name)

    panel.event("guild_join", {"id": 123})

RIVET_URL and RIVET_TELEMETRY_KEY are set for you when you generate a key
on the bot's Analytics tab. If they are missing this does nothing.
"""
from __future__ import annotations

import asyncio
import logging
import os
from collections import Counter

import aiohttp

log = logging.getLogger("rivetpanel")


class RivetPanel:
    def __init__(self, bot, interval: float = 30.0):
        self.bot = bot
        self.url = os.environ.get("RIVET_URL", "").rstrip("/")
        self.key = os.environ.get("RIVET_TELEMETRY_KEY", "")
        self.interval = max(interval, 10.0)  # the panel keeps one sample per 10 s
        self._commands: Counter[str] = Counter()
        self._events: list[dict] = []
        self._widgets: dict[str, dict] = {}
        self._unpublished: set[str] = set()
        self._task: asyncio.Task | None = None

    @property
    def enabled(self) -> bool:
        return bool(self.url and self.key)

    def start(self) -> None:
        if self.enabled and self._task is None:
            self._task = asyncio.get_running_loop().create_task(self._run())

    def stop(self) -> None:
        if self._task:
            self._task.cancel()
            self._task = None

    def command(self, name: str, count: int = 1) -> None:
        """Count a command invocation (batched into the next push)."""
        self._commands[name] += count

    def event(self, name: str, data: dict | None = None) -> None:
        """Record a notable event; data must be small JSON (max 1 KB)."""
        if len(self._events) < 10:
            self._events.append({"name": name, "data": data})

    def widget(self, key: str, kind: str, title: str, data: dict, position: int = 0, *, group: str = "Overview", span: int = 1, min_height: int = 0, ttl_seconds: int = 0) -> None:
        """Publish a safe dashboard widget visible to every panel user."""
        if len(self._widgets) < 48 or key in self._widgets:
            self._widgets[key] = {"key": key, "kind": kind, "title": title, "position": position, "group": group, "span": span, "min_height": min_height, "ttl_seconds": ttl_seconds, "data": data}
        self._unpublished.discard(key)

    def unpublish(self, key: str) -> None:
        """Remove a previously published widget on the next push."""
        self._widgets.pop(key, None)
        self._unpublished.add(key)

    def stats(self) -> dict:
        bot = self.bot
        return {
            "guilds": len(bot.guilds),
            "members": sum(g.member_count or 0 for g in bot.guilds),
            "ping": round(bot.latency * 1000, 1) if bot.latency == bot.latency else 0,  # NaN before ready
            "voice_channels": len(bot.voice_clients),
        }

    async def _run(self) -> None:
        await self.bot.wait_until_ready()
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=5)) as http:
            while True:
                await self.flush(http)
                await asyncio.sleep(self.interval)

    async def flush(self, http: aiohttp.ClientSession) -> None:
        body = {
            # Each push is also a heartbeat: the panel can alert when they stop.
            "ready": self.bot.is_ready() and not self.bot.is_closed(),
            "identity": ({"id": str(self.bot.user.id), "username": self.bot.user.name, "avatar_url": str(self.bot.user.display_avatar.url)} if self.bot.is_ready() and self.bot.user else None),
            "stats": self.stats(),
            "commands": [{"name": n, "count": c} for n, c in list(self._commands.items())[:20]],
            "events": self._events[:10],
            "widgets": list(self._widgets.values())[:48],
            "unpublish": list(self._unpublished)[:48],
        }
        self._commands.clear()
        del self._events[:10]
        try:
            async with http.post(
                f"{self.url}/api/v1/bot-telemetry",
                json=body,
                headers={"Authorization": f"Bearer {self.key}"},
            ) as resp:
                if resp.status >= 400:
                    log.warning("push failed: HTTP %s", resp.status)
                else:
                    self._unpublished.clear()
        except Exception as exc:  # never crash the bot
            log.warning("push failed: %s", exc)
