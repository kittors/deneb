package main

import "github.com/egoist/mygo/ui"

// The menu bar's marks (DESIGN §7.1) in one colour, for the tray's tip and
// any view of the bar: the star with its eyes; the dot at its lower right
// when something waits for me, the star cut away around it; asleep, eyes
// closed and faded, when nothing connects here. The bar itself shows the
// images of design/brand/final (systray.go), the same shapes drawn on each
// size's own grid.
var (
	trayStarDot    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 18 18"><defs><mask id="m" maskUnits="userSpaceOnUse" x="0" y="0" width="18" height="18"><rect width="18" height="18" fill="#fff"/><circle cx="15" cy="15" r="3.5" fill="#000"/></mask></defs><path fill="#000" fill-rule="evenodd" mask="url(#m)" d="M7.23,2.5A1.77,1.77 0 0 1 10.77,2.5A5.2,5.2 0 0 0 14.56,7.29A1.77,1.77 0 0 1 14.56,10.71A5.2,5.2 0 0 0 10.77,15.5A1.77,1.77 0 0 1 7.23,15.5A5.2,5.2 0 0 0 3.44,10.71A1.77,1.77 0 0 1 3.44,7.29A5.2,5.2 0 0 0 7.23,2.5ZM7,8H7A1,1 0 0 1 8,9V10A1,1 0 0 1 7,11H7A1,1 0 0 1 6,10V9A1,1 0 0 1 7,8ZM11,8H11A1,1 0 0 1 12,9V10A1,1 0 0 1 11,11H11A1,1 0 0 1 10,10V9A1,1 0 0 1 11,8Z"/><circle fill="#000" cx="15" cy="15" r="2.5"/></svg>`))
	trayStarHollow = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 18 18"><path fill="#000" fill-opacity="0.5" fill-rule="evenodd" d="M7.23,2.5A1.77,1.77 0 0 1 10.77,2.5A5.2,5.2 0 0 0 14.56,7.29A1.77,1.77 0 0 1 14.56,10.71A5.2,5.2 0 0 0 10.77,15.5A1.77,1.77 0 0 1 7.23,15.5A5.2,5.2 0 0 0 3.44,10.71A1.77,1.77 0 0 1 3.44,7.29A5.2,5.2 0 0 0 7.23,2.5ZM6,10H8V11H6ZM10,10H12V11H10Z"/></svg>`))
)

// trayGlyph draws the menu bar's mark for state ("solid", "dot" or
// "hollow") in col, filling the box it is given.
func trayGlyph(c *ui.Context, state string, col ui.Color) ui.Element {
	g := starGlyph
	switch state {
	case "dot":
		g = trayStarDot
	case "hollow":
		g = trayStarHollow
	}
	return ui.Icon(c, g).Size(16, 16).TextColor(col)
}
