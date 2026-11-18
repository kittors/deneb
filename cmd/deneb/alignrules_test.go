package main

import (
	"fmt"
	"image"
	"math"
	"sort"

	"github.com/egoist/mygo/ui"
)

// The rules of one column the user asked for on 11-18 ("这种错位感总让我感觉很
// 难受", DESIGN §3.9): in a card or a dialog, things of one kind keep to one
// column. The marks that lead rows (an icon, a number, a ring, a ball) are
// on one middle line, and the words after them start at one edge; the
// groups of chips start at one edge; what ends a row ends at one edge by
// what shows of it, its ink, read in the frame's pixels. In a row, the
// words stand on one baseline. TestLayoutRules looks at them beside the
// rows' middle lines, the columns' edges and the spacing (rowalign_test.go).

// scopes are the window and each panel in it (a card, a dialog, a well),
// and each layer over it (a dialog's, a card's, out of the flow), each with
// what is inside it and not inside a panel or a layer nested in it: what is
// to keep to one column is looked at within one.
func scopes(root *laid) [][]*laid {
	var out [][]*laid
	var walk func(l *laid, at int)
	walk = func(l *laid, at int) {
		if !l.shown {
			return
		}
		out[at] = append(out[at], l)
		for _, c := range l.children {
			if c.shown && (c.panel || c.absolute) {
				out = append(out, nil)
				walk(c, len(out)-1)
				continue
			}
			walk(c, at)
		}
	}
	out = append(out, nil)
	walk(root, 0)
	return out
}

// onScreen reports whether l shows in the window (w × h).
func onScreen(l *laid, w, h float32) bool {
	return l.rect.Y < h && l.rect.Y+l.rect.H > 0 && l.rect.X < w && l.rect.X+l.rect.W > 0 && l.rect.W > 0 && l.rect.H > 0
}

// things are the children of l that show, in the flow, in their order.
func (l *laid) things() []*laid {
	var out []*laid
	for _, c := range l.children {
		if c.absolute || !c.shown {
			continue
		}
		if _, ok := c.ink(); ok {
			out = append(out, c)
		}
	}
	return out
}

// markInk is what shows of l, when l is a mark that leads a row: an icon or
// a picture, a painted thing no larger than a tile (a number's dot, a
// ring, a ball, with a letter or two in it), or a box holding one of those
// only. ok is false for words and for all else.
func (l *laid) markInk() (ui.Rect, bool) {
	switch {
	case l.kind == 1 || l.kind == 3:
		return ui.Rect{}, false
	case l.kind == 2 || l.kind == 4:
		return l.ink()
	case l.painted:
		if l.rect.W > 44 || l.rect.H > 44 || len([]rune(l.words())) > 2 {
			return ui.Rect{}, false
		}
		return l.rect, true
	}
	if k := l.things(); len(k) == 1 {
		return k[0].markInk()
	}
	return ui.Rect{}, false
}

// firstWords is the first words that show in l, l itself if it is words:
// nil when a painted thing (a chip, a button) comes first.
func (l *laid) firstText() *laid {
	if l.kind == 1 {
		return l
	}
	if l.kind != 0 || l.painted {
		return nil
	}
	for _, c := range l.things() {
		if t := c.firstText(); t != nil {
			return t
		}
		return nil
	}
	return nil
}

// soleText is l's words when l is words, or holds nothing but them, one
// box in another, unpainted (a value's box, a text button at rest); nil
// for all else, a column of two lines among it.
func (l *laid) soleText() *laid {
	if l.kind == 1 {
		return l
	}
	if l.kind != 0 || l.painted {
		return nil
	}
	if k := l.things(); len(k) == 1 {
		return k[0].soleText()
	}
	return nil
}

// near reports whether a and b are of one list: each at most two levels
// under the nearest that holds them both (a list's rows, the rows of its
// groups, a row after a list), not rows of two parts of a card that only
// start at one edge (a card's head and its services).
func near(a, b *laid) bool {
	depth := map[*laid]int{}
	for d, x := 0, a; x != nil; d, x = d+1, x.parent {
		depth[x] = d
	}
	for d, x := 0, b; x != nil; d, x = d+1, x.parent {
		if da, ok := depth[x]; ok {
			return da <= 2 && d <= 2
		}
	}
	return false
}

