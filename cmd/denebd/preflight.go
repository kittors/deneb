package main

import (
	"flag"
	"fmt"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/engine/tun"
)

// cmdPreflight is `denebd preflight [-state dir]`: it checks, changing
// nothing, whether kernel mode can start here, and says what it would
// change: the interface, the routes, the DNS settings (plan §3 M5, like the
// prototype's -preflight). From the network map the engine last had.
func cmdPreflight(args []string) error {
	fs := flag.NewFlagSet("preflight", flag.ExitOnError)
	stateDir := fs.String("state", defaultStateDir(), "state directory with the last network map")
	mtu := fs.Int("mtu", 0, "the interface's MTU (default 1280)")
	fs.Parse(args)
	cfg, peers, err := engine.KernelPlan(*stateDir, *mtu)
	if err != nil {
		return err
	}
	fmt.Printf("空跑检查（什么都没改）：%s，地址段 %s，%d 个对端\n", cfg.Self, cfg.Block, len(peers))
	steps, err := tun.Plan(cfg, peers)
	if err != nil {
		return fmt.Errorf("内核模式起不来：%w", err)
	}
	for _, s := range append(steps, preflightService()...) {
		fmt.Println("- " + s)
	}
	fmt.Println("检查通过。")
	return nil
}
