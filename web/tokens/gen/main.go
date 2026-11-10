// Command gen writes the web's design tokens, web/tokens.json and
// web/tokens.css, from the desktop client's code, so that the web looks the
// same as the desktop and follows it when it changes (DESIGN.md §14.1):
//
//   - the palettes of cmd/deneb/theme.go and its base font size;
//   - the owners' colors of cmd/deneb/model.go;
//   - the named numbers, durations, springs and poses of the desktop's
//     drawing code (canvas.go, links.go, panels.go, ...), under their own
//     names, file by file;
//   - MyGo's springs (Smooth, Snappy, Bouncy), which the desktop uses by name.
//
// It reads the source with go/parser and runs nothing of the desktop's.
// Strings are left out: no addresses or keys leave cmd/deneb this way.
//
// Run from the repository's root: go run ./web/tokens/gen
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// drawing are the desktop's files whose named values are design: sizes,
// timings, springs, poses and colors.
var drawing = []string{"canvas.go", "links.go", "panels.go", "allview.go", "view.go", "views.go", "menubar.go", "glyphs.go", "model.go", "theme.go"}

func main() {
	log.SetFlags(0)
	desk := "cmd/deneb"
	files := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, name := range drawing {
		f, err := parser.ParseFile(fset, filepath.Join(desk, name), nil, 0)
		if err != nil {
			log.Fatalf("gen: %v (run from the repository's root)", err)
		}
		files[name] = f
	}

	out := map[string]any{
		"generatedBy": "go run ./web/tokens/gen, from cmd/deneb and MyGo's ui package. Do not edit by hand.",
	}
	theme := files["theme.go"]
	color := map[string]any{
		"light":  palette(theme, "lightPalette"),
		"dark":   palette(theme, "darkPalette"),
		"owners": list(files["model.go"], "ownerColors"),
	}
	for _, k := range []string{"white", "black"} {
		if v := topValue(theme, k); v != nil {
			color[k] = v
		}
	}
	out["color"] = color
	out["theme"] = map[string]any{"fontSize": assigned(theme, "FontSize")}

	desktop := map[string]any{}
	for _, name := range drawing {
		if name == "theme.go" {
			continue // its palettes are color.light and color.dark
		}
		if vals := named(files[name]); len(vals) > 0 {
			desktop[name] = vals
		}
	}
	out["desktop"] = desktop
	out["spring"] = mygoSprings()

	js, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	write("web/tokens.json", append(js, '\n'))
	// The desktop's own colors outside the palettes (offlineGrey) go into
	// the stylesheet too, under their names.
	extra := map[string]any{}
	for _, vals := range desktop {
		for k, v := range vals.(map[string]any) {
			if c, ok := v.(string); ok && isColor(c) {
				extra[k] = c
			}
		}
	}
	cssOut := css(color, out["theme"].(map[string]any), extra)
	write("web/tokens.css", cssOut)
	// The same for the design demos (design/demos/web), so that they have the
	// desktop's colors served from design/demos too, not only opened as files.
	write("design/demos/web/tokens.css", cssOut)
}

func write(path string, b []byte) {
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", path)
}

// palette reads a palette literal: field name → color, the bool fields left
// out.
func palette(f *ast.File, name string) map[string]any {
	lit, ok := topExpr(f, name).(*ast.CompositeLit)
	if !ok {
		log.Fatalf("gen: no palette %s in theme.go", name)
	}
	m := map[string]any{}
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if c, ok := value(kv.Value).(string); ok {
			m[kv.Key.(*ast.Ident).Name] = c
		}
	}
	return m
}

// list reads an array literal of values.
func list(f *ast.File, name string) []any {
	lit, ok := topExpr(f, name).(*ast.CompositeLit)
	if !ok {
		log.Fatalf("gen: no list %s", name)
	}
	var l []any
	for _, e := range lit.Elts {
		l = append(l, value(e))
	}
	return l
}

// assigned finds `x.field = <value>` anywhere in f, as theme.go sets the
// base font size inside colors().
func assigned(f *ast.File, field string) any {
	var v any
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		if sel, ok := as.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == field {
			v = value(as.Rhs[0])
		}
		return true
	})
	return v
}

// topExpr returns the value of a top-level const or var.
func topExpr(f *ast.File, name string) ast.Expr {
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range g.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if n.Name == name && i < len(vs.Values) {
					return vs.Values[i]
				}
			}
		}
	}
	return nil
}

func topValue(f *ast.File, name string) any {
	if e := topExpr(f, name); e != nil {
		return value(e)
	}
	return nil
}

// named returns every top-level const and var of f whose value reads as
// design: a number, a duration (in milliseconds), a color, or a literal of
// those (a spring, a pose). Strings and anything computed are left out.
func named(f *ast.File) map[string]any {
	m := map[string]any{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || (g.Tok != token.CONST && g.Tok != token.VAR) {
			continue
		}
		for _, s := range g.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if i >= len(vs.Values) || n.Name == "_" {
					continue
				}
				if n.Name == "ownerColors" {
					continue // color.owners
				}
				if v := value(vs.Values[i]); v != nil {
					if _, isStr := v.(string); isStr && !isColor(v.(string)) {
						continue
					}
					m[n.Name] = v
				}
			}
		}
	}
	return m
}

func isColor(s string) bool { return strings.HasPrefix(s, "#") || strings.HasPrefix(s, "rgba(") }

