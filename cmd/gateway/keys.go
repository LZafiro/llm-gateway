package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/LZafiro/llm-gateway/internal/store"
)

func keys(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gateway keys create|list|revoke")
	}
	flags := flag.NewFlagSet("keys "+args[0], flag.ContinueOnError)
	name := flags.String("name", "", "key name")
	rate := flags.Float64("rate", 1, "requests per second")
	burst := flags.Int("burst", 10, "bucket capacity")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "create":
		key, plaintext, err := st.CreateAPIKey(ctx, *name, *rate, *burst)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "created key %q (id %d, rate %.2f/s, burst %d)\n%s\nstore it now, it will not be shown again\n", key.Name, key.ID, key.Rate, key.Burst, plaintext)
		return err
	case "list":
		list, err := st.ListAPIKeys(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tNAME\tPREFIX\tRATE\tBURST\tACTIVE\tCREATED")
		for _, k := range list {
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%.2f\t%d\t%t\t%s\n", k.ID, k.Name, k.Prefix, k.Rate, k.Burst, k.Active, k.CreatedAt.UTC().Format(time.RFC3339))
		}
		return w.Flush()
	case "revoke":
		if err := st.RevokeAPIKey(ctx, *name); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "revoked key %q\n", *name)
		return err
	default:
		return fmt.Errorf("unknown keys subcommand %q, want create, list or revoke", args[0])
	}
}
