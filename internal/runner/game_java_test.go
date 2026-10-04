package runner

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/blueprint"
)

func TestJavaImageChoice(t *testing.T) {
	s := blueprint.Spec{Name: "Velocity", Images: []blueprint.Image{
		{Label: "Java 21", Java: 21}, {Label: "Java 25", Java: 25}, {Label: "Java 17", Java: 17}}}
	cases := []struct {
		choice string
		need   int
		want   string
		logs   string
	}{
		{"", 0, "Java 21", ""},                         // unknown: the blueprint's first image
		{"", 25, "Java 25", "needs Java 25"},           // Velocity 4 (class file 69)
		{"", 17, "Java 17", "needs Java 17"},           // the lowest Java that works
		{"Java 21", 25, "Java 25", "provides Java 21"}, // an earlier automatic choice that is too old
		{"Java 25", 21, "Java 25", ""},                 // a newer explicit choice is kept
		{"Java 21", 0, "Java 21", ""},
		{"", 99, "Java 25", "needs Java 99"},
		{"Java 17", 99, "Java 17", "no image"},
	}
	for _, c := range cases {
		var log bytes.Buffer
		got := javaImageChoice(s, c.choice, c.need, "Velocity 4.2.0", &log)
		if got != c.want || (c.logs == "") != (log.Len() == 0) || !strings.Contains(log.String(), c.logs) {
			t.Errorf("%q need %d: got %q log %q, want %q", c.choice, c.need, got, log.String(), c.want)
		}
	}
}
