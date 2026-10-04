# frozen_string_literal: true

# discordrb starter. Set DISCORD_TOKEN in the panel's Environment tab.
token = ENV.fetch('DISCORD_TOKEN', '')
if token.empty?
  warn 'DISCORD_TOKEN is not set. Add it in the panel under Environment.'
  exit 1
end

require 'bundler/setup'
require 'discordrb' # prints a note that voice needs libsodium; text commands work without it
require_relative 'rivetpanel'

$stdout.sync = true
bot = Discordrb::Bot.new(token: token, intents: [:servers])
panel = RivetPanel.new(bot) # no-op until you generate a key on the Analytics tab

bot.ready do
  puts "Logged in as #{bot.profile.username}"
  bot.register_application_command(:ping, 'Replies with the gateway latency')
  panel.start
end

bot.application_command(:ping) do |event|
  panel.command('ping')
  event.respond(content: 'Pong!')
end

bot.server_create { |event| panel.event('guild_join', id: event.server.id.to_s) }

bot.run
