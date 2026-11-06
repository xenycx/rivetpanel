package sdk

import "time"

// Log writes a line to the extension's log in the panel.
func Log(msg string) { _ = Call("log", map[string]string{"message": msg}, nil) }

// KV scopes: per extension, per bot, or per user.
type KVKey struct {
	Scope string `json:"scope"` // "" (extension), "bot:<id>" or "user:<id>"
	Key   string `json:"key"`
}

// KVGet reads a value; ok is false when it is absent or expired.
func KVGet(scope, key string) (value []byte, ok bool, err error) {
	var r struct {
		Value []byte `json:"value"`
		Found bool   `json:"found"`
	}
	err = Call("kv.get", KVKey{scope, key}, &r)
	return r.Value, r.Found, err
}

// KVSet stores a value (at most 64 KiB); ttl 0 keeps it until deleted.
func KVSet(scope, key string, value []byte, ttl time.Duration) error {
	return Call("kv.set", map[string]any{"scope": scope, "key": key, "value": value, "ttl_ms": ttl.Milliseconds()}, nil)
}

// KVDelete removes a value.
func KVDelete(scope, key string) error { return Call("kv.delete", KVKey{scope, key}, nil) }

// Settings returns the extension's non-secret settings.
func Settings() (map[string]any, error) {
	var s map[string]any
	err := Call("settings.get", struct{}{}, &s)
	return s, err
}

// Bot is the subset of a bot an extension sees.
type Bot struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	DesiredState     string `json:"desired_state"`
	ObservedState    string `json:"observed_state"`
	Runtime          string `json:"runtime"`
	NodeID           string `json:"node_id,omitempty"`
	DiscordUserID    string `json:"discord_user_id,omitempty"`
	DiscordUsername  string `json:"discord_username,omitempty"`
	DiscordAvatarURL string `json:"discord_avatar_url,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	ObservedAtMS     int64  `json:"observed_at_ms,omitempty"`
}

// Notify sends an in-panel notification to the bot's holders (bot set) or the
// acting user.
func Notify(botID, title, body, link string) error {
	return Call("notify", map[string]string{"bot_id": botID, "title": title, "body": body, "link": link}, nil)
}