// marksOff finds, in each scope, the rows of one list (near) led by a mark,
// starting at most 16 apart, whose marks are not on one middle line, or
// whose words after the mark do not start at one edge: the steps' numbers
// and a ring under them, a ball and a tile, an icon and a picture. A row
// drawn as one thing (a chip, a button) is not a list's row.
func marksOff(root *laid, w, h float32) []offCenter {
	var out []offCenter
	for _, scope := range scopes(root) {
		type lead struct {
			mark  ui.Rect
			words float32
			what  string
			row   *laid
		}
		var leads []lead
		for _, e := range scope {
			if !e.row || e.painted || !onScreen(e, w, h) {
				continue
			}
			k := e.things()
			if len(k) < 2 {
				continue
			}
			m, ok := k[0].markInk()
			if !ok {
				continue
			}
			t := k[1].firstText()
			if t == nil {
				continue
			}
			leads = append(leads, lead{m, t.rect.X, firstWords(e.words(), 30), e})
		}
		// Lists: the rows near one another, joined; then, in each, those that
		// start at most 16 apart.
		list := make([]int, len(leads))
		for i := range list {
			list[i] = i
		}
		var find func(int) int
		find = func(i int) int {
			for list[i] != i {
				i = list[i]
			}
			return i
		}
		for i := range leads {
			for j := i + 1; j < len(leads); j++ {
				if near(leads[i].row, leads[j].row) {
					list[find(j)] = find(i)
				}
			}
		}
		lists := map[int][]lead{}
		var order []int
		for i, l := range leads {
			r := find(i)
			if _, ok := lists[r]; !ok {
				order = append(order, r)
			}
			lists[r] = append(lists[r], l)
		}
		var groups [][]lead
		for _, r := range order {
			ls := lists[r]
			sort.SliceStable(ls, func(i, j int) bool { return ls[i].mark.X < ls[j].mark.X })
			for i := 0; i < len(ls); {
				j := i + 1
				for j < len(ls) && ls[j].mark.X-ls[i].mark.X <= 16 {
					j++
				}
				groups = append(groups, ls[i:j])
				i = j
			}
		}
		for _, group := range groups {
			if len(group) < 2 {
				continue
			}
			mids, words := make([]float32, len(group)), make([]float32, len(group))
			for n, g := range group {
				mids[n], words[n] = g.mark.X+g.mark.W/2, g.words
			}
			if s := spread(mids); s > 0.5 {
				oc := offCenter{row: "marks leading rows, off one middle line: " + group[0].what, spread: s}
				for _, g := range group {
					oc.parts = append(oc.parts, fmt.Sprintf("%q: its mark %.1f–%.1f (middle %.1f)", g.what, g.mark.X, g.mark.X+g.mark.W, g.mark.X+g.mark.W/2))
				}
				out = append(out, oc)
			}
			if s := spread(words); s > 0.5 {
				oc := offCenter{row: "words after leading marks, off one edge: " + group[0].what, spread: s}
				for _, g := range group {
					oc.parts = append(oc.parts, fmt.Sprintf("%q: its words from %.1f", g.what, g.words))
				}
				out = append(out, oc)
			}
		}
	}
	return out
}

// leadColumn is the column a row's leading mark is in, when the row puts
// it in one of its own, wider than the mark, its middle on the column's
// (marksOff's rule): where the row starts, for the columns' edges.
func (l *laid) leadColumn() (ui.Rect, bool) {
	if !l.row {
		return ui.Rect{}, false
	}
	k := l.things()
	if len(k) < 2 || k[0].kind != 0 || k[0].painted {
		return ui.Rect{}, false
	}
	m, ok := k[0].markInk()
	if !ok || k[0].rect.W <= m.W+0.5 {
		return ui.Rect{}, false
	}
	return k[0].rect, true
}

// isChip reports whether l is a chip: a pill no higher than 32, wider than
// high, with words in it (design/components.md 多选胶囊、筛选胶囊).
func (l *laid) isChip() bool {
	return l.kind == 0 && l.painted && l.rect.H <= 32 && l.rect.W > l.rect.H && l.radius >= l.rect.H/2-1 && l.words() != ""
}

// chipsOff finds, in each scope, groups of chips (a row of chips only) that
// do not start at one edge: a device's LANs and its services' LANs in My
// Devices (the user's 11-18 screenshot).
func chipsOff(root *laid, w, h float32) []offCenter {
	var out []offCenter
	for _, scope := range scopes(root) {
		type group struct {
			x    float32
			what string
		}
		var groups []group
		for _, e := range scope {
			if !e.row || !onScreen(e, w, h) {
				continue
			}
			k := e.things()
			if len(k) == 0 {
				continue
			}
			all := true
			for _, c := range k {
				all = all && c.isChip()
			}
			if all {
				groups = append(groups, group{k[0].rect.X, firstWords(e.words(), 30)})
			}
		}
		if len(groups) < 2 {
			continue
		}
		xs := make([]float32, len(groups))
		for i, g := range groups {
			xs[i] = g.x
		}
		if s := spread(xs); s > 0.5 {
			oc := offCenter{row: "groups of chips, off one edge: " + groups[0].what, spread: s}
			for _, g := range groups {
				oc.parts = append(oc.parts, fmt.Sprintf("%q from %.1f", g.what, g.x))
			}
			out = append(out, oc)
		}
	}
	return out
}

