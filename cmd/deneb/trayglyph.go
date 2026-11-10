package main

import "github.com/egoist/mygo/ui"

// The menu bar's marks (DESIGN §7.1): the star, the star with a dot at its
// bottom right when something waits for me, and the star's outline when
// nothing connects here.
var (
	trayStarDot    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M12,1.5 C12.29,9.48 14.31,11.69 21.61,12 C14.31,12.31 12.29,14.52 12,22.5 C11.71,14.52 9.69,12.31 2.39,12 C9.69,11.69 11.71,9.48 12,1.5Z"/><circle fill="#000" cx="12" cy="12" r="4.24"/><circle fill="#000" cx="20" cy="20" r="3.2"/></svg>`))
	trayStarHollow = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="#000" stroke-width="1.7" stroke-linejoin="round" d="M12 2.6c.75 5.1 2.95 7.3 8.4 9.4-5.45 2.1-7.65 4.3-8.4 9.4-.75-5.1-2.95-7.3-8.4-9.4C9.05 9.9 11.25 7.7 12 2.6z"/></svg>`))
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
