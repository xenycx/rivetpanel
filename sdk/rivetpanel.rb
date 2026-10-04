# frozen_string_literal: true

# RivetPanel telemetry for discordrb (standard library only).
#
#   panel = RivetPanel.new(bot)
#   bot.ready { panel.start }
#   panel.command('ping')            # in a command handler
#   panel.event('guild_join', id: 1) # small JSON data (max 1 KB)
#
# RIVET_URL and RIVET_TELEMETRY_KEY are set for you when you generate a
# key on the bot's Analytics tab. Without them this does nothing. Each push is
# also a heartbeat, so the panel can alert when the bot stops reporting.
require 'json'
require 'net/http'
require 'uri'

class RivetPanel
  def initialize(bot, interval: 30)
    @bot = bot
    @url = ENV.fetch('RIVET_URL', '').chomp('/')
    @key = ENV.fetch('RIVET_TELEMETRY_KEY', '')
    @interval = [interval, 10].max # the panel keeps one sample per 10 s
    @commands = Hash.new(0)
    @events = []
    @widgets = {}
    @lock = Mutex.new
  end

  def enabled? = !@url.empty? && !@key.empty?

  def start
    return if !enabled? || @thread

    @thread = Thread.new do
      loop do
        flush
        sleep @interval
      end
    end
  end

  def command(name, count = 1)
    @lock.synchronize { @commands[name] += count }
  end

  def event(name, data = nil)
    @lock.synchronize { @events << { name: name, data: data } if @events.size < 10 }
  end

  def widget(key, kind, title, data, position: 0)
    @lock.synchronize { @widgets[key] = { key: key, kind: kind, title: title, position: position, data: data } if @widgets.size < 48 || @widgets.key?(key) }
  end

  def flush
    commands, events = @lock.synchronize do
      c = @commands.first(20).map { |n, k| { name: n, count: k } }
      e = @events.shift(10)
      @commands.clear
      [c, e]
    end
    body = { ready: @bot.connected?, stats: stats, commands: commands, events: events, widgets: @lock.synchronize { @widgets.values.first(48) } }
    uri = URI("#{@url}/api/v1/bot-telemetry")
    Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == 'https', open_timeout: 5, read_timeout: 5) do |http|
      req = Net::HTTP::Post.new(uri, 'Content-Type' => 'application/json', 'Authorization' => "Bearer #{@key}")
      req.body = JSON.generate(body)
      res = http.request(req)
      warn "[rivetpanel] push failed: HTTP #{res.code}" unless res.is_a?(Net::HTTPSuccess)
    end
  rescue StandardError => e
    warn "[rivetpanel] push failed: #{e.message}" # never crash the bot
  end

  private

  def stats
    servers = @bot.servers.values
    { guilds: servers.size, members: servers.sum { |s| s.member_count.to_i } }
  end
end
