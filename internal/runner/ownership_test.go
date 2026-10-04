package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestChooseOwnership(t *testing.T) {
	denied := func(int, int) error { return errors.New("operation not permitted") }
	allowed := func(int, int) error { return nil }
	probe := func(euid, egid int, chown func(int, int) error) OwnershipProbe {
		return OwnershipProbe{Euid: func() int { return euid }, Egid: func() int { return egid }, CanChown: chown}
	}
	for _, tc := range []struct {
		name              string
		user, owner       string
		userSet, ownerSet bool
		p                 OwnershipProbe
		wantUser, wantOwn string
		fallback          bool
	}{
		{"root keeps default", "65532:65532", "", false, false, probe(0, 0, denied), "65532:65532", "65532:65532", false},
		{"cap_chown keeps default", "65532:65532", "", false, false, probe(1000, 1000, allowed), "65532:65532", "65532:65532", false},
		{"unprivileged falls back", "65532:65532", "", false, false, probe(1000, 1001, denied), "1000:1001", "1000:1001", true},
		{"explicit user kept", "65532:65532", "", true, false, probe(1000, 1000, denied), "65532:65532", "65532:65532", false},
		{"explicit owner kept", "65532:65532", "2000:2000", false, true, probe(1000, 1000, denied), "65532:65532", "2000:2000", false},
		{"already own ids", "1000:1000", "", false, false, probe(1000, 1000, denied), "1000:1000", "1000:1000", false},
		{"root group kept", "65532:65532", "", false, false, probe(1000, 0, denied), "65532:65532", "65532:65532", false},
		{"no probe", "65532:65532", "", false, false, OwnershipProbe{}, "65532:65532", "65532:65532", false},
	} {
		c := ChooseOwnership(tc.user, tc.owner, tc.userSet, tc.ownerSet, tc.p)
		if c.User != tc.wantUser || c.Owner != tc.wantOwn || c.Fallback != tc.fallback {
			t.Errorf("%s: got %+v", tc.name, c)
		}
		if c.Fallback && (c.Default != "65532:65532" || c.Reason == "") {
			t.Errorf("%s: fallback lacks default/reason: %+v", tc.name, c)
		}
	}
}

func TestSharedUIDPolicy(t *testing.T) {
	denied := func(int, int) error { return errors.New("operation not permitted") }
	p := OwnershipProbe{Euid: func() int { return 1000 }, Egid: func() int { return 1001 }, CanChown: denied}
	fallback := ChooseOwnership("65532:65532", "", false, false, p)

	// Development keeps the automatic fallback.
	if c := ApplySharedUIDPolicy(fallback, false, false); !c.Fallback || c.Refused || c.OptedIn || c.User != "1000:1001" || c.RefusalError("the panel", "X=1", "Y") != nil {
		t.Fatalf("development: %+v", c)
	}
	// Production refuses it and keeps the dedicated user.
	c := ApplySharedUIDPolicy(fallback, true, false)
	if !c.Refused || c.Fallback || c.User != "65532:65532" || c.Owner != "65532:65532" {
		t.Fatalf("production: %+v", c)
	}
	if err := c.RefusalError("the panel", "RIVET_ALLOW_SHARED_UID=1", "RIVET_CONTAINER_USER"); err == nil ||
		!strings.Contains(err.Error(), "RIVET_ALLOW_SHARED_UID=1") || !strings.Contains(err.Error(), "operation not permitted") {
		t.Fatalf("refusal error = %v", err)
	}
	// Production with the explicit opt-in runs on the shared uid.
	if c := ApplySharedUIDPolicy(fallback, true, true); !c.Fallback || !c.OptedIn || c.Refused || c.User != "1000:1001" {
		t.Fatalf("opted in: %+v", c)
	}
	// Without a fallback the policy changes nothing.
	root := ChooseOwnership("65532:65532", "", false, false, OwnershipProbe{Euid: func() int { return 0 }, Egid: func() int { return 0 }, CanChown: denied})
	if c := ApplySharedUIDPolicy(root, true, false); c.Refused || c.Fallback || c.User != "65532:65532" {
		t.Fatalf("root: %+v", c)
	}
}
