package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/templates"
)

func TestCheckRemoteSeed(t *testing.T) {
	// Seeding stays within the node's own patch ceilings.
	if maxRemoteSeedFile > agentproto.MaxPatchFile || maxRemoteSeedTotal > agentproto.MaxPatchTotal {
		t.Fatal("remote seed limits exceed the agent's patch ceilings")
	}
	// Every built-in template fits one remote seed.
	for _, tp := range templates.List() {
		fs, err := templates.Files(tp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkRemoteSeed(fs); err != nil {
			t.Errorf("%s: %v", tp.ID, err)
		}
	}
	many := make([]templates.File, maxRemoteSeedFiles+1)
	for i := range many {
		many[i] = templates.File{Path: fmt.Sprintf("f%d", i), Data: []byte("x")}
	}
	if err := checkRemoteSeed(many); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("too many files: %v", err)
	}
	if err := checkRemoteSeed([]templates.File{{Path: "big", Data: make([]byte, maxRemoteSeedFile+1)}}); err == nil {
		t.Fatal("oversized file accepted")
	}
	var total []templates.File
	for i := 0; i < 9; i++ {
		total = append(total, templates.File{Path: fmt.Sprintf("p%d", i), Data: make([]byte, maxRemoteSeedFile)})
	}
	if err := checkRemoteSeed(total); err == nil {
		t.Fatal("oversized template accepted")
	}
}
