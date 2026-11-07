package service

import (
	"reflect"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestShellWords(t *testing.T) {
	for in, want := range map[string][]string{
		`serve --port 8080`:            {"serve", "--port", "8080"},
		`sh -c "echo 'hi there' && x"`: {"sh", "-c", "echo 'hi there' && x"},
		`a\ b 'c d' ""`:                {"a b", "c d", ""},
	} {
		got, err := shellWords(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %q %v", in, got, err)
		}
	}
	if _, err := shellWords(`"open`); err == nil {
		t.Error("unterminated quote accepted")
	}
}

func TestComposeInterpolate(t *testing.T) {
	vars := map[string]string{"SET": "x", "EMPTY": ""}
	missing := map[string]bool{}
	for in, want := range map[string]string{
		"${SET}":          "x",
		"$SET/a":          "x/a",
		"${EMPTY:-d}":     "d",
		"${EMPTY-d}":      "",
		"${UNSET-d}":      "d",
		"${SET:+on}":      "on",
		"$$HOME":          "$HOME",
		"pre-${UNSET}-po": "pre--po",
	} {
		if got := composeInterpolate(in, vars, missing); got != want {
			t.Errorf("%q = %q want %q", in, got, want)
		}
	}
	if !missing["UNSET"] || missing["SET"] {
		t.Errorf("missing = %v", missing)
	}
}

func TestParseBytesAndDotEnv(t *testing.T) {
	for in, want := range map[string]int64{"512m": 512 << 20, "1g": 1 << 30, "1.5gb": 3 << 29, "1024k": 1 << 20, "4096": 4096} {
		if got, err := parseBytes(in); err != nil || got != want {
			t.Errorf("%q = %d %v", in, got, err)
		}
	}
	env := parseDotEnv("# c\nA=1\nexport B=\"two words\"\nC='x'\n\nbad\n")
	if env["A"] != "1" || env["B"] != "two words" || env["C"] != "x" || len(env) != 3 {
		t.Errorf("env %v", env)
	}
}

func TestContainerPolicyAndConfig(t *testing.T) {
	p := ContainerPolicy{Registries: []string{"docker.io", "ghcr.io"}}
	for in, want := range map[string]string{"nginx": "docker.io/library/nginx:latest", "ghcr.io/o/a:v1": "ghcr.io/o/a:v1",
		"redis:7@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "docker.io/library/redis:7@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
		if got, err := p.Normalize(in); err != nil || got != want {
			t.Errorf("%q = %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"quay.io/a/b", "UPPER/case", "", "localhost:5000/x"} {
		if _, err := p.Normalize(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := (ContainerPolicy{}).Normalize("nginx"); err == nil {
		t.Error("an empty registry list allowed an image")
	}
	ok := domain.ContainerConfig{User: "1000:1000", Writable: []string{"/tmp", "/var/run"}, Volumes: []domain.Volume{{Name: "data", Path: "/data"}}}
	got, err := checkContainerConfig(ok)
	if err != nil || len(got.Writable) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []domain.ContainerConfig{
		{User: "0:0"}, {User: "root"}, {User: "1000:0"},
		{Writable: []string{"relative"}}, {Writable: []string{"/sys/x"}}, {Writable: []string{"/a/../b"}},
		{Volumes: []domain.Volume{{Name: "a", Path: "/x"}, {Name: "a", Path: "/y"}}},
		{Volumes: []domain.Volume{{Name: "a", Path: "/x"}}, Writable: []string{"/x/cache"}},
		{Volumes: []domain.Volume{{Name: "a", Path: "/tmp"}}},
	} {
		if _, err := checkContainerConfig(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestDependencyCycle(t *testing.T) {
	if c := dependencyCycle([]ComposeServicePlan{{Service: "a", DependsOn: []string{"b"}}, {Service: "b"}}); c != "" {
		t.Errorf("false cycle %q", c)
	}
	if c := dependencyCycle([]ComposeServicePlan{{Service: "a", DependsOn: []string{"b"}}, {Service: "b", DependsOn: []string{"a"}}}); c == "" {
		t.Error("cycle missed")
	}
}
