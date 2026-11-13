---
title: Widget data shapes
lede: The exact `data` object each of the 18 widget kinds expects, with an example you can paste into a push.
icon: code
aliases: [widget-data]
glance:
  - 18 widget kinds
  - 4096 bytes of data per widget
related: [observe/widgets, observe/telemetry]
---

## How to read this page

Every widget in a push has a `kind` and a `data` object. The panel checks
`data` against its kind when the push arrives and refuses the push if it does
not fit, naming the widget. [enforced: panel] The common fields (`key`,
`title`, `group`, `span` and so on) are in [Dashboard
widgets](01-widgets.md#widget-fields).

In the shapes below, `?` marks an optional field. Numbers must be finite.

## Numbers and states

| Kind | Data shape | Example |
|---|---|---|
| `metric` | `{value, unit?, detail?, delta?}`: `value` and `delta` are numbers. A non-zero `delta` shows as a rise or fall next to `detail` | `{"value":1284,"unit":"servers","delta":12,"detail":"this week"}` |
| `status` | `{state, text}`: `state` is `good`, `warn`, `bad` or `neutral` | `{"state":"good","text":"Connected"}` |
| `progress` | `{value, max?, label?, unit?}`: `max` is a number, 100 when left out | `{"value":68,"max":100,"unit":"%"}` |
| `gauge` | Same as `progress` | `{"value":0.7,"max":4,"label":"Load","unit":"cores"}` |
| `kv` | `{items}`: up to 30 `{key, value}` objects; `key` is a string, `value` a string, number or boolean | `{"items":[{"key":"Version","value":"2.4.1"},{"key":"Shards","value":4}]}` |

## Series

| Kind | Data shape | Example |
|---|---|---|
| `chart` | `{points, unit?}`: up to 100 `{label?, value}` objects, drawn as bars (the latest 40 show) | `{"unit":"cmds","points":[{"label":"Mon","value":120},{"label":"Tue","value":96}]}` |
| `line`, `area`, `sparkline` | `{points, unit?}`: up to 100 `{label?, value}` objects (the latest 60 are drawn) | `{"unit":"ms","points":[{"label":"now","value":42}]}` |
| `donut` | `{values}`: up to 30 `{label?, value}` objects; the total shows in the middle | `{"values":[{"label":"EU","value":62},{"label":"US","value":38}]}` |
| `heatmap` | `{cells}`: up to 120 `{label?, value}` objects, drawn 12 to a row | `{"cells":[{"label":"00:00","value":3},{"label":"01:00","value":7}]}` |

`label`, when present, must be a string.

## Tables

| Kind | Data shape | Example |
|---|---|---|
| `table` | `{columns, rows}`: up to 12 columns, each a string or `{label}`; up to 50 rows, each an array of up to 12 cells. A cell is a string, a number or a link `{label, url}` | `{"columns":["Guild","Members"],"rows":[["Lobby",1200],[{"label":"Docs","url":"https://example.com"},0]]}` |

## Text

| Kind | Data shape | Example |
|---|---|---|
| `text` | `{text}`: plain text; line breaks are kept | `{"text":"Maintenance at 02:00 UTC."}` |
| `markdown` | `{text}`: lines starting with `#`, `##`, `###`, `- ` or `> ` become headings, bullets and quotes. Raw HTML is shown as text and never rendered | `{"text":"## Today\n- 3 new servers"}` |
| `log` | `{text}`: shown as bounded monospace output | `{"text":"12:00 ready\n12:01 shard 2 resumed"}` |
| `code` | `{code, language?}` | `{"code":"!ping","language":"text"}` |

## Links and images

| Kind | Data shape | Example |
|---|---|---|
| `link` | `{url, label?}`: an absolute `http` or `https` URL; the button reads **Open link** without a label | `{"url":"https://example.com/status","label":"Status"}` |
| `image` | `{url, alt?}`: an absolute `http` or `https` URL; `alt` falls back to the widget's title | `{"url":"https://example.com/graph.png","alt":"Weekly graph"}` |

For `link` and `image`, any other URL scheme is refused when the bot pushes
it. [enforced: panel] A table cell link whose URL is not `http` or
`https` is shown empty.
Images load without sending a referrer.
