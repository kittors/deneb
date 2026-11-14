//go:build ignore

// Regenerate the editable SVG originals and the PNGs shipped by the client.
// Sharp is a development-only renderer; the application reads embedded PNGs.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var palettes = []struct{ name, light, mid, dark, ground string }{
	{"海蓝", "#bce5ff", "#498fe9", "#2554a3", "#edf3fc"},
	{"青碧", "#bcf3e9", "#3eb8ac", "#197779", "#edf7f4"},
	{"森林", "#d8edb7", "#7aaf75", "#3f7659", "#f0f5e9"},
	{"落日", "#ffe0b9", "#ef9b69", "#b85c4e", "#fff3e9"},
	{"玫瑰", "#ffd8e3", "#dc8fab", "#a05278", "#fcf0f4"},
	{"鸢尾", "#e1dcff", "#a399dc", "#6a62a3", "#f3f1fc"},
	{"石墨", "#d9e5f0", "#869db4", "#465c77", "#eff2f5"},
	{"暖沙", "#f4e7c8", "#c5ac83", "#8d755a", "#f7f3e9"},
}
var names = []string{"星芒", "环轨", "山峦", "海浪", "新月", "日光", "叶芽", "云朵", "方块", "花瓣", "小猫", "机器人"}

// Each object has a hand-drawn silhouette, volume, edge light and restrained
// material detail. Paths stay readable at the actual 56px thumbnail size.
var objects = []string{
	// Star: four curved points, a beveled rim and a luminous glass core.
	`<path d="M256 91C273 190 298 224 402 254C297 273 270 307 255 414C237 308 213 278 108 257C213 231 239 198 256 91Z" fill="url(#accent)" stroke="@D@" stroke-opacity=".16" stroke-width="2"/>
<path d="M256 110C267 198 294 232 380 254L257 258Z" fill="#fff" opacity=".56"/>
<path d="M257 258L255 391C240 304 215 277 133 258Z" fill="@D@" opacity=".2"/>
<path d="M256 152C266 214 288 240 347 254C289 269 269 288 257 351C244 287 224 268 161 257C224 239 244 215 256 152Z" fill="url(#glass)"/>
<path d="M253 143C242 208 227 229 181 247" fill="none" stroke="#fff" stroke-opacity=".85" stroke-width="5" stroke-linecap="round"/>
<circle cx="365" cy="137" r="14" fill="url(#pearl)"/><circle cx="137" cy="358" r="7" fill="@M@" opacity=".45"/>`,
	// Saturn: behind/front orbital arcs, pearl sphere and fine metallic rim.
	`<ellipse cx="256" cy="260" rx="169" ry="55" transform="rotate(-24 256 260)" fill="none" stroke="@D@" stroke-opacity=".15" stroke-width="25"/>
<ellipse cx="256" cy="254" rx="169" ry="55" transform="rotate(-24 256 254)" fill="none" stroke="url(#metal)" stroke-width="17"/>
<circle cx="256" cy="246" r="100" fill="url(#accent)" stroke="@D@" stroke-opacity=".12" stroke-width="2"/>
<path d="M171 211C204 242 262 255 346 239M166 242C210 279 285 291 349 270" fill="none" stroke="#fff" stroke-opacity=".19" stroke-width="13"/>
<ellipse cx="221" cy="184" rx="34" ry="16" transform="rotate(-34 221 184)" fill="#fff" opacity=".48"/>
<path d="M105 294C101 330 188 330 282 292C360 260 409 224 407 207" fill="none" stroke="url(#metal)" stroke-width="19" stroke-linecap="round"/>
<path d="M109 296C126 320 192 316 282 280C347 255 386 226 402 207" fill="none" stroke="#fff" stroke-width="3" stroke-opacity=".85"/>
<circle cx="386" cy="126" r="7" fill="@M@" opacity=".4"/>`,
	// Mountains: overlapping sculpted porcelain facets and a metallic sun.
	`<path d="M99 348L205 148Q214 131 224 149L324 348Z" fill="url(#muted)" stroke="@D@" stroke-opacity=".1" stroke-width="2"/>
<path d="M214 146L219 348H99Z" fill="#fff" opacity=".23"/>
<path d="M167 223L207 148Q214 134 224 149L258 217L226 204L214 220L198 207Z" fill="url(#pearl)"/>
<path d="M180 355L294 169Q302 155 312 171L421 355Q427 368 412 371L189 371Q172 369 180 355Z" fill="url(#accent)"/>
<path d="M303 168L293 366H190Z" fill="#fff" opacity=".23"/>
<path d="M264 222L294 170Q302 158 311 172L347 233L322 221L306 241L289 217Z" fill="url(#pearl)"/>
<path d="M199 366H411" stroke="#fff" stroke-opacity=".64" stroke-width="4" stroke-linecap="round"/>
<circle cx="369" cy="144" r="28" fill="url(#gold)"/>`,
	// Wave: flowing glass ribbon with an open curl rather than a line glyph.
	`<path d="M101 337C151 355 198 345 224 319C251 293 248 262 245 233C240 179 268 128 323 129C370 130 396 171 380 211C367 242 337 250 318 236C347 237 355 218 347 202C336 181 306 192 305 215C303 248 328 272 354 294C381 317 395 338 406 366C282 408 164 393 101 337Z" fill="url(#accent)"/>
<path d="M107 341C174 369 237 333 254 289C272 241 250 191 282 158C302 136 328 136 346 145C312 140 288 172 289 211C289 265 331 294 370 328C309 311 288 286 276 264C278 320 224 380 161 367Z" fill="url(#glass)"/>
<path d="M105 339C160 362 208 345 237 315C267 283 252 238 258 207C266 160 296 137 325 138" fill="none" stroke="#fff" stroke-opacity=".86" stroke-width="7" stroke-linecap="round"/>
<path d="M161 364C236 401 321 380 372 359" fill="none" stroke="@D@" stroke-opacity=".25" stroke-width="12" stroke-linecap="round"/>
<circle cx="191" cy="183" r="13" fill="url(#glass)"/><circle cx="149" cy="226" r="7" fill="@M@" opacity=".45"/>`,
	// Crescent: curved inner rim, satin pearl body, a small companion star.
	`<path d="M324 112C246 170 245 280 323 345C341 360 362 371 383 376C264 425 143 349 140 239C138 170 186 112 256 96C279 91 304 98 324 112Z" fill="url(#accent)" stroke="@D@" stroke-opacity=".12" stroke-width="2"/>
<path d="M304 111C238 174 242 281 312 344C327 358 342 369 360 376C269 393 172 329 166 245C159 179 202 121 263 107Z" fill="url(#pearl)"/>
<path d="M243 116C200 138 174 178 173 222" fill="none" stroke="#fff" stroke-width="7" stroke-opacity=".9" stroke-linecap="round"/>
<path d="M357 156Q361 184 388 188Q361 193 357 220Q352 193 326 188Q352 183 357 156Z" fill="url(#gold)"/>
<circle cx="396" cy="285" r="7" fill="@M@" opacity=".4"/>`,
	// Sun: amber body, carved broad rays, warm porcelain center.
	`<g fill="url(#gold)" stroke="#c68f4e" stroke-opacity=".12" stroke-width="2">
<rect x="244" y="95" width="24" height="47" rx="12"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(45 256 256)"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(90 256 256)"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(135 256 256)"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(180 256 256)"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(225 256 256)"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(270 256 256)"/><rect x="244" y="95" width="24" height="47" rx="12" transform="rotate(315 256 256)"/></g>
<circle cx="256" cy="256" r="96" fill="url(#gold)" stroke="#b58047" stroke-opacity=".2" stroke-width="2"/>
<circle cx="251" cy="249" r="77" fill="url(#sun)"/>
<path d="M190 233C195 203 218 182 244 179" stroke="#fff" stroke-opacity=".72" stroke-width="7" fill="none" stroke-linecap="round"/>
<path d="M282 320C301 311 317 292 322 274" stroke="#be7b38" stroke-opacity=".22" stroke-width="9" fill="none" stroke-linecap="round"/>`,
	// Leaf: two asymmetrical ginkgo fans with fine etched veins.
	`<path d="M242 382C261 301 250 250 219 204" fill="none" stroke="@D@" stroke-width="11" stroke-linecap="round"/>
<path d="M253 324C284 281 302 259 330 239" fill="none" stroke="@D@" stroke-width="8" stroke-linecap="round"/>
<path d="M231 282C160 278 115 221 132 156C136 142 147 135 159 142L172 152L178 131C182 114 196 109 208 119L221 131L230 113C237 102 251 107 254 122C278 189 275 240 231 282Z" fill="url(#accent)"/>
<path d="M234 268C222 224 198 182 169 154M234 268C238 222 234 167 229 132M232 265C210 234 163 214 144 187" stroke="#fff" stroke-opacity=".43" stroke-width="3" fill="none" stroke-linecap="round"/>
<path d="M264 305C265 245 305 194 363 192Q380 192 376 208L373 220L390 222Q403 225 394 239L387 249L402 254Q416 261 404 274C359 321 310 335 264 305Z" fill="url(#muted)"/>
<path d="M279 301L370 225M279 301C316 292 365 288 390 265M279 301C300 272 306 230 324 208" stroke="#fff" stroke-opacity=".45" stroke-width="3" fill="none" stroke-linecap="round"/>`,
	// Cloud: continuous soft silhouette with volumetric lobe shading.
	`<path d="M143 350C92 350 77 299 101 268C113 251 133 244 152 247C151 199 185 166 226 172C254 129 322 137 344 183C385 181 412 212 407 251C450 273 440 338 398 348C329 363 212 360 143 350Z" fill="url(#cloud)" stroke="@D@" stroke-opacity=".08" stroke-width="2"/>
<path d="M154 262C156 215 184 189 223 191C244 156 297 157 321 183" fill="none" stroke="#fff" stroke-width="10" stroke-linecap="round" opacity=".9"/>
<path d="M114 312C165 347 333 349 407 319" fill="none" stroke="@M@" stroke-opacity=".17" stroke-width="14" stroke-linecap="round"/>
<path d="M227 192C239 205 245 224 242 244M345 203C333 209 326 224 325 238" fill="none" stroke="@M@" stroke-opacity=".13" stroke-width="5" stroke-linecap="round"/>`,
	// Cube: softened edges, luminous top face and optically layered sides.
	`<path d="M133 172L242 111Q256 103 270 111L378 172Q391 180 391 195V318Q391 333 378 341L270 403Q256 410 242 403L133 341Q120 333 120 318V195Q120 180 133 172Z" fill="url(#accent)" stroke="@D@" stroke-opacity=".22" stroke-width="2"/>
<path d="M130 181L246 117Q256 112 266 118L382 183L256 256Z" fill="url(#glass)"/>
<path d="M129 198L247 267V393L138 331Q129 327 129 316Z" fill="#fff" opacity=".32"/>
<path d="M264 269L382 200V314Q382 325 371 331L264 394Z" fill="@D@" opacity=".15"/>
<path d="M137 183L256 250L374 184M256 250V390" fill="none" stroke="#fff" stroke-width="5" stroke-opacity=".75" stroke-linejoin="round"/>
<path d="M149 203L211 239V303L149 267Z" fill="#fff" opacity=".2"/>`,
	// Blossom: five distinct folded glass petals, rather than overlapping circles.
	`<g fill="url(#accent)" stroke="@D@" stroke-opacity=".14" stroke-width="2">
<path id="petal" d="M256 269C222 241 193 189 212 146C226 111 272 107 292 141C319 185 285 239 256 269Z"/>
<use href="#petal" transform="rotate(72 256 256)"/><use href="#petal" transform="rotate(144 256 256)"/><use href="#petal" transform="rotate(216 256 256)"/><use href="#petal" transform="rotate(288 256 256)"/></g>
<g fill="none" stroke="#fff" stroke-opacity=".45" stroke-width="5" stroke-linecap="round"><path id="petalLight" d="M240 220C225 186 226 154 244 138"/><use href="#petalLight" transform="rotate(72 256 256)"/><use href="#petalLight" transform="rotate(144 256 256)"/><use href="#petalLight" transform="rotate(216 256 256)"/><use href="#petalLight" transform="rotate(288 256 256)"/></g>
<circle cx="256" cy="256" r="39" fill="@D@" opacity=".15"/><circle cx="252" cy="250" r="32" fill="url(#pearl)"/><circle cx="244" cy="239" r="10" fill="#fff" opacity=".64"/>`,
	// Cat: continuous ceramic silhouette, shaped ears, muzzle and inset eyes.
	`<path d="M144 234L135 128Q136 105 156 119L211 157Q254 144 299 157L354 118Q374 106 376 129L368 233C411 311 354 388 257 389C157 390 101 311 144 234Z" fill="url(#pearl)" stroke="@D@" stroke-opacity=".15" stroke-width="2"/>
<path d="M153 142L160 211L198 175Z" fill="url(#ear)"/><path d="M357 141L349 212L312 175Z" fill="url(#ear)"/>
<path d="M160 248C174 213 203 194 229 191" fill="none" stroke="#fff" stroke-width="9" stroke-linecap="round" opacity=".85"/>
<ellipse cx="206" cy="269" rx="13" ry="18" fill="#35485f"/><ellipse cx="305" cy="269" rx="13" ry="18" fill="#35485f"/>
<ellipse cx="202" cy="263" rx="4" ry="5" fill="#fff" opacity=".95"/><ellipse cx="301" cy="263" rx="4" ry="5" fill="#fff" opacity=".95"/>
<ellipse cx="234" cy="318" rx="27" ry="23" fill="#fff" opacity=".54"/><ellipse cx="277" cy="318" rx="27" ry="23" fill="#fff" opacity=".54"/>
<path d="M246 305Q256 299 267 305Q267 309 256 318Q245 309 246 305Z" fill="#ba8890"/><path d="M256 319V327M256 327Q245 337 235 329M256 327Q267 338 279 329" fill="none" stroke="#9e8a8a" stroke-width="3" stroke-linecap="round"/>
<ellipse cx="176" cy="304" rx="18" ry="8" fill="@M@" opacity=".17"/><ellipse cx="336" cy="304" rx="18" ry="8" fill="@M@" opacity=".17"/>`,
	// Robot: rounded ceramic chassis, inset glass face, metal antenna and ears.
	`<rect x="98" y="223" width="34" height="83" rx="15" fill="url(#metal)"/><rect x="380" y="223" width="34" height="83" rx="15" fill="url(#metal)"/>
<path d="M256 173V127" fill="none" stroke="url(#metal)" stroke-width="14" stroke-linecap="round"/><circle cx="256" cy="115" r="18" fill="url(#accent)"/><ellipse cx="250" cy="109" rx="7" ry="5" fill="#fff" opacity=".65"/>
<rect x="125" y="170" width="262" height="208" rx="61" fill="url(#pearl)" stroke="@D@" stroke-opacity=".16" stroke-width="2"/>
<rect x="151" y="202" width="210" height="130" rx="38" fill="url(#screen)"/>
<path d="M174 222Q183 211 202 212H311" fill="none" stroke="#fff" stroke-opacity=".23" stroke-width="4" stroke-linecap="round"/>
<rect x="190" y="244" width="24" height="42" rx="12" fill="url(#eye)"/><rect x="297" y="244" width="24" height="42" rx="12" fill="url(#eye)"/>
<path d="M235 303Q256 314 277 303" fill="none" stroke="@L@" stroke-opacity=".7" stroke-width="5" stroke-linecap="round"/>
<circle cx="240" cy="354" r="4" fill="@M@"/><circle cx="255" cy="354" r="4" fill="@M@" opacity=".5"/><circle cx="270" cy="354" r="4" fill="@M@" opacity=".25"/>`,
}

