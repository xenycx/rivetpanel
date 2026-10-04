// discord.js starter in TypeScript. Node.js 24 strips the types and runs this
// file directly, so there is no build step; use only erasable TypeScript
// (no enums or namespaces). Set DISCORD_TOKEN in the panel's Environment tab.
import { createRequire } from 'node:module';
import { Client, Events, GatewayIntentBits, type ChatInputCommandInteraction, type Guild } from 'discord.js';

// The RivetPanel SDK is a CommonJS file next to package.json.
const require = createRequire(import.meta.url);
const { RivetPanel } = require('../rivetpanel.js') as {
  RivetPanel: new (client: Client) => { start(): void; command(name: string): void; event(name: string, data?: unknown): void };
};

const token: string | undefined = process.env.DISCORD_TOKEN;
if (!token) {
  console.error('DISCORD_TOKEN is not set. Add it in the panel under Environment.');
  process.exit(1);
}

const client = new Client({ intents: [GatewayIntentBits.Guilds] });
const panel = new RivetPanel(client); // no-op until you generate a key on the Analytics tab

type Handler = (interaction: ChatInputCommandInteraction) => Promise<void>;
const commands: Record<string, { description: string; run: Handler }> = {
  ping: {
    description: 'Replies with the gateway latency',
    run: async (i) => {
      await i.reply(`Pong! ${client.ws.ping} ms`);
    },
  },
};

client.once(Events.ClientReady, async (c) => {
  console.log(`Logged in as ${c.user.tag}`);
  await c.application.commands.set(Object.entries(commands).map(([name, cmd]) => ({ name, description: cmd.description })));
  panel.start();
});

client.on(Events.InteractionCreate, async (interaction) => {
  if (!interaction.isChatInputCommand()) return;
  const cmd = commands[interaction.commandName];
  if (!cmd) return;
  panel.command(interaction.commandName);
  await cmd.run(interaction);
});

client.on(Events.GuildCreate, (g: Guild) => panel.event('guild_join', { id: g.id }));

await client.login(token);
