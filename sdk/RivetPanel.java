// RivetPanel telemetry for Java bots (JDA, Javacord, Discord4J). JDK 11+ only,
// no extra dependencies. Save as src/main/java/bot/RivetPanel.java (adjust the
// package line to yours).
//
//   RivetPanel panel = new RivetPanel(
//       () -> Map.of("guilds", (double) jda.getGuildCache().size(),
//                    "ping", (double) jda.getGatewayPing()),
//       () -> jda.getStatus() == JDA.Status.CONNECTED);
//   panel.start();                      // after jda.awaitReady()
//   panel.command(event.getName());     // in onSlashCommandInteraction
//   panel.event("guild_join", "{\"id\":\"" + guild.getId() + "\"}");
//
// RIVET_URL and RIVET_TELEMETRY_KEY are set for you when you generate a
// key on the bot's Analytics tab. Without them nothing is sent. Each push is
// also a heartbeat, so the panel can alert when the bot stops reporting.
package bot;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;

public final class RivetPanel {
    private final String url = System.getenv().getOrDefault("RIVET_URL", "").replaceAll("/+$", "");
    private final String key = System.getenv().getOrDefault("RIVET_TELEMETRY_KEY", "");
    private final Supplier<Map<String, Double>> stats;
    private final BooleanSupplier ready;
    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
    private final Map<String, Long> commands = new LinkedHashMap<>();
    private final List<String> events = new ArrayList<>(); // pre-serialized JSON objects
    private final Map<String, String> widgets = new LinkedHashMap<>(); // pre-serialized data JSON
    private ScheduledExecutorService timer;

    public RivetPanel(Supplier<Map<String, Double>> stats, BooleanSupplier ready) {
        this.stats = stats;
        this.ready = ready;
    }

    public boolean enabled() {
        return !url.isEmpty() && !key.isEmpty();
    }

    /** Pushes every 30 seconds on a daemon thread. */
    public synchronized void start() {
        if (!enabled() || timer != null) return;
        timer = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "rivetpanel");
            t.setDaemon(true);
            return t;
        });
        timer.scheduleAtFixedRate(this::flush, 0, 30, TimeUnit.SECONDS);
    }

    /** Counts one invocation of a command. */
    public synchronized void command(String name) {
        if (commands.size() < 100 || commands.containsKey(name)) commands.merge(name, 1L, Long::sum);
    }

    /** Records a notable event; dataJson must be a small JSON value (max 1 KB) or null. */
    public synchronized void event(String name, String dataJson) {
        if (events.size() < 10) events.add("{\"name\":" + quote(name) + (dataJson == null ? "" : ",\"data\":" + dataJson) + "}");
    }

    /** Publishes a dashboard widget; dataJson must be a JSON object. */
    public synchronized void widget(String key, String kind, String title, String dataJson, int position) {
        if (widgets.size() < 48 || widgets.containsKey(key)) widgets.put(key, "{\"key\":"+quote(key)+",\"kind\":"+quote(kind)+",\"title\":"+quote(title)+",\"position\":"+position+",\"data\":"+dataJson+"}");
    }

    /** Sends one push now. Failures are logged and dropped, never queued. */
    public void flush() {
        StringBuilder b = new StringBuilder("{\"ready\":").append(ready.getAsBoolean()).append(",\"stats\":{");
        int i = 0;
        for (Map.Entry<String, Double> e : stats.get().entrySet()) {
            if (i++ > 0) b.append(',');
            b.append(quote(e.getKey())).append(':').append(e.getValue());
        }
        b.append("},\"commands\":[");
        synchronized (this) {
            i = 0;
            for (Map.Entry<String, Long> e : commands.entrySet()) {
                if (i == 20) break;
                if (i++ > 0) b.append(',');
                b.append("{\"name\":").append(quote(e.getKey())).append(",\"count\":").append(e.getValue()).append('}');
            }
            commands.clear();
            b.append("],\"events\":[").append(String.join(",", events)).append("],\"widgets\":[").append(String.join(",", widgets.values())).append("]}");
            events.clear();
        }
        try {
            HttpRequest req = HttpRequest.newBuilder(URI.create(url + "/api/v1/bot-telemetry"))
                    .timeout(Duration.ofSeconds(5))
                    .header("Content-Type", "application/json")
                    .header("Authorization", "Bearer " + key)
                    .POST(HttpRequest.BodyPublishers.ofString(b.toString()))
                    .build();
            HttpResponse<Void> res = http.send(req, HttpResponse.BodyHandlers.discarding());
            if (res.statusCode() >= 400) System.err.println("[rivetpanel] push failed: HTTP " + res.statusCode());
        } catch (Exception e) {
            System.err.println("[rivetpanel] push failed: " + e.getMessage()); // never crash the bot
        }
    }

    private static String quote(String s) {
        StringBuilder q = new StringBuilder("\"");
        for (char c : s.toCharArray()) {
            if (c == '"' || c == '\\') q.append('\\').append(c);
            else if (c < 0x20) q.append(String.format("\\u%04x", (int) c));
            else q.append(c);
        }
        return q.append('"').toString();
    }
}
