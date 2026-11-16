package main

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The rows of the window keep what is in them on one middle line (the
// user, 2026-11-16: 添加设备 sat higher than the words and the circle beside
// it in the tray). MyGo's Tester gives no layout tree, so this reads the
// last frame's elements from MyGo's own runtime, by reflection and only to
// read: it knows MyGo v0.7.4's insides, and skips on any other version
// until checked against it.

const layoutMyGo = "v0.7.4"

// laid is an element of the last frame as it was laid out.
type laid struct {
	kind     uint64 // 0 box, 1 text, 2 image, 3 input, 4 icon
	row      bool
	wrap     bool
	align    uint64 // 0 start, 1 center, …, 7 baseline, 8 auto
	self     uint64
	text     string
	rect     ui.Rect // in the window
	shown    bool    // not hidden, not see-through, not out of the flow
	absolute bool
	toggle   bool // a switch, a check box: its knob's inset is its own drawing
	// A mark whose strokes keep clear of its box's sides, by these parts of
	// its width: where its ink is (glyphInk).
	inkL, inkR float32
	painted    bool // a fill, a border, a shadow or its own drawing: it shows as a whole
	// What lays it out, as set: top, right, bottom, left; between children.
	pad, margin [4]float32
	gapX, gapY  float32
	radius      float32 // its largest corner
	panel       bool    // filled, with a round corner: a dialog, a card, a tile
	children    []*laid
}

// glyphInk is, for the marks that stand at a row's end by their strokes
// (glyphRoom), how much of their 24 units their strokes keep clear on the
// left and the right: the cross from 7 to 17, the arrow from 7 to 17.5,
// each with half its 1.6 stroke.
func glyphInk() map[uintptr][2]float32 {
	return map[uintptr][2]float32{
		reflect.ValueOf(uiGlyphs["close"]).Pointer():    {6.2 / 24, 6.2 / 24},
		reflect.ValueOf(uiGlyphs["external"]).Pointer(): {6.2 / 24, 5.7 / 24},
	}
}

// MyGo's flags (ui/element.go, v0.7.4).
const (
	flagAbsoluteBit  = 1 << 8
	flagInvisibleBit = 1 << 18
	flagToggleBit    = 1 << 20
)

func layoutOf(t *testing.T, tt *ui.Tester) *laid {
	t.Helper()
	if v := mygoVersion(); v != layoutMyGo {
		t.Skipf("the layout reader knows MyGo %s's insides, not %s's: check it against them", layoutMyGo, v)
	}
	root := deref(deref(deref(reflect.ValueOf(tt)).FieldByName("rt")).FieldByName("c")).FieldByName("root")
	return readLaid(root)
}

// deref is v, or what it points to.
func deref(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Pointer {
		return v.Elem()
	}
	return v
}

func mygoVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, m := range bi.Deps {
		if m.Path == "github.com/egoist/mygo" {
			return m.Version
		}
	}
	return ""
}

// readLaid reads a *node and those inside it.
func readLaid(p reflect.Value) *laid {
	if p.IsNil() {
		return nil
	}
	n := p.Elem()
	l := &laid{
		kind:  n.FieldByName("kind").Uint(),
		row:   n.FieldByName("row").Bool(),
		wrap:  n.FieldByName("wrap").Bool(),
		align: n.FieldByName("align").Uint(),
		self:  n.FieldByName("self").Uint(),
		text:  n.FieldByName("text").String(),
	}
	flags := n.FieldByName("flags").Uint()
	l.absolute = flags&flagAbsoluteBit != 0
	l.toggle = flags&flagToggleBit != 0
	if svg := n.FieldByName("svg"); l.kind == 4 && !svg.IsNil() {
		if room, ok := glyphInk()[svg.Pointer()]; ok {
			l.inkL, l.inkR = room[0], room[1]
		}
	}
	op := float32(n.FieldByName("opacity").Float())
	l.shown = !n.FieldByName("hidden").Bool() && flags&flagInvisibleBit == 0 && !(n.FieldByName("opacitySet").Bool() && op < 0.02)
	border := n.FieldByName("border")
	for i := range 4 {
		if border.Index(i).Float() > 0 {
			l.painted = true
		}
	}
	filled := n.FieldByName("bg").FieldByName("A").Uint() > 0
	if filled || !n.FieldByName("paintFn").IsNil() || n.FieldByName("shadows").Len() > 0 {
		l.painted = true
	}
	for i := range 4 {
		l.pad[i] = float32(n.FieldByName("pad").Index(i).Float())
		l.margin[i] = float32(n.FieldByName("margin").Index(i).Float())
		l.radius = max(l.radius, float32(n.FieldByName("radius").Index(i).Float()))
	}
	l.gapX, l.gapY = float32(n.FieldByName("gapX").Float()), float32(n.FieldByName("gapY").Float())
	if st := n.FieldByName("st"); !st.IsNil() {
		s := st.Elem()
		r := ui.Rect{X: float32(s.FieldByName("x").Float()), Y: float32(s.FieldByName("y").Float()), W: float32(s.FieldByName("w").Float()), H: float32(s.FieldByName("h").Float())}
		if w := s.FieldByName("world"); w.FieldByName("Set").Bool() {
			r = affineBounds(w, r)
		}
		l.rect = r
	}
	l.panel = filled && l.radius >= 12 && l.kind == 0 && l.radius < l.rect.H/2-1 // not a pill

	for c := n.FieldByName("first"); !c.IsNil(); c = c.Elem().FieldByName("next") {
		if k := readLaid(c); k != nil {
			l.children = append(l.children, k)
		}
	}
	return l
}

