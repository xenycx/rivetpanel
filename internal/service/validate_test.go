package service

import "testing"

// Container apps own their image and command, so PATH and LD_* are theirs;
// language-runtime apps keep them reserved; RIVET_* is reserved for both.
func TestValidateEnvNameFor(t *testing.T) {
	for _, c := range []struct {
		name      string
		container bool
		ok        bool
	}{
		{"PATH", true, true}, {"LD_SUPERUSER_NAME", true, true}, {"RIVET_X", true, false}, {"bad name", true, false},
		{"PATH", false, false}, {"LD_PRELOAD", false, false}, {"RIVET_X", false, false}, {"TOKEN", false, true},
	} {
		if err := validateEnvNameFor(c.name, false, c.container); (err == nil) != c.ok {
			t.Errorf("%s container=%v: %v", c.name, c.container, err)
		}
	}
}
