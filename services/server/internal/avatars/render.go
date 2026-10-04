// Package avatars implements the versioned, deterministic abstract avatar
// renderers. It has no database, network or operating-system randomness.
package avatars

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Node struct{ Color, Category string }
type Edge [2]int
type Options struct {
	PX          int
	Intensity   float64
	UIDOverride string
}

const DefaultGeneration = 1
const CanonicalPX = 240
const FingerprintUID = "fp"

func normalized(o Options) Options {
	if o.PX == 0 {
		o.PX = 80
	}
	return o
}
func digest(seed string) [32]byte {
	if seed == "" {
		seed = "?"
	}
	return sha256.Sum256([]byte(seed))
}
func prng(seed string) func() float64 {
	hash := digest(seed)
	state := binary.BigEndian.Uint64(hash[:8])
	if state == 0 {
		state = 1
	}
	return func() float64 {
		state ^= state >> 12
		state ^= state << 25
		state ^= state >> 27
		return float64(state*0x2545F4914F6CDD1D) / 18446744073709551616.0
	}
}
func esc(raw string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(raw)
}
func color(nodes []Node, i int) string {
	c := nodes[i].Color
	if c == "" {
		c = "#8c8c8c"
	}
	return esc(c)
}
func start(px int) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-hidden="true">`, px, px, px, px)
}
func pyFloat(value float64) string {
	s := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
func DataURI(svg string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}
func SignatureSeed(base string, generation, salt int) string {
	if generation == 1 && salt == 0 {
		return base
	}
	return fmt.Sprintf("%s|g%d|s%d", base, generation, salt)
}
func GenerationName(g int) string {
	if g == 2 {
		return "Orbits"
	}
	return "Constellation"
}

func IdenticonSVG(seed string, px int) string {
	if px == 0 {
		px = 80
	}
	hash := digest(seed)
	hue := (int(hash[0])<<8 | int(hash[1])) % 360
	cell := float64(px) / 5
	var b strings.Builder
	b.WriteString(start(px))
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#eef0ee"/><g fill="hsl(%d, 52%%, 47%%)">`, px, px, hue)
	for col := 0; col < 3; col++ {
		for row := 0; row < 5; row++ {
			if hash[col*5+row]&1 != 0 {
				for _, c := range []int{col, 4 - col} {
					fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s"/>`, pyFloat(math.RoundToEven(float64(c)*cell*100)/100), pyFloat(math.RoundToEven(float64(row)*cell*100)/100), pyFloat(cell), pyFloat(cell))
				}
			}
		}
	}
	b.WriteString("</g></svg>")
	return b.String()
}

type point struct{ x, y float64 }

func layout(rnd func() float64, n int, size float64) []point {
	if n == 0 {
		return nil
	}
	if n == 1 {
		return []point{{size / 2, size / 2}}
	}
	radius := size * .30
	if n > 6 {
		radius = size * .34
	}
	base := rnd() * 2 * math.Pi
	pts := make([]point, n)
	for i := range pts {
		angle := base + 2*math.Pi*float64(i)/float64(n) + (rnd()-.5)*(2*math.Pi/float64(n))*.35
		r := radius * (.82 + .30*rnd())
		pts[i] = point{size/2 + r*math.Cos(angle), size/2 + r*math.Sin(angle)}
	}
	return pts
}

func flourish(b *strings.Builder, seed, uid, blur string, size, intensity float64) {
	if intensity <= 0 {
		return
	}
	amt := max(0, min(1, intensity))
	glow := prng(seed + "|glow")
	radius := size * (.30 + .16*amt)
	fmt.Fprintf(b, `<radialGradient id="%s_aura" cx="50%%" cy="50%%" r="50%%"><stop offset="0%%" stop-color="#ffffff" stop-opacity="0"/><stop offset="72%%" stop-color="#cfe0ff" stop-opacity="0"/><stop offset="100%%" stop-color="#cfe0ff" stop-opacity="%.3f"/></radialGradient>`, uid, .10+.30*amt)
	fmt.Fprintf(b, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="url(#%s_aura)" filter="url(#%s)"/>`, size/2, size/2, radius, uid, blur)
	count := int(math.RoundToEven(amt * 10))
	if count > 0 {
		b.WriteString("<g>")
		for i := 0; i < count; i++ {
			x, y := glow()*size, glow()*size
			r := max((.5+.9*glow())*(size/240)*1.8, .5)
			opacity := .30 + .45*glow()
			fmt.Fprintf(b, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="#ffffff" opacity="%.2f"/>`, x, y, r, opacity)
		}
		b.WriteString("</g>")
	}
}

