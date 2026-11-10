// What shoot.sh needs to know of this Mac's screen, in points, top-left
// origin. shoot.sh compiles it with swiftc.
//
//	mac-windows <pid>   the process's on-screen windows, one per line:
//	                    <window id> <layer> <x> <y> <width> <height>
//	                    (layer 0: an ordinary window)
//	mac-windows screen  <width> <height> <menu bar height> <scale> of the
//	                    main screen
import AppKit
import CoreGraphics

let args = CommandLine.arguments
if args.count == 2, args[1] == "screen" {
	guard let s = NSScreen.main else { exit(1) }
	let bar = s.frame.maxY - s.visibleFrame.maxY
	print(Int(s.frame.width), Int(s.frame.height), Int(bar), s.backingScaleFactor)
	exit(0)
}
guard args.count == 2, let pid = Int32(args[1]) else {
	FileHandle.standardError.write("usage: mac-windows <pid> | screen\n".data(using: .utf8)!)
	exit(2)
}
let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]] ?? []
for w in list {
	guard let owner = w[kCGWindowOwnerPID as String] as? Int32, owner == pid else { continue }
	let id = w[kCGWindowNumber as String] as? Int ?? 0
	let layer = w[kCGWindowLayer as String] as? Int ?? 0
	let b = w[kCGWindowBounds as String] as? [String: Double] ?? [:]
	print(id, layer, Int(b["X"] ?? 0), Int(b["Y"] ?? 0), Int(b["Width"] ?? 0), Int(b["Height"] ?? 0))
}