// value evaluates the literals the desktop writes its design in. It returns
// nil for anything else.
func value(e ast.Expr) any {
	switch x := e.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT, token.FLOAT:
			f, err := strconv.ParseFloat(x.Value, 64)
			if err != nil {
				return nil
			}
			return f
		}
		return nil // strings: never copied out
	case *ast.ParenExpr:
		return value(x.X)
	case *ast.UnaryExpr:
		if f, ok := value(x.X).(float64); ok && x.Op == token.SUB {
			return -f
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" {
			if ms, ok := durations[x.Sel.Name]; ok {
				return ms
			}
		}
		if id, ok := x.X.(*ast.Ident); ok && id.Name == "ui" {
			return "ui." + x.Sel.Name // a value of MyGo's, as ui.Snappy
		}
	case *ast.BinaryExpr:
		a, b := value(x.X), value(x.Y)
		fa, oka := a.(float64)
		fb, okb := b.(float64)
		if oka && okb {
			switch x.Op {
			case token.MUL:
				return fa * fb
			case token.QUO:
				if fb != 0 {
					return fa / fb
				}
			case token.ADD:
				return fa + fb
			case token.SUB:
				return fa - fb
			}
		}
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			// time.Duration(n) and float32(n) are their argument.
			if id, ok := x.Fun.(*ast.Ident); ok && len(x.Args) == 1 && (id.Name == "float32" || id.Name == "float64") {
				return value(x.Args[0])
			}
			return nil
		}
		if pkg, ok := sel.X.(*ast.Ident); ok {
			switch {
			case pkg.Name == "time" && sel.Sel.Name == "Duration" && len(x.Args) == 1:
				return value(x.Args[0])
			case pkg.Name == "ui" && sel.Sel.Name == "Hex" && len(x.Args) == 1:
				if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					return strings.ToLower(s)
				}
			case pkg.Name == "ui" && sel.Sel.Name == "RGBA" && len(x.Args) == 4:
				var p [4]float64
				for i, a := range x.Args {
					f, ok := value(a).(float64)
					if !ok {
						return nil
					}
					p[i] = f
				}
				return fmt.Sprintf("rgba(%g, %g, %g, %g)", p[0], p[1], p[2], p[3])
			}
		}
	case *ast.CompositeLit:
		// A struct literal with keys (a spring, a pose) becomes an object
		// with the keys in lower camel case; an array literal, a list.
		if len(x.Elts) == 0 {
			return nil
		}
		if _, keyed := x.Elts[0].(*ast.KeyValueExpr); keyed {
			m := map[string]any{}
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					return nil
				}
				id, ok := kv.Key.(*ast.Ident)
				if !ok {
					return nil
				}
				v := value(kv.Value)
				if v == nil {
					return nil
				}
				m[lowerFirst(id.Name)] = v
			}
			return m
		}
		var l []any
		for _, el := range x.Elts {
			v := value(el)
			if v == nil {
				return nil
			}
			l = append(l, v)
		}
		return l
	}
	return nil
}

var durations = map[string]any{"Nanosecond": 1e-6, "Microsecond": 1e-3, "Millisecond": 1.0, "Second": 1000.0, "Minute": 60000.0}

func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }

// mygoSprings reads MyGo's named springs from its ui package, so that the
// web's Smooth, Snappy and Bouncy are the desktop's.
func mygoSprings() map[string]any {
	dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/egoist/mygo").Output()
	if err != nil {
		log.Fatalf("gen: cannot find MyGo: %v", err)
	}
	path := filepath.Join(strings.TrimSpace(string(dir)), "ui", "motion.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		log.Fatal(err)
	}
	base, _ := value(topExpr(f, "defaultSpringDuration")).(float64)
	springs := map[string]any{}
	for _, name := range []string{"Smooth", "Snappy", "Bouncy"} {
		lit, ok := topExpr(f, name).(*ast.CompositeLit)
		if !ok {
			log.Fatalf("gen: MyGo has no ui.%s", name)
		}
		sp := map[string]any{"duration": base, "bounce": 0.0}
		ast.Inspect(lit, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			switch kv.Key.(*ast.Ident).Name {
			case "Bounce":
				if v, ok := value(kv.Value).(float64); ok {
					sp["bounce"] = v
				}
			case "Duration":
				if v, ok := value(kv.Value).(float64); ok {
					sp["duration"] = v
				}
			}
			return true
		})
		springs[name] = sp
	}
	return springs
}

// css writes the colors as custom properties, light under :root and
// [data-theme="light"], dark under [data-theme="dark"], with the names the
// demos' stylesheet uses (ink2 → --ink-2).
func css(color map[string]any, theme map[string]any, extra map[string]any) []byte {
	var b bytes.Buffer
	b.WriteString("/* Generated by go run ./web/tokens/gen from cmd/deneb/theme.go and model.go. Do not edit by hand. */\n")
	block := func(sel string, p map[string]any) {
		fmt.Fprintf(&b, "%s {\n", sel)
		keys := make([]string, 0, len(p))
		for k := range p {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  --%s: %v;\n", cssName(k), p[k])
		}
		if sel != `[data-theme="dark"]` {
			for i, c := range color["owners"].([]any) {
				fmt.Fprintf(&b, "  --owner-%d: %v;\n", i, c)
			}
			if fs, ok := theme["fontSize"].(float64); ok {
				fmt.Fprintf(&b, "  --font-size: %gpx;\n", fs)
			}
			names := make([]string, 0, len(extra))
			for k := range extra {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				fmt.Fprintf(&b, "  --%s: %v;\n", cssName(k), extra[k])
			}
		}
		b.WriteString("}\n")
	}
	block(`:root,
[data-theme="light"]`, color["light"].(map[string]any))
	block(`[data-theme="dark"]`, color["dark"].(map[string]any))
	return b.Bytes()
}

// cssName turns ink2 into ink-2 and ballHi into ball-hi.
func cssName(k string) string {
	var b strings.Builder
	for i, r := range k {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteByte('-')
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9' && i > 0 && !(k[i-1] >= '0' && k[i-1] <= '9'):
			b.WriteByte('-')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
