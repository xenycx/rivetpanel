# Testing one-click apps

`tests/apps` installs apps on a running panel through its public API and
checks each one end to end: every container reaches running and stays
running, the web interface answers (or the terminal runs a command), it
survives a stack restart, and the stack is deleted cleanly. Use a disposable
panel with Docker:

```bash
RIVET_APPS_PANEL=http://127.0.0.1:8080 RIVET_APPS_EMAIL=admin@example.com \
RIVET_APPS_PASSWORD=… RIVET_APPS=all go test -tags apps -count=1 -timeout 120m -v ./tests/apps
```

`RIVET_APPS=miniflux,ntfy` picks apps, `RIVET_APPS_ADDRESS=site` installs with
a web address (needs site hosting), `RIVET_APPS_KEEP=1` keeps them installed.
The run ends with a pass/fail table.

