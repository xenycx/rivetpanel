package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestCoordinatorClaimsAndPreemption(t *testing.T) {
	var c Coordinator
	cl, ctx, err := c.Claim(context.Background(), "b1", "A deployment", true)
	if err != nil {
		t.Fatal(err)
	}
	var busy *domain.BusyError
	if _, _, err := c.Claim(context.Background(), "b1", "A backup", false); !errors.As(err, &busy) || busy.What != "A deployment" {
		t.Fatalf("second claim: %v", err)
	}
	if err := c.Blocked("b1"); err == nil {
		t.Fatal("exclusive claim must block writes")
	}
	if err := c.Blocked("b2"); err != nil {
		t.Fatal("other bots are unaffected")
	}
	go func() {
		<-ctx.Done() // the holder notices cancellation and releases
		time.Sleep(10 * time.Millisecond)
		cl.Release()
	}()
	pctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Preempt(pctx, "b1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Active("b1"); ok {
		t.Fatal("claim still active after preemption")
	}
	cl.Release() // idempotent
	nb, _, err := c.Claim(context.Background(), "b1", "A backup", false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Blocked("b1") != nil {
		t.Fatal("a non-exclusive claim must not block edits")
	}
	nb.Release()
}
