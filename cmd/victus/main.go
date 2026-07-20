// Command victus runs the Victus meal planner server, or manages its database.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("victus exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: victus <serve|migrate> [args...]")
	}

	switch args[0] {
	case "serve":
		return cmdServe(ctx)
	case "migrate":
		return cmdMigrate(ctx, args[1:])
	case "version":
		fmt.Println("victus (dev)")
		return nil
	default:
		return fmt.Errorf("unknown command %q (expected: serve, migrate, version)", args[0])
	}
}
