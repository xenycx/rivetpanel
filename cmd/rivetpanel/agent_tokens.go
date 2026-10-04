package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func agentTokenCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rivetpanel agent-token create|list|revoke|reissue|discard")
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := &service.AgentEnrollmentService{Store: db}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("agent-token create", flag.ContinueOnError)
		location := fs.String("location", domain.LocalLocationID, "location UUID")
		ttl := fs.Duration("ttl", 15*time.Minute, "token lifetime")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: rivetpanel agent-token create [--location UUID] [--ttl 15m] NODE_NAME")
		}
		plain, token, err := svc.Create(ctx, fs.Arg(0), *location, *ttl)
		if err != nil {
			return err
		}
		fmt.Printf("node: %s (%s)\ntoken id: %s\nexpires: %s\nenrollment token (shown once):\n%s\n",
			token.NodeName, token.NodeID, token.ID, time.UnixMilli(token.ExpiresAtMS).UTC().Format(time.RFC3339), plain)
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New("usage: rivetpanel agent-token list")
		}
		rows, err := svc.List(ctx)
		if err != nil {
			return err
		}
		for _, e := range rows {
			state := "active"
			switch {
			case e.UsedAtMS != nil:
				state = "used"
			case e.RevokedAtMS != nil:
				state = "revoked"
			case e.ExpiresAtMS <= time.Now().UnixMilli():
				state = "expired"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", e.ID, e.NodeID, e.NodeName, e.Prefix, state)
		}
		return nil
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: rivetpanel agent-token revoke TOKEN_ID")
		}
		if err := svc.Revoke(ctx, args[1]); err != nil {
			return err
		}
		fmt.Println("enrollment token revoked")
		return nil
	case "reissue":
		fs := flag.NewFlagSet("agent-token reissue", flag.ContinueOnError)
		ttl := fs.Duration("ttl", 15*time.Minute, "token lifetime")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: rivetpanel agent-token reissue [--ttl 15m] NODE_ID")
		}
		plain, token, err := svc.Reissue(ctx, fs.Arg(0), *ttl)
		if errors.Is(err, domain.ErrConflict) {
			return errors.New("node already holds a certificate; revoke it before re-enrolling")
		}
		if err != nil {
			return err
		}
		fmt.Printf("node: %s\ntoken id: %s\nexpires: %s\nenrollment token (shown once):\n%s\n",
			token.NodeID, token.ID, time.UnixMilli(token.ExpiresAtMS).UTC().Format(time.RFC3339), plain)
		return nil
	case "discard":
		if len(args) != 2 {
			return errors.New("usage: rivetpanel agent-token discard NODE_ID")
		}
		err := svc.Discard(ctx, args[1])
		if errors.Is(err, domain.ErrConflict) {
			return errors.New("node already enrolled or still owns workloads; it is not a placeholder")
		}
		if err != nil {
			return err
		}
		fmt.Println("unenrolled agent node and its enrollment token deleted")
		return nil
	default:
		return fmt.Errorf("unknown agent-token command %q", args[0])
	}
}