// affineBounds is the bounds of r moved by MyGo's scene.Affine w.
func affineBounds(w reflect.Value, r ui.Rect) ui.Rect {
	f := func(k string) float32 { return float32(w.FieldByName(k).Float()) }
	a, b, c, d, x, y := f("A"), f("B"), f("C"), f("D"), f("X"), f("Y")
	if (b != 0 || c != 0) && math.Abs(float64(a-d)) < 1e-3 && math.Abs(float64(b+c)) < 1e-3 {
		// Turned (a spinner, a chevron): what turns about its middle shows
		// in the bounds it has upright, there, scaled as it is.
		k := float32(math.Hypot(float64(a), float64(b)))
		mx, my := r.X+r.W/2, r.Y+r.H/2
		cx, cy := a*mx+c*my+x, b*mx+d*my+y
		return ui.Rect{X: cx - r.W*k/2, Y: cy - r.H*k/2, W: r.W * k, H: r.H * k}
	}
	x0, y0, x1, y1 := float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.Inf(-1))
	for _, pt := range [][2]float32{{r.X, r.Y}, {r.X + r.W, r.Y}, {r.X, r.Y + r.H}, {r.X + r.W, r.Y + r.H}} {
		px, py := a*pt[0]+c*pt[1]+x, b*pt[0]+d*pt[1]+y
		x0, y0, x1, y1 = min(x0, px), min(y0, py), max(x1, px), max(y1, py)
	}
	return ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// ink is the part of l that shows: a painted element as a whole, a text,
// icon or picture, or what those inside an unpainted box show; ok is false
// when nothing does.
func (l *laid) ink() (ui.Rect, bool) {
	if !l.shown || l.rect.W <= 0 || l.rect.H <= 0 {
		return ui.Rect{}, false
	}
	if l.painted || l.kind != 0 || len(l.children) == 0 {
		if l.kind == 0 && !l.painted {
			return ui.Rect{}, false // an empty box shows nothing
		}
		if l.kind != 0 && !l.painted {
			// Words, a mark, a picture: inside their own padding; a mark
			// whose strokes keep clear of its sides, where they are.
			r := l.rect
			r.X, r.Y = r.X+l.pad[3], r.Y+l.pad[0]
			r.W, r.H = r.W-l.pad[1]-l.pad[3], r.H-l.pad[0]-l.pad[2]
			r.X, r.W = r.X+r.W*l.inkL, r.W*(1-l.inkL-l.inkR)
			return r, r.W > 0 && r.H > 0
		}
		return l.rect, true
	}
	return l.innerInk()
}

// contentInk is l's ink, but where a painted box reaches across what holds
// it (from start to end: a chosen row's light, a list's well), its things
// in place of it: its light reaches past the edge the words keep to, as a
// menu row's does.
func (l *laid) contentInk(start, end float32) (ui.Rect, bool) {
	if !l.shown || l.rect.W <= 0 || l.rect.H <= 0 {
		return ui.Rect{}, false
	}
	band := l.painted && l.kind == 0 && len(l.children) > 0 && l.rect.X <= start+0.5 && l.rect.X+l.rect.W >= end-0.5
	if !band && (l.painted || l.kind != 0 || len(l.children) == 0) {
		return l.ink()
	}
	in0, in1 := l.rect.X+l.pad[3], l.rect.X+l.rect.W-l.pad[1]
	var out ui.Rect
	have := false
	for _, c := range l.children {
		if c.absolute {
			continue
		}
		r, ok := c.contentInk(in0, in1)
		if !ok {
			continue
		}
		if !have {
			out, have = r, true
			continue
		}
		x0, y0 := min(out.X, r.X), min(out.Y, r.Y)
		x1, y1 := max(out.X+out.W, r.X+r.W), max(out.Y+out.H, r.Y+r.H)
		out = ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	}
	return out, have
}

// innerInk is what those inside l show, together: a row's things, without
// its light.
func (l *laid) innerInk() (ui.Rect, bool) {
	var out ui.Rect
	have := false
	for _, c := range l.children {
		if c.absolute {
			continue
		}
		r, ok := c.ink()
		if !ok {
			continue
		}
		if !have {
			out, have = r, true
			continue
		}
		x0, y0 := min(out.X, r.X), min(out.Y, r.Y)
		x1, y1 := max(out.X+out.W, r.X+r.W), max(out.Y+out.H, r.Y+r.H)
		out = ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	}
	return out, have
}

// words is what l and those inside it say, for the report.
func (l *laid) words() string {
	var out []string
	var walk func(*laid)
	walk = func(e *laid) {
		if !e.shown {
			return
		}
		if e.text != "" {
			out = append(out, e.text)
		}
		for _, c := range e.children {
			walk(c)
		}
	}
	walk(l)
	return strings.Join(out, " · ")
}

