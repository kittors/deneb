# Deneb device avatars

Original editable SVG artwork. Each of 12 objects has eight palette variants;
`../avatar-presets` contains the 128×128 PNGs embedded by the application.

Art direction: pale porcelain and tinted glass, a shared upper-left light,
restrained highlights, curved silhouettes, subtle contact shadow, clear at 56px.
No network request or third-party avatar service is used at runtime. Existing
uploaded avatars are saved device data and are not rewritten on upgrade.

Regenerate from the repository root with Node.js and Sharp available:

```sh
go run deploy/gen-avatar-presets.go
```

`SHARP_MODULE` may name an already installed Sharp module when it is outside
Node's normal module lookup. This dependency is used only when regenerating
artwork. SVG sources and final PNGs are checked in so normal builds need no
image renderer.
