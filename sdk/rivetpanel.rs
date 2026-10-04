//! RivetPanel telemetry for Rust bots (serenity, poise, twilight, ...).
//!
//! Save as `src/rivetpanel.rs`, add `mod rivetpanel;` to main.rs and these
//! dependencies to Cargo.toml (serenity already builds the same reqwest, so
//! this adds almost nothing to the build):
//!
//! ```toml
//! reqwest = { version = "0.12", default-features = false, features = ["json", "rustls-tls"] }
//! serde_json = "1"
//! ```
//!
//! ```ignore
//! let panel = rivetpanel::Panel::from_env();
//! // in poise's setup (or serenity's ready handler), with your own stats:
//! let cache = ctx.cache.clone();
//! panel.clone().spawn(move || {
//!     let mut s = std::collections::HashMap::new();
//!     s.insert("guilds".to_string(), cache.guild_count() as f64);
//!     (s, true) // (stats, ready)
//! });
//! panel.command("ping"); // in a command
//! ```
//!
//! RIVET_URL and RIVET_TELEMETRY_KEY are set for you when you generate a
//! key on the bot's Analytics tab. Without them nothing is sent. Each push is
//! also a heartbeat, so the panel can alert when the bot stops reporting.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

#[derive(Clone)]
pub struct Panel {
    inner: Arc<Inner>,
}

struct Inner {
    url: String,
    key: String,
    http: reqwest::Client,
    commands: Mutex<HashMap<String, u64>>,
    events: Mutex<Vec<serde_json::Value>>,
    widgets: Mutex<HashMap<String, serde_json::Value>>,
}

impl Panel {
    pub fn from_env() -> Self {
        let url = std::env::var("RIVET_URL").unwrap_or_default().trim_end_matches('/').to_string();
        let key = std::env::var("RIVET_TELEMETRY_KEY").unwrap_or_default();
        let http = reqwest::Client::builder().timeout(Duration::from_secs(5)).build().expect("http client");
        Panel { inner: Arc::new(Inner { url, key, http, commands: Mutex::default(), events: Mutex::default(), widgets: Mutex::default() }) }
    }

    pub fn enabled(&self) -> bool {
        !self.inner.url.is_empty() && !self.inner.key.is_empty()
    }

    /// Counts one invocation of a command.
    pub fn command(&self, name: &str) {
        let mut c = self.inner.commands.lock().unwrap();
        if c.len() < 100 || c.contains_key(name) {
            *c.entry(name.to_string()).or_default() += 1;
        }
    }

    /// Records a notable event; data must serialize to at most 1 KB.
    pub fn event(&self, name: &str, data: serde_json::Value) {
        let mut e = self.inner.events.lock().unwrap();
        if e.len() < 10 {
            e.push(serde_json::json!({ "name": name, "data": data }));
        }
    }

    pub fn widget(&self, key: &str, kind: &str, title: &str, data: serde_json::Value, position: u16) {
        let mut w=self.inner.widgets.lock().unwrap(); if w.len()<48 || w.contains_key(key) { w.insert(key.to_string(),serde_json::json!({"key":key,"kind":kind,"title":title,"position":position,"data":data})); }
    }

    /// Pushes every 30 seconds on the Tokio runtime. `sample` returns the
    /// current stats and whether the bot is connected to Discord.
    pub fn spawn<F>(self, sample: F)
    where
        F: Fn() -> (HashMap<String, f64>, bool) + Send + Sync + 'static,
    {
        if !self.enabled() {
            return;
        }
        tokio::spawn(async move {
            let mut tick = tokio::time::interval(Duration::from_secs(30));
            loop {
                tick.tick().await;
                let (stats, ready) = sample();
                self.flush(stats, ready).await;
            }
        });
    }

    /// Sends one push now. Failures are logged and dropped, never queued.
    pub async fn flush(&self, stats: HashMap<String, f64>, ready: bool) {
        let commands: Vec<_> = {
            let mut c = self.inner.commands.lock().unwrap();
            let out = c.iter().take(20).map(|(n, k)| serde_json::json!({ "name": n, "count": k })).collect();
            c.clear();
            out
        };
        let events: Vec<_> = {
            let mut e = self.inner.events.lock().unwrap();
            e.drain(..).collect()
        };
        let widgets: Vec<_> = self.inner.widgets.lock().unwrap().values().cloned().collect();
        let body = serde_json::json!({ "ready": ready, "stats": stats, "commands": commands, "events": events, "widgets": widgets });
        let res = self
            .inner
            .http
            .post(format!("{}/api/v1/bot-telemetry", self.inner.url))
            .bearer_auth(&self.inner.key)
            .json(&body)
            .send()
            .await;
        match res {
            Ok(r) if r.status().is_success() => {}
            Ok(r) => eprintln!("[rivetpanel] push failed: HTTP {}", r.status()),
            Err(e) => eprintln!("[rivetpanel] push failed: {e}"), // never crash the bot
        }
    }
}
