// Package proto holds what the control server, the engine and the client
// say to one another: the ids, keys, network maps, user views and
// operations of design/plan/implementation.md §2.6.
//
// Three interfaces share these types:
//
//   - The device interface (/device/v1, device.go): engine ⇄ control
//     server, over TLS pinned to the server's certificate.
//   - The local interface (/v1, local.go): client ⇄ engine, over a Unix
//     socket or a named pipe. The engine owns it; the types live here so the
//     client builds against the same ones.
//   - Operations (ops.go): what a person asks for. The client hands them to
//     the engine, which passes them to the control server as they are; from
//     M3 the Web sends the same ones straight to the server.
//
// Rules the types are built around:
//
//   - Only the control server judges permissions. Clients send what a person
//     wants; a refusal comes back as an Error whose Message is one sentence
//     a toast can show as it is.
//   - One version number. Every change on the server bumps it, and a
//     device's NetMap and its owner's UserView travel in one StreamFrame
//     under the same version, so what the screen shows and what the engine
//     enforces never disagree.
//   - A device learns only what it shares with others. A NetMap holds only
//     the peers this device shares a scope with, and for each of them only
//     the shared scopes; a UserView holds only its person's LANs and what is
//     in them. This is the first layer of isolation (multi-lan-tech §3.5).
//
// Everything travels as JSON. Keys, addresses and port ranges marshal as
// short text ("nodekey:…", "10.67.1.5/32", "tcp/3389"), so a stream can be
// read with curl. Fields are only ever added; a reader ignores what it does
// not know.
package proto
