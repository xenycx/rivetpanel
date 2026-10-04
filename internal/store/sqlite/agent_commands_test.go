package sqlite

import (
	"context"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestAgentCommandQueueReplayAndCompletion(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	if err := db.EnsureLocalNode(ctx); err != nil {
		t.Fatal(err)
	}
	cmd := domain.AgentCommand{ID: "c1", NodeID: domain.LocalNodeID, Kind: "server.start", PayloadJSON: `{}`,
		IdempotencyKey: "operation-123", DeadlineAtMS: 10_000, CreatedAtMS: 1_000}
	got, created, err := db.QueueAgentCommand(ctx, cmd)
	if err != nil || !created || got.ID != cmd.ID || got.Status != "queued" {
		t.Fatalf("first queue: %+v created=%v err=%v", got, created, err)
	}
	duplicate := cmd
	duplicate.ID = "c2"
	got, created, err = db.QueueAgentCommand(ctx, duplicate)
	if err != nil || created || got.ID != "c1" {
		t.Fatalf("duplicate queue: %+v created=%v err=%v", got, created, err)
	}
	pending, err := db.ListPendingAgentCommands(ctx, domain.LocalNodeID, 2_000, 1_500, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v err=%v", pending, err)
	}
	if err := db.MarkAgentCommandDelivered(ctx, domain.LocalNodeID, "c1", 2_000); err != nil {
		t.Fatal(err)
	}
	// retryBeforeMS is an absolute lease cutoff. The command delivered at
	// 2,000 must not replay while the cutoff remains before that timestamp.
	pending, _ = db.ListPendingAgentCommands(ctx, domain.LocalNodeID, 2_500, 1_999, 10)
	if len(pending) != 0 {
		t.Fatal("command replayed before its delivery lease elapsed")
	}
	pending, _ = db.ListPendingAgentCommands(ctx, domain.LocalNodeID, 3_000, 2_500, 10)
	if len(pending) != 1 {
		t.Fatal("command was not replayed after its delivery lease elapsed")
	}
	if err := db.CompleteAgentCommand(ctx, domain.LocalNodeID, "c1", true, "", 3_100); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteAgentCommand(ctx, domain.LocalNodeID, "c1", true, "", 3_200); err != nil {
		t.Fatalf("same acknowledgement must be idempotent: %v", err)
	}
	if err := db.CompleteAgentCommand(ctx, domain.LocalNodeID, "c1", false, "late failure", 3_300); err == nil {
		t.Fatal("opposite acknowledgement replaced the terminal result")
	}
}

func TestAgentCommandExpiry(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	db.EnsureLocalNode(ctx)
	_, _, err := db.QueueAgentCommand(ctx, domain.AgentCommand{ID: "expired", NodeID: domain.LocalNodeID,
		Kind: "server.stop", PayloadJSON: `{}`, IdempotencyKey: "operation-expired", CreatedAtMS: 1, DeadlineAtMS: 2})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := db.ListPendingAgentCommands(ctx, domain.LocalNodeID, 3, 3, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("expired pending: %+v err=%v", pending, err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM agent_commands WHERE id = 'expired'`).Scan(&status); err != nil || status != "expired" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}