// offCenter is a row whose children's shown parts are not on one middle
// line: more than half a point apart.
type offCenter struct {
	row    string
	spread float32
	parts  []string
}

// rowsOffCenter finds, in what shows in the window (w × h), every row that
// centers its children and whose children's shown parts are not on one
// middle line. A child that places itself (AlignSelf), or is out of the
// flow, has its reasons; a row that lines its children up otherwise (Start,
// Baseline…) is left as it is.
func rowsOffCenter(root *laid, w, h float32) []offCenter {
	// DENEB_ALIGN_START: the rows that line up at the top too, to look
	// over by hand (the audit of 2026-11-16).
	startToo := os.Getenv("DENEB_ALIGN_START") != ""
	var out []offCenter
	var walk func(*laid)
	walk = func(e *laid) {
		if !e.shown {
			return
		}
		if e.row && (e.align == 1 || startToo && e.align == 0) && e.rect.Y < h && e.rect.Y+e.rect.H > 0 && e.rect.X < w && e.rect.X+e.rect.W > 0 {
			type part struct {
				mid, top, bottom float32
				what             string
			}
			var parts []part
			for _, c := range e.children {
				if c.absolute || c.self != 8 && c.self != 1 {
					continue
				}
				r, ok := c.ink()
				if !ok || r.H < 2 {
					continue
				}
				parts = append(parts, part{r.Y + r.H/2, r.Y, r.Y + r.H, fmt.Sprintf("%q %.1f–%.1f", firstWords(c.words(), 24), r.Y, r.Y+r.H)})
			}
			// A row that wraps has lines of its own: each is looked at.
			lines := [][]part{parts}
			if e.wrap && len(parts) > 1 {
				sort.SliceStable(parts, func(i, j int) bool { return parts[i].top < parts[j].top })
				lines = [][]part{{parts[0]}}
				bottom := parts[0].bottom
				for _, p := range parts[1:] {
					if p.top >= bottom {
						lines = append(lines, []part{p})
						bottom = p.bottom
						continue
					}
					lines[len(lines)-1] = append(lines[len(lines)-1], p)
					bottom = max(bottom, p.bottom)
				}
			}
			for _, line := range lines {
				if len(line) < 2 {
					continue
				}
				lo, hi := line[0].mid, line[0].mid
				for _, p := range line {
					lo, hi = min(lo, p.mid), max(hi, p.mid)
				}
				if hi-lo > 0.5 {
					oc := offCenter{row: firstWords(e.words(), 60), spread: hi - lo}
					for _, p := range line {
						oc.parts = append(oc.parts, fmt.Sprintf("%s (middle %.1f)", p.what, p.mid))
					}
					out = append(out, oc)
				}
			}
		}
		// A small painted thing, a button, a chip, a tile, keeps what is in
		// it on its own middle line.
		if e.painted && e.kind == 0 && e.rect.H <= 56 && e.rect.Y < h && e.rect.Y+e.rect.H > 0 {
			var in ui.Rect
			have := false
			for _, c := range e.children {
				if c.absolute {
					continue
				}
				if r, ok := c.ink(); ok {
					if !have {
						in, have = r, true
						continue
					}
					x0, y0 := min(in.X, r.X), min(in.Y, r.Y)
					x1, y1 := max(in.X+in.W, r.X+r.W), max(in.Y+in.H, r.Y+r.H)
					in = ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
				}
			}
			if have && in.H < e.rect.H {
				if d := (in.Y + in.H/2) - (e.rect.Y + e.rect.H/2); d > 0.5 || d < -0.5 {
					out = append(out, offCenter{row: firstWords(e.words(), 60), spread: max(d, -d), parts: []string{
						fmt.Sprintf("the thing %.1f–%.1f (middle %.1f)", e.rect.Y, e.rect.Y+e.rect.H, e.rect.Y+e.rect.H/2),
						fmt.Sprintf("what is in it %.1f–%.1f (middle %.1f)", in.Y, in.Y+in.H, in.Y+in.H/2),
					}})
				}
			}
		}
		for _, c := range e.children {
			walk(c)
		}
	}
	walk(root)
	return out
}

