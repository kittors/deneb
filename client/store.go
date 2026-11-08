// Package client is the layer between the view and the engine
// (implementation.md §2.7): the Store the view reads its world from and
// sends its ops to, the demo's store and the engine's, and the debouncing of
// connection paths, which lives here so that it is written and tested once.
package client

import (
	"context"

	"github.com/kittors/deneb/client/model"
)

// Store is where the client's world comes from: the demo's or the engine's.
// The view reads Snapshot on the main thread, at the start of every frame;
// Subscribe's callback runs on another goroutine and only wakes the view,
// which then reads the new snapshot itself.
type Store interface {
	// Snapshot returns the world as it is now. It never blocks, and what it
	// returns never changes.
	Snapshot() *model.Snapshot
	// Subscribe calls fn with every new snapshot until cancel is called.
	Subscribe(fn func(*model.Snapshot)) (cancel func())
	// Do sends op and waits for its answer. An *model.Error says why it did
	// not go through.
	Do(ctx context.Context, op model.Op) (model.Result, error)
	// Login starts a login on this computer and returns the page to open in
	// the browser, where it finishes; the snapshot shows it once it has.
	Login(ctx context.Context) (url string, err error)
	// Open gets a service of a device ready and returns the address the
	// system opens it at, as host:port.
	Open(ctx context.Context, dev model.DeviceID, svc model.Service) (addr string, err error)
	// Preview tells who invites me into which LAN with link, before I say
	// yes or no.
	Preview(ctx context.Context, link string) (*model.Invite, error)
}

// Immediate is implemented by a store whose Do, Login, Open and Preview
// return at once (the demo's): the view calls it in place instead of on
// another goroutine, so that an op takes effect in the very frame it is
// sent, as the prototype's direct changes did.
type Immediate interface{ Immediate() }
