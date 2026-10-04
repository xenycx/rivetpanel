# discord.js TypeScript starter

`src/index.mts` is TypeScript that Node.js 24 runs directly: it strips the
type annotations when it loads the file, so the panel needs no build step.
Keep to *erasable* syntax (type annotations, interfaces, `type` imports); enums
and namespaces need a compiler and are rejected (`erasableSyntaxOnly` in
`tsconfig.json` catches them).

- Token: set `DISCORD_TOKEN` in the panel under **Environment**.
- Type-check on your computer: `npm install && npm run check`. The panel
  installs only runtime dependencies (`npm install --omit=dev`).
- Add commands in the `commands` table; they are registered when the bot
  starts.
- Analytics: generate a key in the panel's **Analytics** section; the bundled
  `rivetpanel.js` then reports guilds, latency, commands and a heartbeat.
