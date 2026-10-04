# discordrb starter

1. In the panel, open **Environment** and add `DISCORD_TOKEN` (Discord Developer Portal → Bot).
2. Press **Start**. The first start runs `bundle install` in a build container (gems land in `vendor/bundle`).
3. Invite the bot (OAuth2 → URL Generator → scopes `bot` and `applications.commands`) and run `/ping`.

`rivetpanel.rb` reports guilds, members, commands and a heartbeat once you generate a key in the panel's **Analytics** section.