func ConstellationSVG(seed string, nodes []Node, edges []Edge, o Options) string {
	o = normalized(o)
	rnd := prng(seed)
	n := len(nodes)
	size := float64(o.PX)
	uid := o.UIDOverride
	if uid == "" {
		hash := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%d", seed, o.PX, n))
		uid = fmt.Sprintf("%x", hash[:4])
	}
	sky, starBlur, edgeBlur := uid+"_sky", uid+"_sblur", uid+"_eblur"
	var b strings.Builder
	b.WriteString(start(o.PX))
	b.WriteString("<defs>")
	fmt.Fprintf(&b, `<radialGradient id="%s" cx="50%%" cy="44%%" r="78%%"><stop offset="0%%" stop-color="#161d33"/><stop offset="100%%" stop-color="#04060d"/></radialGradient>`, sky)
	fmt.Fprintf(&b, `<filter id="%s" x="-70%%" y="-70%%" width="240%%" height="240%%"><feGaussianBlur stdDeviation="%.3f"/></filter>`, starBlur, size*.022)
	fmt.Fprintf(&b, `<filter id="%s" x="-50%%" y="-50%%" width="200%%" height="200%%"><feGaussianBlur stdDeviation="%.3f"/></filter>`, edgeBlur, size*.012)
	pts := layout(rnd, n, size)
	for i := range nodes {
		c := color(nodes, i)
		fmt.Fprintf(&b, `<radialGradient id="%s_h%d" cx="50%%" cy="50%%" r="50%%"><stop offset="0%%" stop-color="#ffffff" stop-opacity="1"/><stop offset="22%%" stop-color="%s" stop-opacity="0.95"/><stop offset="55%%" stop-color="%s" stop-opacity="0.55"/><stop offset="100%%" stop-color="%s" stop-opacity="0"/></radialGradient>`, uid, i, c, c, c)
	}
	seen := map[Edge]bool{}
	valid := []Edge{}
	for _, e := range edges {
		i, j := e[0], e[1]
		if i < 0 || j < 0 || i >= n || j >= n || i == j {
			continue
		}
		key := Edge{min(i, j), max(i, j)}
		if !seen[key] {
			seen[key] = true
			valid = append(valid, e)
		}
	}
	for k, e := range valid {
		a, c := pts[e[0]], pts[e[1]]
		fmt.Fprintf(&b, `<linearGradient id="%s_e%d" gradientUnits="userSpaceOnUse" x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f"><stop offset="0%%" stop-color="%s"/><stop offset="50%%" stop-color="#ffffff" stop-opacity="0.85"/><stop offset="100%%" stop-color="%s"/></linearGradient>`, uid, k, a.x, a.y, c.x, c.y, color(nodes, e[0]), color(nodes, e[1]))
	}
	b.WriteString("</defs>")
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="url(#%s)"/>`, o.PX, o.PX, sky)
	b.WriteString("<g>")
	dust := 7
	if o.PX >= 90 {
		dust = 14
	}
	for i := 0; i < dust; i++ {
		x, y := rnd()*size, rnd()*size
		r := max((.4+.7*rnd())*(size/240)*1.6, .4)
		opacity := .12 + .22*rnd()
		fmt.Fprintf(&b, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="#cfd8ff" opacity="%.2f"/>`, x, y, r, opacity)
	}
	b.WriteString("</g>")
	if len(valid) > 0 {
		for pass := 0; pass < 2; pass++ {
			if pass == 0 {
				fmt.Fprintf(&b, `<g filter="url(#%s)">`, edgeBlur)
			} else {
				b.WriteString("<g>")
			}
			width, opacity := max(size*.022, 2.4), "0.42"
			if pass == 1 {
				width, opacity = max(size*.008, 1.1), "0.55"
			}
			for k, e := range valid {
				a, c := pts[e[0]], pts[e[1]]
				fmt.Fprintf(&b, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="url(#%s_e%d)" stroke-width="%.2f" stroke-opacity="%s" stroke-linecap="round"/>`, a.x, a.y, c.x, c.y, uid, k, width, opacity)
			}
			b.WriteString("</g>")
		}
	}
	core, halo := max(size*.020, 1.5), max(size*.078, 6.5)
	for i, p := range pts {
		fmt.Fprintf(&b, `<g transform="translate(%.2f %.2f)"><circle r="%.2f" fill="url(#%s_h%d)" filter="url(#%s)"/><circle r="%.2f" fill="%s" opacity="0.95"/><circle r="%.2f" fill="#ffffff"/></g>`, p.x, p.y, halo, uid, i, starBlur, core*1.9, color(nodes, i), core)
	}
	flourish(&b, seed, uid, starBlur, size, o.Intensity)
	b.WriteString("</svg>")
	return b.String()
}

func OrbitsSVG(seed string, nodes []Node, o Options) string {
	o = normalized(o)
	rnd := prng(seed)
	hash := digest(seed)
	size := float64(o.PX)
	center := size / 2
	uid := o.UIDOverride
	if uid == "" {
		d := sha256.Sum256(fmt.Appendf(nil, "orb|%s|%d|%d", seed, o.PX, len(nodes)))
		uid = fmt.Sprintf("%x", d[:4])
	}
	sunHue := (int(hash[2])<<8 | int(hash[3])) % 360
	var b strings.Builder
	b.WriteString(start(o.PX))
	b.WriteString("<defs>")
	fmt.Fprintf(&b, `<radialGradient id="%s_bg" cx="50%%" cy="50%%" r="72%%"><stop offset="0%%" stop-color="#101528"/><stop offset="100%%" stop-color="#03040a"/></radialGradient><radialGradient id="%s_sun" cx="50%%" cy="50%%" r="50%%"><stop offset="0%%" stop-color="#ffffff"/><stop offset="38%%" stop-color="hsl(%d, 85%%, 72%%)"/><stop offset="100%%" stop-color="hsl(%d, 70%%, 52%%)" stop-opacity="0"/></radialGradient><filter id="%s_blur" x="-60%%" y="-60%%" width="220%%" height="220%%"><feGaussianBlur stdDeviation="%.3f"/></filter></defs><rect width="%d" height="%d" fill="url(#%s_bg)"/>`, uid, uid, sunHue, sunHue, uid, size*.018, o.PX, o.PX, uid)
	b.WriteString("<g>")
	for i := 0; i < 20; i++ {
		x, y := rnd()*size, rnd()*size
		r := max((.3+.6*rnd())*(size/240)*1.5, .3)
		opacity := .12 + .26*rnd()
		fmt.Fprintf(&b, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="#cdd9ff" opacity="%.2f"/>`, x, y, r, opacity)
	}
	b.WriteString("</g>")
	categories := []string{}
	groups := map[string][]int{}
	for i, node := range nodes {
		category := node.Category
		if category == "" {
			category = fmt.Sprintf("_solo%d", i)
		}
		if _, exists := groups[category]; !exists {
			categories = append(categories, category)
		}
		groups[category] = append(groups[category], i)
	}
	type ring struct{ rx, ry, rotation float64 }
	rings := []ring{}
	for k := range categories {
		rx := size * (.20 + .24*(float64(k)+.6*rnd())/float64(max(len(categories), 1)) + .06)
		ry := rx * (.34 + .18*rnd())
		rotation := rnd() * 180
		rings = append(rings, ring{rx, ry, rotation})
		fmt.Fprintf(&b, `<ellipse cx="%.2f" cy="%.2f" rx="%.2f" ry="%.2f" transform="rotate(%.1f %.2f %.2f)" fill="none" stroke="#aebde0" stroke-width="%.2f" opacity="0.30"/>`, center, center, rx, ry, rotation, center, center, max(size*.004, .5))
	}
	if len(categories) == 0 {
		fmt.Fprintf(&b, `<ellipse cx="%.2f" cy="%.2f" rx="%.2f" ry="%.2f" transform="rotate(%.1f %.2f %.2f)" fill="none" stroke="#aebde0" stroke-width="%.2f" stroke-dasharray="%.2f %.2f" opacity="0.35"/>`, center, center, size*.30, size*.12, rnd()*180, center, center, max(size*.004, .5), size*.012, size*.02)
	}
	sun := size * .085
	fmt.Fprintf(&b, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="url(#%s_sun)" filter="url(#%s_blur)"/><circle cx="%.2f" cy="%.2f" r="%.2f" fill="hsl(%d, 80%%, 68%%)"/><circle cx="%.2f" cy="%.2f" r="%.2f" fill="#ffffff"/>`, center, center, sun*2.6, uid, uid, center, center, sun, sunHue, center, center, sun*.55)
	planet := max(size*.028, 1.6)
	for k, category := range categories {
		r := rings[k]
		rotation := r.rotation * math.Pi / 180
		for _, i := range groups[category] {
			theta := rnd() * 2 * math.Pi
			ex, ey := r.rx*math.Cos(theta), r.ry*math.Sin(theta)
			x := center + ex*math.Cos(rotation) - ey*math.Sin(rotation)
			y := center + ex*math.Sin(rotation) + ey*math.Cos(rotation)
			c := color(nodes, i)
			fmt.Fprintf(&b, `<g transform="translate(%.2f %.2f)"><circle r="%.2f" fill="%s" opacity="0.30" filter="url(#%s_blur)"/><circle r="%.2f" fill="%s"/><circle cx="%.2f" cy="%.2f" r="%.2f" fill="#ffffff" opacity="0.85"/></g>`, x, y, planet*2.1, c, uid, planet, c, -planet*.3, -planet*.3, planet*.32)
		}
	}
	flourish(&b, seed, uid, uid+"_blur", size, o.Intensity)
	b.WriteString("</svg>")
	return b.String()
}

func RenderGeneration(generation int, seed string, nodes []Node, edges []Edge, o Options) string {
	if generation != 2 {
		generation = 1
	}
	if generation == 1 && len(nodes) == 0 {
		return IdenticonSVG(seed, normalized(o).PX)
	}
	if generation == 2 {
		return OrbitsSVG(seed, nodes, o)
	}
	return ConstellationSVG(seed, nodes, edges, o)
}

// ActivityAccentSVG is decorative generative art, with no seed text emitted.
func ActivityAccentSVG(seed string) string { return ActivityAccentWithSize(seed, 320, 120) }
func ActivityAccentWithSize(seed string, width, height int) string {
	rnd := prng(seed)
	hash := digest(seed)
	uid := fmt.Sprintf("ac%x", hash[:5])
	hue := (int(hash[0])<<8 | int(hash[1])) % 360
	hue2 := (hue + 32) % 360
	deep := fmt.Sprintf("hsl(%d, 44%%, 40%%)", hue)
	bright := fmt.Sprintf("hsl(%d, 46%%, 54%%)", hue2)
	tint := fmt.Sprintf("hsl(%d, 38%%, 90%%)", hue)
	var shapes strings.Builder
	for i := 0; i < 4; i++ {
		x := math.RoundToEven(rnd()*float64(width)*10) / 10
		y := math.RoundToEven(rnd()*float64(height)*10) / 10
		r := math.RoundToEven((20+rnd()*48)*10) / 10
		opacity := math.RoundToEven((.08+rnd()*.20)*100) / 100
		fmt.Fprintf(&shapes, `<circle cx="%s" cy="%s" r="%s" fill="%s" opacity="%s"/>`, pyFloat(x), pyFloat(y), pyFloat(r), tint, pyFloat(opacity))
	}
	for i := 0; i < 2; i++ {
		y1 := math.RoundToEven(rnd()*float64(height)*10) / 10
		y2 := math.RoundToEven(rnd()*float64(height)*10) / 10
		fmt.Fprintf(&shapes, `<line x1="0" y1="%s" x2="%d" y2="%s" stroke="%s" stroke-width="1.5" opacity="0.16"/>`, pyFloat(y1), width, pyFloat(y2), tint)
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="100%%" height="100%%" preserveAspectRatio="xMidYMid slice" role="img" aria-hidden="true" focusable="false"><defs><linearGradient id="%s" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient></defs><rect width="%d" height="%d" fill="url(#%s)"/>%s</svg>`, width, height, uid, deep, bright, width, height, uid, shapes.String())
}
