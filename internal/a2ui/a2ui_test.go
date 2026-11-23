package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

const plan = `{"messages":[
 {"version":"1.0","createSurface":{"surfaceId":"plan","dataModel":{"form":{"name":"pixel","memory":512}},"components":[
  {"id":"root","component":"Card","child":"col"},
  {"id":"col","component":"Column","children":["title","name","mem","rt","go"]},
  {"id":"title","component":"Heading","text":"Deploy plan"},
  {"id":"name","component":"TextField","label":"Name","value":{"@path":"/form/name"},"@checks":[{"condition":{"@call":"required","args":{"value":{"@path":"/form/name"}}},"message":"Name is required"}]},
  {"id":"mem","component":"Slider","label":"Memory","value":{"@path":"/form/memory"},"min":128,"max":2048,"steps":15},
  {"id":"rt","component":"ChoicePicker","options":[{"label":"Node.js","value":"nodejs"},{"label":"Python","value":"python"}],"value":{"@path":"/form/runtime"},"displayStyle":"chips"},
  {"id":"go_label","component":"Text","text":"Deploy"},
  {"id":"go","component":"Button","child":"go_label","variant":"primary","action":{"event":{"name":"deploy","context":{"name":{"@path":"/form/name"},"memory":{"@path":"/form/memory"}}}}}
 ]}},
 {"updateDataModel":{"surfaceId":"plan","path":"/form/runtime","value":["nodejs"]}}
]}`

