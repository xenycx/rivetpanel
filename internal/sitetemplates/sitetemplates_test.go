package sitetemplates

import (
	"strings"
	"testing"
)

func TestListAndRender(t *testing.T) {
	list := List()
	want := map[string]bool{"landing": false, "docs": false, "portfolio": false, "coming-soon": false, "game-community": false}
	for _, tp := range list {
		if _, ok := want[tp.ID]; ok {
			want[tp.ID] = true
		}
		if tp.Description == "" || len(tp.Tags) == 0 || tp.Accent == "" || tp.Files == 0 || tp.Bytes == 0 || len(tp.Pages) == 0 {
			t.Errorf("%s: incomplete metadata %+v", tp.ID, tp)
		}
		if tp.Bytes > 64<<10 {
			t.Errorf("%s: %d bytes is too large for a built-in starter", tp.ID, tp.Bytes)
		}
		files, err := Render(tp.ID, Vars{SiteName: `A <b>"site"</b>`, Year: 2026, PanelURL: "https://panel.example.com/"})
		if err != nil {
			t.Fatal(err)
		}
		hasIndex := false
		for _, f := range files {
			s := string(f.Data)
			if strings.Contains(s, "{{") {
				t.Errorf("%s/%s: unfilled placeholder", tp.ID, f.Path)
			}
			if strings.Contains(s, "<b>\"site\"") {
				t.Errorf("%s/%s: site name not escaped", tp.ID, f.Path)
			}
			hasIndex = hasIndex || f.Path == "index.html"
		}
		if !hasIndex {
			t.Errorf("%s: no index.html", tp.ID)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("built-in %s missing", id)
		}
	}
	files, _ := Render("game-community", Vars{SiteName: "x", PanelURL: "https://panel.example.com"})
	for _, f := range files {
		if f.Path == "index.html" && !strings.Contains(string(f.Data), `data-status-api="https://panel.example.com/api/v1/status"`) {
			t.Error("status widget is not pointed at the panel")
		}
	}
}

func TestGetRejectsPaths(t *testing.T) {
	for _, id := range []string{"", "../landing", "Landing", "landing/files", "nope"} {
		if _, ok := Get(id); ok {
			t.Errorf("Get(%q) succeeded", id)
		}
	}
}

func TestPreviewInlinesStyles(t *testing.T) {
	b, err := Preview("docs", "blog/first-post.html", Vars{SiteName: "Demo", Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `<link rel="stylesheet"`) || !strings.Contains(string(b), "<style>") {
		t.Fatal("stylesheet not inlined")
	}
	if _, err := Preview("docs", "../etc/passwd", Vars{}); err == nil {
		t.Fatal("unknown page accepted")
	}
}