// inkRight is where what shows of l ends on the right, read in img (the
// frame at scale): the last column of its box, a little past it, in which
// it differs from the ground on its right, a part of a pixel by how far.
func inkRight(img image.Image, scale float32, l *laid) (float32, bool) {
	b := img.Bounds()
	x0 := int(math.Floor(float64(l.rect.X * scale)))
	x1 := int(math.Ceil(float64((l.rect.X + l.rect.W + 2) * scale)))
	y0 := int(math.Floor(float64(l.rect.Y * scale)))
	y1 := int(math.Ceil(float64((l.rect.Y + l.rect.H) * scale)))
	x0, x1 = max(x0, b.Min.X), min(x1, b.Max.X-2)
	y0, y1 = max(y0, b.Min.Y), min(y1, b.Max.Y)
	if x1 <= x0 || y1 <= y0 {
		return 0, false
	}
	ground := func(y int) [3]int {
		r, g, bl, _ := img.At(x1+1, y).RGBA()
		return [3]int{int(r >> 8), int(g >> 8), int(bl >> 8)}
	}
	dev := func(x, y int) int {
		r, g, bl, _ := img.At(x, y).RGBA()
		gr := ground(y)
		d := max(abs(int(r>>8)-gr[0]), abs(int(g>>8)-gr[1]), abs(int(bl>>8)-gr[2]))
		return d
	}
	most := 0
	cols := make([]int, x1-x0)
	for x := x0; x < x1; x++ {
		for y := y0; y < y1; y++ {
			cols[x-x0] = max(cols[x-x0], dev(x, y))
		}
		most = max(most, cols[x-x0])
	}
	if most < 40 {
		return 0, false
	}
	for x := x1 - 1; x >= x0; x-- {
		if c := cols[x-x0]; c > most/4 {
			return (float32(x) + float32(c)/float32(most)) / scale, true
		}
	}
	return 0, false
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// endsOff finds every column whose rows end near its right edge (16 at most
// from it) in words or a mark, whose ends, by what shows of them, are more
// than a point apart: "已是最新版本" over "隐私说明 ↗" in the settings (the
// user's 11-18 screenshot). A row of one thing only is a label, which ends
// where its words do.
func endsOff(root *laid, img image.Image, scale, w, h float32) []offCenter {
	var out []offCenter
	var walk func(*laid)
	walk = func(e *laid) {
		if !e.shown {
			return
		}
		if !e.row && e.kind == 0 && onScreen(e, w, h) {
			end := e.rect.X + e.rect.W - e.pad[1]
			type tail struct {
				right float32
				what  string
			}
			var tails []tail
			var lists [][]tail
			// A list's rows follow one another: one far under the last (a
			// bar at the foot, past a spacer) starts another.
			below := float32(math.Inf(-1))
			for _, c := range e.things() {
				if c.rect.Y-below > 24 && len(tails) > 0 {
					lists, tails = append(lists, tails), nil
				}
				below = c.rect.Y + c.rect.H
				if !c.row {
					continue
				}
				k := c.things()
				if len(k) < 2 {
					continue
				}
				last := k[len(k)-1]
				if last.painted && last.kind == 0 {
					continue // a button, a switch: its own edge, the columns' rule
				}
				r, ok := inkRight(img, scale, last)
				if !ok || r < end-16 || r > end+16 {
					continue
				}
				tails = append(tails, tail{r, firstWords(c.words(), 30)})
			}
			for _, tails := range append(lists, tails) {
				if len(tails) < 2 {
					continue
				}
				rs := make([]float32, len(tails))
				for i, t := range tails {
					rs[i] = t.right
				}
				if s := spread(rs); s > 1 {
					oc := offCenter{row: "row ends, by what shows, off one edge: " + firstWords(e.words(), 40), spread: s}
					for _, t := range tails {
						oc.parts = append(oc.parts, fmt.Sprintf("%q shows to %.1f", t.what, t.right))
					}
					out = append(out, oc)
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

// baselinesOff finds every row that holds words of its own on more than one
// baseline: its words, and those that lead what holds them unpainted (a
// value's box, a text button at rest); not those in a chip or a button
// drawn as one, which is a thing of its own on the row's middle line.
func baselinesOff(root *laid, w, h float32) []offCenter {
	var out []offCenter
	var walk func(*laid)
	walk = func(e *laid) {
		if !e.shown {
			return
		}
		if e.row && !e.wrap && onScreen(e, w, h) {
			var texts []*laid
			for _, c := range e.things() {
				if t := c.soleText(); t != nil && t.hasBase && t.rect.H < 2*t.size*1.6 {
					texts = append(texts, t)
				}
			}
			if len(texts) > 1 {
				bs := make([]float32, len(texts))
				for i, t := range texts {
					bs[i] = t.baseline
				}
				if s := spread(bs); s > 0.5 {
					oc := offCenter{row: "words of a row on more than one baseline: " + firstWords(e.words(), 40), spread: s}
					for _, t := range texts {
						oc.parts = append(oc.parts, fmt.Sprintf("%q (%g) on %.1f", firstWords(t.text, 16), t.size, t.baseline))
					}
					out = append(out, oc)
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

// spread is how far apart the furthest of vs are.
func spread(vs []float32) float32 {
	if len(vs) == 0 {
		return 0
	}
	lo, hi := vs[0], vs[0]
	for _, v := range vs {
		lo, hi = min(lo, v), max(hi, v)
	}
	return hi - lo
}
