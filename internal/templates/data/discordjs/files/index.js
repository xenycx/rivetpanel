// discord.js starter. Set DISCORD_TOKEN in the panel's Environment tab.
const { Client, GatewayIntentBits, Events } = require('discord.js');
const { RivetPanel } = require('./rivetpanel');

const token = process.env.DISCORD_TOKEN;
if (!token) {
  console.error('DISCORD_TOKEN is not set. Add it in the panel under Environment.');
  process.exit(1);
}

const client = new Client({ intents: [GatewayIntentBits.Guilds] });
const panel = new RivetPanel(client); // no-op until you generate a key on the Analytics tab

client.once(Events.ClientReady, async (c) => {
  console.log(`Logged in as ${c.user.tag}`);
  await c.application.commands.set([{ name: 'ping', description: 'Replies with the gateway latency' }]);
  panel.start();
});

client.on(Events.InteractionCreate, async (interaction) => {
  if (!interaction.isChatInputCommand()) return;
  panel.command(interaction.commandName);
  if (interaction.commandName === 'ping') {
    await interaction.reply(`Pong! ${client.ws.ping} ms`);
  }
});

client.on(Events.GuildCreate, (g) => panel.event('guild_join', { id: g.id }));

client.login(token);