func TestApplyAcceptsAPlanAndRemembersIt(t *testing.T) {
	st := NewState()
	res, err := st.Apply([]byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 1 || res.Components["plan"] != 8 || !strings.Contains(res.Summary(), `Showed surface "plan" (8 components)`) {
		t.Fatalf("result: %+v %q", res, res.Summary())
	}
	// Creating it again is an integrity error; updating works.
	if _, err := st.Apply([]byte(`{"messages":[{"createSurface":{"surfaceId":"plan","components":[{"id":"root","component":"Text","text":"x"}]}}]}`)); err == nil || !strings.Contains(err.Error(), "INTEGRITY_ERROR") {
		t.Fatalf("re-create: %v", err)
	}
	res, err = st.Apply([]byte(`{"messages":[{"updateComponents":{"surfaceId":"plan","components":[{"id":"title","component":"Heading","text":"Deploy plan (2)"}]}}]}`))
	if err != nil || len(res.Updated) != 1 {
		t.Fatalf("update: %v %+v", err, res)
	}
	label, err := st.CheckAction(Action{Name: "deploy", SurfaceID: "plan", SourceComponentID: "go", Context: map[string]any{"name": "pixel"}})
	if err != nil || label != "Deploy" {
		t.Fatalf("action: %q %v", label, err)
	}
	for _, a := range []Action{
		{Name: "delete_everything", SurfaceID: "plan", SourceComponentID: "go"},
		{Name: "deploy", SurfaceID: "plan", SourceComponentID: "title"},
		{Name: "deploy", SurfaceID: "other", SourceComponentID: "go"},
		{Name: "deploy", SurfaceID: "plan", SourceComponentID: "go", Context: map[string]any{"x": strings.Repeat("a", 9000)}},
	} {
		if _, err := st.CheckAction(a); err == nil {
			t.Fatalf("accepted forged action %+v", a)
		}
	}
	if _, err := st.Apply([]byte(`{"messages":[{"deleteSurface":{"surfaceId":"plan"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CheckAction(Action{Name: "deploy", SurfaceID: "plan", SourceComponentID: "go"}); err == nil {
		t.Fatal("deleted surface still accepts actions")
	}
}

func TestApplyRejectsUnsafeOrBrokenSurfaces(t *testing.T) {
	surface := func(components string) string {
		return `{"messages":[{"version":"1.0","createSurface":{"surfaceId":"s","components":[` + components + `]}}]}`
	}
	cases := map[string]string{
		"no root":           surface(`{"id":"a","component":"Text","text":"x"}`),
		"missing child":     surface(`{"id":"root","component":"Card","child":"nope"}`),
		"cycle":             surface(`{"id":"root","component":"Card","child":"b"},{"id":"b","component":"Card","child":"root"}`),
		"unknown component": surface(`{"id":"root","component":"Script","text":"x"}`),
		"video":             surface(`{"id":"root","component":"Video","url":"/a.mp4"}`),
		"unknown prop":      surface(`{"id":"root","component":"Text","text":"x","onclick":"alert(1)"}`),
		"obscured field":    surface(`{"id":"root","component":"TextField","label":"Token","value":{"@path":"/t"},"variant":"obscured"}`),
		"external image":    surface(`{"id":"root","component":"Image","url":"https://evil.example/p.gif?d=1"}`),
		"protocol image":    surface(`{"id":"root","component":"Image","url":"//evil.example/p.gif"}`),
		"javascript link":   surface(`{"id":"root","component":"Link","text":"x","href":"javascript:alert(1)"}`),
		"http link":         surface(`{"id":"root","component":"Link","text":"x","href":"http://plain.example"}`),
		"open url binding":  surface(`{"id":"root","component":"Text","text":{"@call":"openUrl","args":{"url":"https://x.example"}}}`),
		"unknown function":  surface(`{"id":"root","component":"Text","text":{"@call":"eval","args":{"value":"1"}}}`),
		"bad binding":       surface(`{"id":"root","component":"Text","text":{"@path":"/a","extra":1}}`),
		"button no action":  surface(`{"id":"root","component":"Button","child":"l"},{"id":"l","component":"Text","text":"x"}`),
		"bad action url":    surface(`{"id":"root","component":"Button","child":"l","action":{"functionCall":{"@call":"openUrl","args":{"url":"javascript:alert(1)"}}}},{"id":"l","component":"Text","text":"x"}`),
		"other function":    surface(`{"id":"root","component":"Button","child":"l","action":{"functionCall":{"@call":"formatString","args":{"value":"x"}}}},{"id":"l","component":"Text","text":"x"}`),
		"usage hint":        surface(`{"id":"root","component":"Text","text":"x","usageHint":"h1"}`),
		"bad version":       `{"messages":[{"version":"0.8","createSurface":{"surfaceId":"s"}}]}`,
		"two keys":          `{"messages":[{"createSurface":{"surfaceId":"s"},"deleteSurface":{"surfaceId":"s"}}]}`,
		"unknown surface":   `{"messages":[{"updateComponents":{"surfaceId":"zz","components":[]}}]}`,
		"bad surface id":    `{"messages":[{"createSurface":{"surfaceId":"a b"}}]}`,
		"custom in basic":   `{"messages":[{"createSurface":{"surfaceId":"s","catalogId":"` + BasicCatalogID + `","components":[{"id":"root","component":"Metric","label":"x","value":1}]}}]}`,
		"empty":             `{"messages":[]}`,
		"huge data":         `{"messages":[{"createSurface":{"surfaceId":"s","dataModel":{"x":"` + strings.Repeat("a", MaxDataBytes) + `"},"components":[{"id":"root","component":"Text","text":"x"}]}}]}`,
	}
	for name, raw := range cases {
		st := NewState()
		if _, err := st.Apply([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if len(st.Surfaces) != 0 {
			t.Errorf("%s: a rejected call changed the state", name)
		}
	}
}

func TestApplyAcceptsTemplatesFunctionsAndLinks(t *testing.T) {
	raw := `{"messages":[{"createSurface":{"surfaceId":"apps","dataModel":{"apps":[{"name":"a","cpu":3}]},"components":[
	 {"id":"root","component":"Column","children":["list","more","open"]},
	 {"id":"list","component":"List","children":{"path":"/apps","componentId":"row"}},
	 {"id":"row","component":"Row","children":["n","m"],"justify":"spaceBetween"},
	 {"id":"n","component":"Text","text":{"@call":"formatString","args":{"value":"${@index}: ${name}"}}},
	 {"id":"m","component":"Metric","label":"CPU","value":{"@path":"cpu"},"unit":"%","max":100,"tone":"good"},
	 {"id":"more","component":"Link","text":"All apps","href":"/apps"},
	 {"id":"open_l","component":"Text","text":"Docs"},
	 {"id":"open","component":"Button","child":"open_l","variant":"borderless","action":{"functionCall":{"@call":"openUrl","args":{"url":"https://a2ui.org"}}}}
	]}}]}`
	st := NewState()
	if _, err := st.Apply([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	// The browser receives show_ui arguments after the panel re-encoded
	// them; replaying the stored text gives the same state.
	var v any
	_ = json.Unmarshal([]byte(raw), &v)
	b, _ := json.Marshal(v)
	if r := Replay([]string{string(b), `{"messages":[{"bogus":{}}]}`}); len(r.Surfaces["apps"].Components) != 8 {
		t.Fatalf("replay: %+v", r.Surfaces)
	}
}

func TestEnvelopeIsTheV1ActionMessage(t *testing.T) {
	got := Action{Name: "deploy", SurfaceID: "plan", SourceComponentID: "go", Timestamp: "2026-11-22T10:00:00Z"}.Envelope("Deploy")
	if !strings.HasPrefix(got, ActionPrefix) {
		t.Fatal(got)
	}
	var m struct {
		Version string         `json:"version"`
		Action  map[string]any `json:"action"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got, ActionPrefix)), &m); err != nil {
		t.Fatal(err)
	}
	a := m.Action
	if m.Version != "1.0" || a["name"] != "deploy" || a["surfaceId"] != "plan" || a["sourceComponentId"] != "go" || a["context"] == nil || a["metadata"].(map[string]any)["label"] != "Deploy" {
		t.Fatalf("envelope: %v", m)
	}
}

