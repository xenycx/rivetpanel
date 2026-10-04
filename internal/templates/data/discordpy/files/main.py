"""discord.py starter. Set DISCORD_TOKEN in the panel's Environment tab."""
import os
import sys

import discord
from discord import app_commands

from rivetpanel import RivetPanel

token = os.environ.get("DISCORD_TOKEN")
if not token:
    sys.exit("DISCORD_TOKEN is not set. Add it in the panel under Environment.")


class Bot(discord.Client):
    def __init__(self) -> None:
        super().__init__(intents=discord.Intents.default())
        self.tree = app_commands.CommandTree(self)
        self.panel = RivetPanel(self)  # no-op until you generate a key on the Analytics tab

    async def setup_hook(self) -> None:
        await self.tree.sync()
        self.panel.start()


bot = Bot()


@bot.tree.command(name="ping", description="Replies with the gateway latency")
async def ping(interaction: discord.Interaction) -> None:
    bot.panel.command("ping")
    await interaction.response.send_message(f"Pong! {round(bot.latency * 1000)} ms")


@bot.event
async def on_ready() -> None:
    print(f"Logged in as {bot.user}", flush=True)


@bot.event
async def on_guild_join(guild: discord.Guild) -> None:
    bot.panel.event("guild_join", {"id": guild.id})


bot.run(token)