func firstWords(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// edgesOff finds, in what shows in the window (w × h), every column whose
// children do not start at one left edge, or whose children that reach its
// right end there do not end at one right edge: more than half a point
// apart. A column that centers what is in it, or puts it at its end, is
// left as it is; so is a child out of the flow.
func edgesOff(root *laid, w, h float32) []offCenter {
	var out []offCenter
	var walk func(*laid)
	walk = func(e *laid) {
		if !e.shown {
			return
		}
		onScreen := e.rect.Y < h && e.rect.Y+e.rect.H > 0 && e.rect.X < w && e.rect.X+e.rect.W > 0
		if !e.row && e.kind == 0 && (e.align == 0 || e.align == 3 || e.align == 8) && onScreen {
			type part struct {
				r, alt ui.Rect // its ink; and its things', a band's light aside
				what   string
				kids   *laid
			}
			var parts []part
			start, end := e.rect.X+e.pad[3], e.rect.X+e.rect.W-e.pad[1]
			for _, c := range e.children {
				if c.absolute || c.self != 8 && c.self != 0 && c.self != 3 {
					continue
				}
				r, ok := c.ink()
				alt, okAlt := c.contentInk(start, end)
				if !okAlt {
					alt = r
				}
				// As wide as the column, what is in it in its middle (a
				// button across the foot of a panel): its edges are the
				// column's own.
				across := r.X <= start+0.5 && r.X+r.W >= end-0.5
				if across && alt.W < r.W-1 && math.Abs(float64(alt.X+alt.W/2-(r.X+r.W/2))) < 1 {
					continue
				}
				if ok && r.W >= 2 {
					parts = append(parts, part{r, alt, firstWords(c.words(), 24), c})
				}
			}
			if len(parts) > 1 {
				// Those that start near the column's left start at one edge;
				// what keeps to the right, or the middle, has its reasons.
				near := func(x float32) bool { return x > start-16 && x < start+16 }
				var starts []part
				for _, p := range parts {
					if near(p.r.X) || near(p.alt.X) {
						starts = append(starts, p)
					}
				}
				if len(starts) > 1 {
					lefts := make([][2]float32, len(starts))
					for i, p := range starts {
						lefts[i] = [2]float32{p.r.X, p.alt.X}
					}
					if spread := offOneEdge(lefts); spread > 0.5 {
						oc := offCenter{row: "left edges of " + firstWords(e.words(), 60), spread: spread}
						for _, p := range starts {
							oc.parts = append(oc.parts, fmt.Sprintf("%q from %.1f%s", p.what, p.r.X, also(p.alt.X, p.r.X, "from")))
						}
						out = append(out, oc)
					}
				}
				// Those that reach the column's right end, and end in a mark,
				// a button or a line rather than in words, end at one edge.
				var ends []part
				for i, p := range parts {
					if p.kids.kind == 1 || !p.kids.endsInMark() {
						continue
					}
					right, alt := p.r.X+p.r.W, p.alt.X+p.alt.W
					if right > end-16 && right < end+16 || alt > end-16 && alt < end+16 {
						ends = append(ends, parts[i])
					}
				}
				if len(ends) > 1 {
					rights := make([][2]float32, len(ends))
					for i, p := range ends {
						rights[i] = [2]float32{p.r.X + p.r.W, p.alt.X + p.alt.W}
					}
					if spread := offOneEdge(rights); spread > 0.5 {
						oc := offCenter{row: "right edges of " + firstWords(e.words(), 60), spread: spread}
						for _, p := range ends {
							oc.parts = append(oc.parts, fmt.Sprintf("%q to %.1f%s", p.what, p.r.X+p.r.W, also(p.alt.X+p.alt.W, p.r.X+p.r.W, "to")))
						}
						out = append(out, oc)
					}
				}
			}
		}
		for _, c := range e.children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// endBox is what shows at l's start (or its end, last) as a box: l, if it
// is painted; else the first (or last) of those inside it that shows,
// looked into the same way; nil for words, a mark or a picture there.
func (l *laid) endBox(last bool) *laid {
	if !l.shown || l.kind != 0 {
		return nil
	}
	if l.painted {
		return l
	}
	return l.innerEndBox(last)
}

// pillWords is how far words at a pill's round end keep from it: their
// corners then clear its curve about as far as a ball's nested there do.
const pillWords = 16

// endWords is the words that show first at l's start (or its end, last),
// looked into what is not painted as innerEndBox does; nil when a box, a
// mark or a picture shows first there.
func (l *laid) endWords(last bool) *laid {
	for i := range l.children {
		k := l.children[i]
		if last {
			k = l.children[len(l.children)-1-i]
		}
		if k.absolute {
			continue
		}
		if _, ok := k.ink(); !ok {
			continue
		}
		switch {
		case !k.shown:
			return nil
		case k.kind == 1:
			return k
		case k.kind != 0 || k.painted:
			return nil
		}
		return k.endWords(last)
	}
	return nil
}

// innerEndBox is endBox of the first (or last) of those inside l that shows.
func (l *laid) innerEndBox(last bool) *laid {
	for i := range l.children {
		k := l.children[i]
		if last {
			k = l.children[len(l.children)-1-i]
		}
		if k.absolute {
			continue
		}
		if _, ok := k.ink(); ok {
			return k.endBox(last)
		}
	}
	return nil
}

// offOneEdge is how far the things of a column are from one edge, each
// meeting it at either of its two places (its ink's, its things'): 0 when
// one edge has them all, else the least spread of the edges they keep to.
func offOneEdge(places [][2]float32) float32 {
	best := float32(math.Inf(1))
	for _, cand := range places {
		for _, x := range cand {
			var worst float32
			for _, p := range places {
				worst = max(worst, min(float32(math.Abs(float64(p[0]-x))), float32(math.Abs(float64(p[1]-x)))))
			}
			best = min(best, worst)
		}
	}
	return best
}

// also says a thing's second place, when it has one.
func also(alt, x float32, way string) string {
	if math.Abs(float64(alt-x)) < 0.5 {
		return ""
	}
	return fmt.Sprintf(" (its things %s %.1f)", way, alt)
}

// endsInMark reports whether what shows at l's right end is a mark (an
// icon, a picture), a button or a line, rather than words: the last of it
// in the row it ends.
func (l *laid) endsInMark() bool {
	if !l.shown {
		return false
	}
	if l.kind != 0 {
		return l.kind != 1
	}
	if l.painted || len(l.children) == 0 {
		return true
	}
	for i := len(l.children) - 1; i >= 0; i-- {
		c := l.children[i]
		if c.absolute {
			continue
		}
		if _, ok := c.ink(); ok {
			return c.endsInMark()
		}
	}
	return false
}

// spacingOff finds every element that shows whose padding, margins or gap
// are not of the spacing scale, multiples of 4 (DESIGN §3.9), and every
// panel (a dialog, a card, a tile) whose padding is not the same all round.
// A switch's padding is how far in its knob runs, a part of how it is drawn,
// as Apple's and Material's are: not a space between things.
func spacingOff(root *laid, w, h float32) []offCenter {
	var out []offCenter
	four := func(v float32) bool {
		if math.IsInf(float64(v), 0) {
			return true // an auto margin
		}
		q := v / 4
		return math.Abs(float64(q-float32(math.Round(float64(q))))) < 0.01
	}
	var walk func(*laid)
	walk = func(e *laid) {
		if !e.shown || e.rect.Y >= h || e.rect.Y+e.rect.H <= 0 || e.rect.X >= w || e.rect.X+e.rect.W <= 0 {
			return
		}
		var bad []string
		for i, side := range []string{"top", "right", "bottom", "left"} {
			if !four(e.pad[i]) && !e.toggle {
				bad = append(bad, fmt.Sprintf("padding %s %g", side, e.pad[i]))
			}
			if !four(e.margin[i]) {
				bad = append(bad, fmt.Sprintf("margin %s %g", side, e.margin[i]))
			}
		}
		if len(e.children) > 1 && e.row && !four(e.gapX) || len(e.children) > 1 && !e.row && !four(e.gapY) {
			bad = append(bad, fmt.Sprintf("gap %g/%g", e.gapX, e.gapY))
		}
		// A panel that sets room over or under what it holds (a card, a
		// dialog, a well) has the same room all round; one that sets only its
		// sides is a bar whose height centers its things (a list's row, a
		// button), which the middle-line check looks at.
		if e.panel && (e.pad[0] != 0 || e.pad[2] != 0) && (e.pad[0] != e.pad[1] || e.pad[1] != e.pad[2] || e.pad[2] != e.pad[3]) {
			bad = append(bad, fmt.Sprintf("a panel's padding not the same all round: %v", e.pad))
		}
		// A pill's round ends hold a ball or a tile as far from the end as
		// from the top and the bottom, as the end's curve and theirs nest
		// (AGENTS.md §2.3, §2.4). Words or a mark at an end are no box: they
		// keep the room their corners need from the curve (the tray's 16).
		// A switch's knob runs from end to end: its drawing, not this.
		if e.row && e.painted && e.kind == 0 && !e.toggle && e.radius >= e.rect.H/2-1 && len(e.children) > 0 {
			for _, last := range []bool{false, true} {
				box := e.innerEndBox(last)
				if box == nil {
					continue
				}
				r := box.rect
				end, side := r.X-e.rect.X, "start"
				if last {
					end, side = e.rect.X+e.rect.W-(r.X+r.W), "end"
				}
				top, bottom := r.Y-e.rect.Y, e.rect.Y+e.rect.H-(r.Y+r.H)
				if math.Abs(float64(end-top)) > 0.5 || math.Abs(float64(end-bottom)) > 0.5 {
					bad = append(bad, fmt.Sprintf("a pill's %s holds %q %g from it, %g from the top, %g from the bottom", side, firstWords(box.words(), 16), end, top, bottom))
				}
			}
			// Words at a round end of a pill as high as a panel (the tray,
			// the toast; not a button) keep 16 from it.
			for _, last := range []bool{false, true} {
				wd := e.endWords(last)
				if wd == nil || e.rect.H < 40 {
					continue
				}
				end, side := wd.rect.X-e.rect.X, "start"
				if last {
					end, side = e.rect.X+e.rect.W-(wd.rect.X+wd.rect.W), "end"
				}
				if end < pillWords-0.5 {
					bad = append(bad, fmt.Sprintf("a pill's %s has words %q %g from it, not %d", side, firstWords(wd.words(), 16), end, pillWords))
				}
			}
		}
		if len(bad) > 0 {
			out = append(out, offCenter{row: firstWords(e.words(), 60), parts: bad})
		}
		for _, c := range e.children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// layoutAllowed is what the checks find that is meant, each with why: the
// kind of finding ("row", "edge", "spacing"), the state ("" for every
// one), and the first words of what it is about, as the report says them.
var layoutAllowed = []struct{ kind, state, row, why string }{
	{"edge", "账号", "left edges of Deneb", "the account card open, the sidebar's account button is lit: its round light stands 4 out of the ball, which keeps to the edge"},
}

func layoutAllows(kind, state, row string) bool {
	for _, a := range layoutAllowed {
		if a.kind == kind && (a.state == "" || a.state == state) && strings.HasPrefix(row, a.row) {
			return true
		}
	}
	return false
}

// layoutFinding is one thing the layout checks find: its kind ("row",
// "edge", "spacing"), and where.
type layoutFinding struct {
	kind string
	offCenter
}

// layoutFindings is what the layout checks find in what tt shows (DESIGN
// §3.9): rows whose things are off one middle line, columns whose things
// are off one edge, room off the spacing scale or a panel's not the same
// all round; the largest first in each kind.
func layoutFindings(t *testing.T, tt *ui.Tester, w, h float32) []layoutFinding {
	t.Helper()
	tt.Frame()
	tree := layoutOf(t, tt)
	var out []layoutFinding
	for _, k := range []struct {
		kind string
		find func(*laid, float32, float32) []offCenter
	}{{"row", rowsOffCenter}, {"edge", edgesOff}, {"spacing", spacingOff}} {
		bad := k.find(tree, w, h)
		sort.SliceStable(bad, func(i, j int) bool { return bad[i].spread > bad[j].spread })
		for _, b := range bad {
			out = append(out, layoutFinding{k.kind, b})
		}
	}
	return out
}

// checkLayout fails the test on each finding in what tt shows that
// layoutAllowed does not name, for the state named, at a size.
func checkLayout(t *testing.T, tt *ui.Tester, state, at string, w, h float32) []layoutFinding {
	t.Helper()
	found := layoutFindings(t, tt, w, h)
	for _, f := range found {
		if !layoutAllows(f.kind, state, f.row) {
			t.Errorf("%s %s: %s %.1f off: %s\n\t%s", at, state, f.kind, f.spread, f.row, strings.Join(f.parts, "\n\t"))
		}
	}
	return found
}

// TestLayoutRules looks at the window in the states a person sees, at its
// smallest, usual and a large size (DESIGN §3.9, design/plan/m7/
// layout-audit.md): what is in a row sits on one middle line, what is in a
// column starts at one edge, the room between things is of the spacing
// scale, and a panel has the same room all round. When DENEB_LAYOUT_AUDIT
// names a file, it writes there all it finds, the allowed too, each once
// with the sizes it is found at: the audit is made from it.
func TestLayoutRules(t *testing.T) {
	type seen struct {
		f      layoutFinding
		state  string
		sizes  []string
		allows bool
	}
	all := map[string]*seen{}
	var order []string
	// DENEB_LAYOUT_ONLY names the states to look at, as DENEB_ALIGN_ONLY
	// does for the stills; all by default.
	want := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_LAYOUT_ONLY"), ",") {
		if k != "" {
			want[k] = true
		}
	}
	for _, size := range [][2]float32{{980, 640}, {1180, 740}, {1440, 900}} {
		w, h := size[0], size[1]
		at := fmt.Sprintf("%.0f×%.0f", w, h)
		for _, st := range alignStates {
			if len(want) > 0 && !want[st.name] {
				continue
			}
			r := alignRig(t, st.first, w, h)
			if !st.open(r, h) {
				t.Errorf("%s %s: could not get there: %q", at, st.name, r.tt.Texts())
				continue
			}
			r.away()
			for _, f := range checkLayout(t, r.tt, st.name, at, w, h) {
				key := f.kind + "|" + st.name + "|" + f.row + "|" + strings.Join(f.parts, ";")
				if g, ok := all[key]; ok {
					g.sizes = append(g.sizes, at)
					continue
				}
				all[key] = &seen{f, st.name, []string{at}, layoutAllows(f.kind, st.name, f.row)}
				order = append(order, key)
			}
		}
	}
	out := os.Getenv("DENEB_LAYOUT_AUDIT")
	if out == "" {
		return
	}
	var b strings.Builder
	for _, k := range order {
		g := all[k]
		mark := ""
		if g.allows {
			mark = " (allowed)"
		}
		fmt.Fprintf(&b, "[%s] %s (%s) %.1f%s: %s\n", g.f.kind, g.state, strings.Join(g.sizes, ", "), g.f.spread, mark, g.f.row)
		for _, p := range g.f.parts {
			fmt.Fprintf(&b, "\t%s\n", p)
		}
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d findings in %s", len(order), out)
}

// settingsWith opens the settings with the app's update in stage, as a
// check leaves it.
func (r *m3rig) settingsWith(stage updateStage) bool {
	r.a.updates = newAppUpdater(r.a)
	r.a.updates.stage = stage
	return r.openWords("设置")
}

// away takes the pointer out of the window, and lets what it lit fade: the
// layout is looked at, and its stills taken, at rest.
func (r *m3rig) away() {
	r.tt.Move(-40, -40)
	r.run(600 * time.Millisecond)
}

// alignRig is the demo a state of alignStates starts from, at w × h.
func alignRig(t *testing.T, first int, w, h float32) *m3rig {
	t.Helper()
	var r *m3rig
	switch {
	case first >= 0:
		// The first time's steps, for a person with no LAN yet.
		r = newM6Rig(t, true, first)
	case first == freshDemo:
		// A person with no LAN yet, past the first time's steps.
		r = newM6Rig(t, true, -1)
	case first == m7Demo:
		// The demo as far as M7: diagnostics, the account's own.
		r = newM7Rig(t)
	default:
		return myRig(t, "", "light", false, "", w, h)
	}
	r.tt.SetSize(int(w), int(h))
	r.run(1200 * time.Millisecond)
	return r
}

// m7Demo starts a state from the demo as far as M7, no first-time steps;
// freshDemo from it for a person with no LAN yet.
const (
	m7Demo    = -2
	freshDemo = -3
)

// alignStates are the window's states the rows are looked at in, from the
// demo as myRig has it (M3 and M4, a LAN's canvas).
var alignStates = []struct {
	name  string
	first int // the first time's step it starts at (newM6Rig); m7Demo; or -1: myRig
	open  func(r *m3rig, h float32) bool
}{
	{"局域网", -1, func(r *m3rig, h float32) bool { return true }},
	{"托盘空着", -1, func(r *m3rig, h float32) bool {
		for _, id := range r.trayIDs() {
			r.tap(func() ui.Point { return r.traySlot(id) })
			r.run(1200 * time.Millisecond)
		}
		r.park()
		r.run(5 * time.Second)
		return true
	}},
	{"设备卡片", -1, func(r *m3rig, h float32) bool { return r.openAt(r.device("game")) }},
	{"人物卡片", -1, func(r *m3rig, h float32) bool { return r.openAt(func() ui.Point { return r.ball("mia") }) }},
	{"局域网卡片", -1, func(r *m3rig, h float32) bool {
		// As a click on the LAN's name in the header does.
		r.a.selected, r.a.more = lanCard, false
		r.run(1300 * time.Millisecond)
		return true
	}},
	{"中转站卡片", -1, func(r *m3rig, h float32) bool { return r.openAt(r.relay("hk")) }},
	{"提示条", -1, func(r *m3rig, h float32) bool {
		d := r.a.device("game")
		r.a.post(func(c *ui.Context) { r.a.notify(c, d, "已移出 我的游戏主机", func() {}) })
		r.run(900 * time.Millisecond)
		return r.tt.HasText("撤销")
	}},
	{"我的设备", -1, func(r *m3rig, h float32) bool { return r.openWords("我的设备") }},
	{"我的设备详情", -1, func(r *m3rig, h float32) bool {
		return r.openWords("我的设备") && r.openWords("详情kittors 的 MacBook")
	}},
	{"添加设备", -1, func(r *m3rig, h float32) bool { return r.openWords("我的设备") && r.openWords("添加设备") }},
	{"共享给我", -1, func(r *m3rig, h float32) bool { return r.openWords("共享给我") }},
	{"全部", -1, func(r *m3rig, h float32) bool { return r.openWords("全部") }},
	{"设置", -1, func(r *m3rig, h float32) bool { return r.openWords("设置") }},
	{"账号", -1, func(r *m3rig, h float32) bool { return r.openWords("账号") }},
	{"新建局域网", -1, func(r *m3rig, h float32) bool { return r.openWords("新建局域网") }},
	{"邀请朋友", -1, func(r *m3rig, h float32) bool { return r.openWords("邀请朋友") }},
	{"用我自己的服务器", -1, func(r *m3rig, h float32) bool { return r.openWords("用我自己的服务器") }},
	{"设备卡片更多", -1, func(r *m3rig, h float32) bool { return r.openAt(r.device("game")) && r.openWords("更多") }},
	{"重命名", -1, func(r *m3rig, h float32) bool { return r.openAt(r.device("game")) && r.openWords("重命名") }},
	// The settings after a check: what 软件更新 says, beside it (the user's
	// first still, 11-16, had 已是最新版本 under it, indented).
	{"设置·已是最新版本", -1, func(r *m3rig, h float32) bool { return r.settingsWith(updateCurrent) }},
	{"设置·正在检查更新", -1, func(r *m3rig, h float32) bool { return r.settingsWith(updateChecking) }},
	{"注销账号", m7Demo, func(r *m3rig, h float32) bool { return r.openWords("设置") && r.openWords("注销账号") }},
	{"注销以后", m7Demo, func(r *m3rig, h float32) bool {
		// The email typed again, and Return: the sign-in, saying so, with
		// the relays others were on.
		if !r.openWords("设置") || !r.openWords("注销账号") {
			return false
		}
		r.run(1500 * time.Millisecond)
		f, ok := r.tt.Find("你的邮箱")
		if !ok {
			return false
		}
		if f.Y+f.H > h-24 {
			r.tt.Scroll(f.X+f.W/2, h/2, 0, f.Y+f.H-h+120)
			r.run(400 * time.Millisecond)
			if f, ok = r.tt.Find("你的邮箱"); !ok {
				return false
			}
		}
		r.tap(at(f.X+f.W/2, f.Y+f.H/2))
		r.run(200 * time.Millisecond)
		r.tt.Type("kittors@example.com")
		r.tt.Key(0, ui.KeyEnter)
		r.run(4 * time.Second)
		return r.tt.HasText("账号已经注销")
	}},
	{"诊断", m7Demo, func(r *m3rig, h float32) bool {
		return r.openAt(r.device("mac")) && r.openWords("更多") && r.openWords("导出诊断包")
	}},
	{"没联网", -1, func(r *m3rig, h float32) bool { return r.after("nonet") }},
	// The starting page with no view to show, the control server out of
	// reach (engine-restart-offline.md §2.3): before 10 s; past them, with
	// devices connected by the network map kept, and with none; and this
	// computer without a network.
	{"启动·正在连接", -1, func(r *m3rig, h float32) bool {
		r.d.Simulate("control-out")
		r.run(2 * time.Second)
		return r.tt.HasText("正在连接 Deneb…")
	}},
	{"启动·连不上·有连着的设备", -1, func(r *m3rig, h float32) bool { return r.waitOut("control-out") }},
	{"启动·连不上·没有连着的设备", -1, func(r *m3rig, h float32) bool { return r.waitOut("control-out-alone") }},
	{"启动·没联网", -1, func(r *m3rig, h float32) bool { return r.waitOut("control-out", "nonet") }},
	// The diagnostics package put together from there: the toast, with no
	// tile at its start, its words at the round end.
	{"启动·导出诊断包", -1, func(r *m3rig, h float32) bool {
		if !r.waitOut("control-out") || !r.openWords(startDiagLabel) {
			return false
		}
		r.run(1500 * time.Millisecond)
		return r.tt.HasText("诊断包存到了「下载」")
	}},
	{"连不上 Deneb", -1, func(r *m3rig, h float32) bool { return r.after("engine-away") }},
	{"中转站连不上", -1, func(r *m3rig, h float32) bool { return r.after("relay-down") }},
	{"提醒", -1, func(r *m3rig, h float32) bool { return r.after("notice") }},
	{"有人要加入", -1, func(r *m3rig, h float32) bool { return r.after("pending-member") }},
	{"收到邀请", -1, func(r *m3rig, h float32) bool { return r.after("invitation") }},
	{"删除设备确认", m7Demo, func(r *m3rig, h float32) bool {
		if !r.openWords("我的设备") || !r.openWords("详情我的游戏主机") {
			return false
		}
		clickVisibleDeviceAction(r.t, r.tt, "删除这台设备", h)
		r.run(600 * time.Millisecond)
		return r.tt.HasText("确认删除设备")
	}},
	{"共享给我空着", freshDemo, func(r *m3rig, h float32) bool { return r.openWords("共享给我") }},
	{"我的设备没有局域网", freshDemo, func(r *m3rig, h float32) bool { return r.openWords("我的设备") }},
	{"登录", 0, func(r *m3rig, h float32) bool { return true }},
	{"装网卡", 1, func(r *m3rig, h float32) bool { return true }},
	{"第一个局域网", 2, func(r *m3rig, h float32) bool { return true }},
}

// after has the demo do what event names, and lets it come in.
func (r *m3rig) after(event string) bool {
	r.d.Simulate(event)
	r.run(1500 * time.Millisecond)
	return true
}

// waitOut has the demo do the events, and waits past the starting page's
// patience, till its line says why it still waits.
func (r *m3rig) waitOut(events ...string) bool {
	for _, e := range events {
		r.d.Simulate(e)
	}
	r.run(startingPatience + 800*time.Millisecond)
	return r.tt.HasText(startNoControl) || r.tt.HasText(startNoNetwork)
}

// openAt clicks where to says and lets what it opens come in.
func (r *m3rig) openAt(to func() ui.Point) bool {
	r.tap(to)
	r.run(1300 * time.Millisecond)
	return true
}

// openWords clicks what shows s, if anything does, and lets what it opens
// come in.
func (r *m3rig) openWords(s string) bool {
	b, ok := r.tt.Find(s)
	if !ok {
		return false
	}
	r.tap(at(b.X+b.W/2, b.Y+b.H/2))
	r.run(1300 * time.Millisecond)
	return true
}

// The menu bar's panel, with chores to do: the same rules.
func TestPanelLayoutRules(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	for _, ev := range []string{"pending-device", "pending-member", "login-due", "invitation"} {
		d.Simulate(ev)
	}
	tt := ui.NewTester(a.panel, panelWidth, panelMaxHeight)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	checkLayout(t, tt, "菜单栏面板", fmt.Sprintf("%.0f×%.0f", float32(panelWidth), float32(panelMaxHeight)), panelWidth, panelMaxHeight)
}

// A LAN's header has two lines across it: its name and 邀请朋友 on one
// middle line, the line under the name and the seats' words on the other.
func TestHeaderLinesMeet(t *testing.T) {
	r := myRig(t, "", "light", false, "", 1180, 740)
	tree := layoutOf(t, r.tt)
	mid := func(s string) (float32, bool) {
		var found *laid
		var walk func(*laid)
		walk = func(e *laid) {
			if found != nil || !e.shown {
				return
			}
			if e.text == s && e.rect.X > sidebarWidth && e.rect.Y < headerHeight {
				found = e
				return
			}
			for _, c := range e.children {
				walk(c)
			}
		}
		walk(tree)
		if found == nil {
			return 0, false
		}
		return found.rect.Y + found.rect.H/2, true
	}
	for _, pair := range [][2]string{{"周末联机", "邀请朋友"}, {"4 个人，5 台设备都连上了", "还能加 1 人"}} {
		a, okA := mid(pair[0])
		b, okB := mid(pair[1])
		if !okA || !okB {
			t.Fatalf("the header has no %q or %q: %q", pair[0], pair[1], r.tt.Texts())
		}
		if d := a - b; d > 0.5 || d < -0.5 {
			t.Errorf("%q's middle line is at %.1f, %q's at %.1f", pair[0], a, pair[1], b)
		}
	}
}