func chartCall(t *testing.T, comps ...map[string]any) []byte {
	t.Helper()
	list := []any{map[string]any{"id": "root", "component": "Column", "children": []any{"c"}}}
	for _, c := range comps {
		list = append(list, c)
	}
	b, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"version": "1.0", "createSurface": map[string]any{"surfaceId": "load", "components": list}}}})
	return b
}

func TestTimeSeriesAndTable(t *testing.T) {
	times := []any{1000, 2000, 3000}
	ok := map[string]any{"id": "c", "component": "TimeSeries", "title": "Host CPU", "unit": "percent", "times": times, "threshold": 80, "height": 160,
		"series":      []any{map[string]any{"label": "CPU", "values": []any{10, nil, 95.5}, "color": "action"}, map[string]any{"label": "worker", "values": []any{1, 2, 3}, "stacked": true, "color": "cat:1"}},
		"annotations": []any{map[string]any{"t": 2000, "end": 3000, "label": "Spike 7", "kind": "spike"}, map[string]any{"t": 1500, "end": nil, "label": "ongoing"}}}
	if _, err := NewState().Apply(chartCall(t, ok)); err != nil {
		t.Fatal(err)
	}
	bound := map[string]any{"id": "c", "component": "TimeSeries", "unit": "cores", "times": map[string]any{"@path": "/cpu/times"}, "series": []any{map[string]any{"label": "a", "values": map[string]any{"@path": "/cpu/a"}}}}
	if _, err := NewState().Apply(chartCall(t, bound)); err != nil {
		t.Fatalf("bound chart: %v", err)
	}
	table := map[string]any{"id": "c", "component": "Table", "title": "Top apps", "bar": "share",
		"columns": []any{map[string]any{"key": "app", "label": "App"}, map[string]any{"key": "cores", "label": "Cores", "format": "cores"}, map[string]any{"key": "share", "label": "Share", "format": "percent", "align": "end"}},
		"rows":    []any{map[string]any{"app": "worker", "cores": 3, "share": 79}, map[string]any{"app": "Host / unattributed", "cores": 0.5, "share": nil}}}
	if _, err := NewState().Apply(chartCall(t, table)); err != nil {
		t.Fatalf("table: %v", err)
	}

	long := make([]any, MaxPoints+1)
	for i := range long {
		long[i] = i
	}
	many := []any{}
	for i := 0; i < MaxSeries+1; i++ {
		many = append(many, map[string]any{"label": "s", "values": times})
	}
	manyRows := []any{}
	for i := 0; i < MaxRows+1; i++ {
		manyRows = append(manyRows, map[string]any{"app": "x"})
	}
	with := func(base map[string]any, k string, v any) map[string]any {
		c := map[string]any{}
		for x, y := range base {
			c[x] = y
		}
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
		return c
	}
	for name, c := range map[string]map[string]any{
		"too many points":    with(ok, "times", long),
		"mismatched length":  with(ok, "series", []any{map[string]any{"label": "CPU", "values": []any{1, 2}}}),
		"string value":       with(ok, "series", []any{map[string]any{"label": "CPU", "values": []any{1, "2", 3}}}),
		"too many series":    with(ok, "series", many),
		"unknown prop":       with(ok, "colour", "red"),
		"unknown series key": with(ok, "series", []any{map[string]any{"label": "CPU", "values": times, "fill": true}}),
		"bad unit":           with(ok, "unit", "furlongs"),
		"bad colour":         with(ok, "series", []any{map[string]any{"label": "CPU", "values": times, "color": "#f00"}}),
		"unsorted times":     with(ok, "times", []any{3000, 2000, 1000}),
		"bad annotation":     with(ok, "annotations", []any{map[string]any{"t": "noon", "label": "x"}}),
		"no times":           with(ok, "times", nil),
		"bad height":         with(ok, "height", 2000),
		"too many rows":      with(table, "rows", manyRows),
		"nested cell":        with(table, "rows", []any{map[string]any{"app": map[string]any{"x": 1}}}),
		"bar not a column":   with(table, "bar", "nope"),
		"duplicate column":   with(table, "columns", []any{map[string]any{"key": "a", "label": "A"}, map[string]any{"key": "a", "label": "B"}}),
		"bad format":         with(table, "columns", []any{map[string]any{"key": "a", "label": "A", "format": "html"}}),
	} {
		if _, err := NewState().Apply(chartCall(t, c)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// Custom components stay out of the basic catalog.
	b, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"createSurface": map[string]any{"surfaceId": "x", "catalogId": BasicCatalogID, "components": []any{with(table, "id", "root")}}}}})
	if _, err := NewState().Apply(b); err == nil {
		t.Fatal("Table accepted in the basic catalog")
	}
	for _, s := range []string{"TimeSeries{", "Table{", "300 points", "32 KiB"} {
		if !strings.Contains(ToolDescription, s) {
			t.Fatalf("tool description lacks %q", s)
		}
	}
	if !strings.Contains(PromptSection, "TimeSeries") || !strings.Contains(PromptSection, "explain_load") {
		t.Fatal("prompt section does not teach charts")
	}
}
