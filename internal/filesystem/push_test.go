package filesystem

import "testing"

func TestGitignoreRules(t *testing.T) {
	rules := parseIgnore([]byte("# comment\n*.log\n!keep.log\n/build\ndist/\ndocs/**/*.tmp\n\\#literal\n"), "")
	rules = append(rules, parseIgnore([]byte("secret.json\n"), "sub")...)
	for _, tc := range []struct {
		path string
		dir  bool
		want bool
	}{
		{"a.log", false, true}, {"x/y/a.log", false, true}, {"keep.log", false, false},
		{"build", true, true}, {"x/build", true, false}, {"dist", true, true}, {"dist", false, false}, {"x/dist", true, true},
		{"docs/a/b/c.tmp", false, true}, {"docs/c.tmp", false, true}, {"other/c.tmp", false, false},
		{"#literal", false, true}, {"sub/secret.json", false, true}, {"secret.json", false, false},
	} {
		if got := ignoredBy(rules, tc.path, tc.dir); got != tc.want {
			t.Errorf("%s (dir=%v): ignored=%v want %v", tc.path, tc.dir, got, tc.want)
		}
	}
	for _, tc := range []struct {
		path string
		want bool
	}{{".env", true}, {".env.production", true}, {".env.example", false}, {"a/.env", true}, {"x.pyc", true}, {".rivetpanel-deploy", true}} {
		if got := builtinIgnored(tc.path, false); got != tc.want {
			t.Errorf("builtin %s = %v", tc.path, got)
		}
	}
	if !builtinIgnored("node_modules", true) || !builtinIgnored("a/node_modules", true) || !builtinIgnored("target", true) || builtinIgnored("src/target", true) {
		t.Error("built-in directory rules")
	}
}
