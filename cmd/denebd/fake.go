package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// cmdFake serves the fake engine on the local API endpoint, so the desktop
// client can run against believable data before the real engine is up.
func cmdFake(args []string) error {
	fs := flag.NewFlagSet("fake", flag.ExitOnError)
	path := fs.String("socket", socket.ListenPath(), "endpoint to listen on ($DENEB_SOCKET)")
	sim := fs.Bool("simulate", true, "play a loop of path changes")
	fs.Parse(args)
	e := fake.New()
	stop, err := e.Listen(*path)
	if err != nil {
		return err
	}
	defer stop()
	fmt.Fprintf(os.Stderr, "fake engine on %s (Ctrl-C to stop)\n", *path)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *sim {
		go e.Simulate(ctx)
	}
	<-ctx.Done()
	return nil
}
