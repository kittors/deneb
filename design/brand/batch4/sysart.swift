// sysart: images of this Mac's own, for the logo batch's scene shots.
//
//   sysart icon <out.png> <px> <file>        the icon macOS draws for a file
//                                             or an app (as Finder and the
//                                             Dock would), px pixels square
//   sysart symbol <out.png> <pt> <scale> <name> [weight]
//                                             an SF Symbol in black, as a
//                                             template image, pt points
//
// Build: swiftc -O -o /tmp/sysart design/brand/batch4/sysart.swift
import AppKit

func save(_ rep: NSBitmapImageRep, _ path: String) {
    let data = rep.representation(using: .png, properties: [:])!
    try! data.write(to: URL(fileURLWithPath: path))
}

func bitmap(_ w: Int, _ h: Int) -> NSBitmapImageRep {
    return NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h, bitsPerSample: 8,
                            samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                            colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
}

let a = CommandLine.arguments
switch a[1] {
case "icon":
    let px = Int(a[3])!
    let img = NSWorkspace.shared.icon(forFile: a[4])
    let rep = bitmap(px, px)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    img.draw(in: NSRect(x: 0, y: 0, width: px, height: px), from: .zero, operation: .sourceOver, fraction: 1)
    NSGraphicsContext.restoreGraphicsState()
    save(rep, a[2])
case "symbol":
    let pt = CGFloat(Double(a[3])!)
    let scale = CGFloat(Double(a[4])!)
    let weights: [String: NSFont.Weight] = ["regular": .regular, "medium": .medium, "semibold": .semibold, "light": .light]
    let w = weights[a.count > 6 ? a[6] : "regular"] ?? .regular
    let cfg = NSImage.SymbolConfiguration(pointSize: pt, weight: w)
    guard let base = NSImage(systemSymbolName: a[5], accessibilityDescription: nil),
          let img = base.withSymbolConfiguration(cfg) else {
        FileHandle.standardError.write("no symbol \(a[5])\n".data(using: .utf8)!)
        exit(1)
    }
    let size = img.size
    let rep = bitmap(Int((size.width * scale).rounded(.up)), Int((size.height * scale).rounded(.up)))
    rep.size = size
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    img.draw(in: NSRect(origin: .zero, size: size))
    NSColor.black.set()
    NSRect(origin: .zero, size: size).fill(using: .sourceAtop)
    NSGraphicsContext.restoreGraphicsState()
    save(rep, a[2])
default:
    FileHandle.standardError.write("usage: sysart icon|symbol ...\n".data(using: .utf8)!)
    exit(2)
}
