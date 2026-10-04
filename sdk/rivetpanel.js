// RivetPanel telemetry for discord.js v14 (Node 18+, no dependencies).
//
//   const { RivetPanel } = require('./rivetpanel');
//   const panel = new RivetPanel(client);       // after creating your Client
//   client.once('ready', () => panel.start());
//   // in your command handler:
//   panel.command(interaction.commandName);
//   panel.event('guild_join', { id: guild.id });
//
// RIVET_URL and RIVET_TELEMETRY_KEY are set for you when you generate a
// key on the bot's Analytics tab. If they are missing this does nothing.
'use strict';

class RivetPanel {
  constructor(client, { intervalMs = 30_000 } = {}) {
    this.client = client;
    this.url = (process.env.RIVET_URL || '').replace(/\/$/, '');
    this.key = process.env.RIVET_TELEMETRY_KEY || '';
    this.intervalMs = Math.max(intervalMs, 10_000); // the panel keeps one sample per 10 s
    this.commands = new Map();
    this.events = [];
    this.widgets = new Map();
    this.unpublished = new Set();
    this.timer = null;
  }

  get enabled() { return Boolean(this.url && this.key); }

  start() {
    if (!this.enabled || this.timer) return;
    this.timer = setInterval(() => this.flush(), this.intervalMs);
    this.timer.unref();
    this.flush();
  }

  stop() { clearInterval(this.timer); this.timer = null; }

  /** Count a command invocation (batched into the next push). */
  command(name, count = 1) {
    this.commands.set(name, (this.commands.get(name) || 0) + count);
  }

  /** Record a notable event; data must be small JSON (max 1 KB). */
  event(name, data) {
    if (this.events.length < 10) this.events.push({ name, data });
  }

  /** Publish a safe dashboard widget. options: group, span, minHeight, ttlSeconds. */
  widget(key, kind, title, data, position = 0, options = {}) {
    if (this.widgets.size < 48 || this.widgets.has(key)) this.widgets.set(key, { key, kind, title, position, group: options.group, span: options.span, min_height: options.minHeight, ttl_seconds: options.ttlSeconds, data });
    this.unpublished.delete(key);
  }

  /** Remove a previously published widget on the next push. */
  unpublish(key) { this.widgets.delete(key); this.unpublished.add(key); }

  stats() {
    const c = this.client;
    let members = 0;
    for (const g of c.guilds.cache.values()) members += g.memberCount || 0;
    return {
      guilds: c.guilds.cache.size,
      members,
      ping: c.ws.ping,
      voice_channels: c.guilds.cache.filter((g) => g.members.me?.voice?.channelId).size,
    };
  }

  async flush() {
    if (!this.enabled) return;
    const body = {
      // Each push is also a heartbeat: the panel can alert when they stop.
      ready: this.client.isReady(),
      identity: this.client.isReady() && this.client.user ? { id: this.client.user.id, username: this.client.user.username, avatar_url: this.client.user.displayAvatarURL({ extension: 'png', size: 128 }) } : undefined,
      stats: this.stats(),
      commands: [...this.commands].slice(0, 20).map(([name, count]) => ({ name, count })),
      events: this.events.splice(0, 10),
      widgets: [...this.widgets.values()].slice(0, 48),
      unpublish: [...this.unpublished].slice(0, 48),
    };
    this.commands.clear();
    try {
      const res = await fetch(`${this.url}/api/v1/bot-telemetry`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${this.key}` },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(5000),
      });
      if (!res.ok) console.warn(`[rivetpanel] push failed: HTTP ${res.status}`);
      else this.unpublished.clear();
    } catch (err) {
      console.warn('[rivetpanel] push failed:', err.message); // never crash the bot
    }
  }
}

module.exports = { RivetPanel };
