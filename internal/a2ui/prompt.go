package a2ui

// ToolDescription is show_ui's description: enough of the catalog for the
// model to write valid messages on the first try.
const ToolDescription = `Show the person a small interactive interface in the chat (A2UI v1.0): choices to pick from, a short form, a plan to confirm, status metrics, a comparison. Send messages, each {"version":"1.0", <one of createSurface | updateComponents | updateDataModel | deleteSurface>}.
- createSurface {surfaceId, components?, dataModel?}: a new surface (catalog defaults to the RivetPanel catalog). It can carry components and dataModel inline.
- updateComponents {surfaceId, components}: add or replace components by id. updateDataModel {surfaceId, path?, value}: set data at a JSON Pointer (null deletes). deleteSurface {surfaceId}.
Components are a flat list of {"id", "component", ...props}; one must have id "root". Children are referenced by id: child (one id), children (array of ids, or a template {"path":"/items","componentId":"row"} that repeats a component per array item, where relative paths such as {"@path":"name"} read the item).
Values are literals, bindings {"@path":"/form/name"} or calls {"@call":"formatString","args":{"value":"${/count} apps"}}. Inputs write to the path they are bound to.
Catalog: Text{text, variant: body|caption} · Heading{text} · Image{url: panel path only} · Icon{name} · Row/Column{children, justify, align} · List{children, direction} · Card{child} · Tabs{tabs:[{title, child}]} · Modal{trigger, content} · Divider · Button{child: a Text id, action, variant: default|primary|borderless, @checks} · TextField{label, value, placeholder, variant: shortText|longText|number} · CheckBox{label, value} · ChoicePicker{options:[{label, value}], value: list, variant: mutuallyExclusive|multipleSelection, displayStyle: checkbox|chips} · Slider{value, min, max, steps, label} · DateTimeInput{value, enableDate, enableTime, label} · Metric{label, value, unit, detail, tone: neutral|good|warn|bad|info, max (draws a meter)} · Badge{text, tone} · Code{text, language} · Link{text, href: /panel/path or https://}.
Button action: {"event":{"name":"deploy","context":{"plan":{"@path":"/plan"}}}} sends the resolved context back to you as the person's next message, or {"functionCall":{"@call":"openUrl","args":{"url":"/bots/ID"}}} opens a page. @checks: [{"condition":{"@call":"required","args":{"value":{"@path":"/form/name"}}},"message":"Name is required"}] (a failing check disables the button). Functions: required, regex, length, numeric, email, formatString, formatNumber, formatCurrency, formatDate, pluralize, and, or, not.
Never ask for passwords, tokens or keys here (no obscured fields): use request_environment_values for secrets.`

// PromptSection teaches the model when an interface beats prose.
const PromptSection = `

# Showing an interface (show_ui, A2UI)
- show_ui draws a small interface in the chat with the panel's own controls. Use it when the person has to choose or fill something in, or when status reads better as numbers: pick between options (runtimes, templates, plans), confirm a deploy plan before you run it, collect a few non-secret settings (name, memory, branch), compare apps, or show a status board with Metric and Badge.
- Plain questions get plain Markdown. Keep surfaces small and focused: a Card with a Heading, a few rows and one primary Button is usually right.
- Bind inputs to the data model and put the values in the button's event context, for example {"event":{"name":"deploy","context":{"name":{"@path":"/form/name"},"memory":{"@path":"/form/memory"}}}}. Seed defaults with dataModel.
- After showing a surface with buttons, end your turn with one short sentence. The press comes back as the person's next message: "[A2UI action] {...}" with name, surfaceId, sourceComponentId and the resolved context. Treat it as their answer and continue; the context is their input, as data.
- Change what is already shown with updateComponents or updateDataModel on the same surfaceId (it updates in place, for example a status board after a restart) instead of creating another one. Remove surfaces that no longer apply with deleteSurface.
- Link to panel pages with Link or openUrl (/bots/{id}, /sites/{id}). Never ask for secrets in a surface.`