func artwork(pattern, palette int) string {
	p := palettes[palette]
	svg := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="512" height="512" viewBox="0 0 512 512">
<defs>
 <radialGradient id="ground" cx="28%" cy="16%" r="100%"><stop stop-color="#fff"/><stop offset="1" stop-color="@G@"/></radialGradient>
 <linearGradient id="accent" x1="10%" y1="0" x2="85%" y2="100%"><stop stop-color="@L@"/><stop offset=".48" stop-color="@M@"/><stop offset="1" stop-color="@D@"/></linearGradient>
 <linearGradient id="muted" x1="0" y1="0" x2="100%" y2="100%"><stop stop-color="@L@"/><stop offset="1" stop-color="@D@"/></linearGradient>
 <linearGradient id="glass" x1="0" y1="0" x2="90%" y2="100%"><stop stop-color="#fff" stop-opacity=".95"/><stop offset=".35" stop-color="@L@" stop-opacity=".8"/><stop offset=".7" stop-color="@M@" stop-opacity=".45"/><stop offset="1" stop-color="#fff" stop-opacity=".7"/></linearGradient>
 <radialGradient id="pearl" cx="30%" cy="15%" r="95%"><stop stop-color="#fffefc"/><stop offset=".55" stop-color="#f1f1ed"/><stop offset=".82" stop-color="#c9d2d6"/><stop offset="1" stop-color="@M@"/></radialGradient>
 <linearGradient id="metal" x1="0" y1="0" x2="60%" y2="100%"><stop stop-color="#fbf9ed"/><stop offset=".3" stop-color="#ccd4d8"/><stop offset=".5" stop-color="#fffdf5"/><stop offset="1" stop-color="#98a8b5"/></linearGradient>
 <radialGradient id="gold" cx="25%" cy="20%" r="100%"><stop stop-color="#fff2c0"/><stop offset=".45" stop-color="#ecc57e"/><stop offset="1" stop-color="#b97846"/></radialGradient>
 <radialGradient id="sun" cx="35%" cy="23%" r="90%"><stop stop-color="#fff5ca"/><stop offset=".55" stop-color="#f3c475"/><stop offset="1" stop-color="#da944b"/></radialGradient>
 <radialGradient id="cloud" cx="30%" cy="10%" r="100%"><stop stop-color="#fff"/><stop offset=".5" stop-color="#fdfcf8"/><stop offset=".8" stop-color="#d4e1eb"/><stop offset="1" stop-color="@M@"/></radialGradient>
 <linearGradient id="ear" x1="0" y1="0" x2="80%" y2="100%"><stop stop-color="#f4d9bf"/><stop offset="1" stop-color="#d3a896"/></linearGradient>
 <linearGradient id="screen" x1="0" y1="0" x2="70%" y2="100%"><stop stop-color="#50687c"/><stop offset=".4" stop-color="#263e54"/><stop offset="1" stop-color="#182b40"/></linearGradient>
 <linearGradient id="eye" x1="0" y1="0" x2="0" y2="100%"><stop stop-color="#f2ffff"/><stop offset="1" stop-color="@M@"/></linearGradient>
 <radialGradient id="shadow"><stop stop-color="@D@" stop-opacity=".19"/><stop offset="1" stop-color="@D@" stop-opacity="0"/></radialGradient>
</defs>
<rect width="512" height="512" fill="url(#ground)"/>
<ellipse cx="260" cy="416" rx="153" ry="31" fill="url(#shadow)"/>
` + objects[pattern] + `</svg>`
	// SVG 1.1 rendering compatibility for the generator's use instances.
	svg = strings.ReplaceAll(svg, "href=", "xlink:href=")
	return strings.NewReplacer("@L@", p.light, "@M@", p.mid, "@D@", p.dark, "@G@", p.ground).Replace(svg)
}
func main() {
	out := flag.String("out", "cmd/deneb/avatar-presets", "PNG output directory")
	source := flag.String("source", "", "editable SVG directory (defaults beside PNG directory)")
	flag.Parse()
	if *source == "" {
		*source = filepath.Join(filepath.Dir(*out), "avatar-art")
	}
	for _, dir := range []string{*out, *source} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			panic(err)
		}
	}
	for p := range palettes {
		for i := range names {
			svg := filepath.Join(*source, fmt.Sprintf("%d-%d.svg", p, i))
			png := filepath.Join(*out, fmt.Sprintf("%d-%d.png", p, i))
			if err := os.WriteFile(svg, []byte(artwork(i, p)), 0644); err != nil {
				panic(err)
			}
			cmd := exec.Command("node", "-e", `const sharp = require(process.env.SHARP_MODULE || "sharp"); sharp(process.argv[1]).resize(128,128).png().toFile(process.argv[2]).catch(e=>{console.error(e.message);process.exitCode=1})`, svg, png)
			if output, err := cmd.CombinedOutput(); err != nil {
				panic(fmt.Sprintf("render %s: %v: %s", svg, err, output))
			}
		}
	}
}
