// Command warehousegen renders a synthetic but reasonably realistic warehouse
// scene as a sequence of JPEG frames: pallet racking, cardboard boxes, moving
// people in hi-vis, and a forklift driving down the aisle. It uses only the Go
// standard library (no third-party image deps) so it builds inside the main
// NEUROFORGE X module. Encode the frames to MP4 with ffmpeg afterwards.
//
//	go run ./tools/warehousegen -out <dir> -frames 72 -w 1024 -h 576
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
)

var W, H int

func main() {
	out := flag.String("out", "frames", "output directory for frames")
	frames := flag.Int("frames", 360, "number of frames to render")
	fps := flag.Int("fps", 24, "frames per second (sets time-based motion)")
	w := flag.Int("w", 1024, "frame width")
	h := flag.Int("h", 576, "frame height")
	flag.Parse()
	W, H = *w, *h

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	for i := 0; i < *frames; i++ {
		ts := float64(i) / float64(*fps) // seconds
		img := renderFrame(ts, i)
		path := filepath.Join(*out, fmt.Sprintf("frame_%03d.jpg", i+1))
		file, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			os.Exit(1)
		}
		if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		file.Close()
	}
	fmt.Printf("rendered %d frames (%dx%d) to %s\n", *frames, W, H, *out)
}

// ---------- drawing primitives (alpha-blended, float colors 0-255) ----------

type canvas struct{ img *image.RGBA }

func newCanvas() *canvas { return &canvas{image.NewRGBA(image.Rect(0, 0, W, H))} }

func (c *canvas) blend(x, y int, r, g, b, a float64) {
	if x < 0 || y < 0 || x >= W || y >= H || a <= 0 {
		return
	}
	if a > 1 {
		a = 1
	}
	i := c.img.PixOffset(x, y)
	p := c.img.Pix
	p[i] = uint8(float64(p[i])*(1-a) + r*a)
	p[i+1] = uint8(float64(p[i+1])*(1-a) + g*a)
	p[i+2] = uint8(float64(p[i+2])*(1-a) + b*a)
	p[i+3] = 255
}

func (c *canvas) rect(x0, y0, x1, y1 float64, col [3]float64, a float64) {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			c.blend(x, y, col[0], col[1], col[2], a)
		}
	}
}

// vRect fills a rectangle with a vertical gradient from top to bottom color.
func (c *canvas) vRect(x0, y0, x1, y1 float64, top, bot [3]float64, a float64) {
	h := y1 - y0
	if h <= 0 {
		return
	}
	for y := int(y0); y < int(y1); y++ {
		t := (float64(y) - y0) / h
		r := top[0] + (bot[0]-top[0])*t
		g := top[1] + (bot[1]-top[1])*t
		b := top[2] + (bot[2]-top[2])*t
		for x := int(x0); x < int(x1); x++ {
			c.blend(x, y, r, g, b, a)
		}
	}
}

func (c *canvas) ellipse(cx, cy, rx, ry float64, col [3]float64, a float64) {
	for y := int(cy - ry); y <= int(cy+ry); y++ {
		for x := int(cx - rx); x <= int(cx+rx); x++ {
			dx := (float64(x) - cx) / rx
			dy := (float64(y) - cy) / ry
			d := dx*dx + dy*dy
			if d <= 1 {
				edge := 1.0
				if d > 0.82 {
					edge = (1 - d) / 0.18 // soft rim
				}
				c.blend(x, y, col[0], col[1], col[2], a*edge)
			}
		}
	}
}

// quad fills an axis-free quadrilateral (p0..p3 clockwise) via scanline.
func (c *canvas) quad(p [4][2]float64, col [3]float64, a float64) {
	minY, maxY := p[0][1], p[0][1]
	for _, q := range p {
		minY = math.Min(minY, q[1])
		maxY = math.Max(maxY, q[1])
	}
	for y := int(minY); y <= int(maxY); y++ {
		var xs []float64
		for i := 0; i < 4; i++ {
			a0, b0 := p[i], p[(i+1)%4]
			if (a0[1] <= float64(y) && b0[1] > float64(y)) || (b0[1] <= float64(y) && a0[1] > float64(y)) {
				t := (float64(y) - a0[1]) / (b0[1] - a0[1])
				xs = append(xs, a0[0]+t*(b0[0]-a0[0]))
			}
		}
		if len(xs) < 2 {
			continue
		}
		lo, hi := xs[0], xs[1]
		if lo > hi {
			lo, hi = hi, lo
		}
		for x := int(lo); x <= int(hi); x++ {
			c.blend(x, y, col[0], col[1], col[2], a)
		}
	}
}

// ---------- scene ----------

var (
	concreteTop = [3]float64{150, 152, 156}
	concreteBot = [3]float64{92, 94, 100}
	ceilTop     = [3]float64{70, 76, 90}
	ceilBot     = [3]float64{150, 158, 172}
	rackSteel   = [3]float64{70, 74, 82}
	rackBeam    = [3]float64{214, 120, 36} // warehouse-orange beams
	boxLight    = [3]float64{198, 158, 108}
	boxMid      = [3]float64{170, 130, 84}
	boxDark     = [3]float64{132, 98, 60}
	hiVis       = [3]float64{240, 190, 40}
	hiVis2      = [3]float64{236, 120, 36}
	skin        = [3]float64{214, 170, 140}
	forkYellow  = [3]float64{236, 196, 36}
	forkDark    = [3]float64{40, 40, 44}
)

const horizonY = 0.40

func renderFrame(ts float64, frame int) *image.RGBA {
	c := newCanvas()
	hz := float64(H) * horizonY

	// ceiling (with faint light strips) and back wall glow
	c.vRect(0, 0, float64(W), hz, ceilTop, ceilBot, 1)
	for i := 1; i <= 5; i++ {
		x := float64(W) * float64(i) / 6
		c.rect(x-2, 0, x+2, hz*0.9, [3]float64{210, 220, 235}, 0.5)
	}
	// floor
	c.vRect(0, hz, float64(W), float64(H), concreteTop, concreteBot, 1)

	// aisle perspective guide + light pools
	cx := float64(W) / 2
	for _, u := range []float64{-1, 1} {
		p := [4][2]float64{
			{cx + u*float64(W)*0.02, hz},
			{cx + u*float64(W)*0.33, float64(H)},
			{cx + u*float64(W)*0.30, float64(H)},
			{cx + u*float64(W)*0.015, hz},
		}
		c.quad(p, [3]float64{230, 228, 60}, 0.10)
	}
	for i := 0; i < 4; i++ {
		t := float64(i) / 3
		y := hz + (float64(H)-hz)*(t*t)
		rx := 40 + 150*t
		c.ellipse(cx, y, rx, rx*0.22, [3]float64{255, 250, 210}, 0.07)
	}

	// racking: far -> near on both sides (painter's algorithm)
	const bays = 6
	for i := 0; i < bays; i++ {
		t := float64(i) / (bays - 1) // 0 far .. 1 near
		drawRack(c, -1, t)
		drawRack(c, +1, t)
	}

	// a person pacing the left aisle, walking back and forth
	pz := 0.42 + 0.12*math.Sin(ts*0.6)
	px := cx - 120 + 40*math.Sin(ts*0.5)
	drawPerson(c, px, floorY(pz)+6, scaleAt(pz)*0.9, hiVis, ts*5)

	// forklift driving down the aisle left -> right, looping across ~11s
	fz := 0.72
	phase := math.Mod(ts/11.0, 1.0)
	fx := -0.15*float64(W) + 1.3*float64(W)*phase
	drawForklift(c, fx, floorY(fz), scaleAt(fz), ts)

	// a worker in the foreground right, shifting weight + occasional step
	drawPerson(c, cx+150+16*math.Sin(ts*0.8), floorY(0.9), scaleAt(0.9), hiVis2, ts*3.5)

	// a loose stack of pallet boxes foreground-left
	drawPalletStack(c, cx-230, floorY(0.95), scaleAt(0.95))

	vignette(c)
	grain(c, frame)
	return c.img
}

func floorY(t float64) float64  { hz := float64(H) * horizonY; return hz + (float64(H)-hz)*t }
func scaleAt(t float64) float64 { return 0.30 + 1.05*t }

// drawRack draws one pallet-racking bay on side (-1 left / +1 right) at depth t.
func drawRack(c *canvas, side, t float64) {
	cx := float64(W) / 2
	sc := scaleAt(t)
	y := floorY(t)
	// bay horizontal position: outside the aisle, moving to the edge as it nears
	edge := cx + side*(float64(W)*0.085+float64(W)*0.46*t)
	bw := 150 * sc  // bay width
	bh := 250 * sc  // bay height
	post := 10 * sc // upright thickness

	x0 := edge - bw/2
	x1 := edge + bw/2
	top := y - bh

	// uprights
	c.rect(x0, top, x0+post, y, rackSteel, 1)
	c.rect(x1-post, top, x1, y, rackSteel, 1)
	// three beam levels with boxes resting on them
	levels := 3
	for l := 0; l <= levels; l++ {
		by := y - bh*float64(l)/float64(levels)
		c.rect(x0, by-4*sc, x1, by, rackBeam, 1)
		if l < levels {
			drawBoxesOnShelf(c, x0+post, x1-post, by, sc, int(side)+l*2)
		}
	}
	// subtle depth shade on far bays
	if t < 0.5 {
		c.rect(x0, top, x1, y, [3]float64{20, 24, 34}, 0.18*(0.5-t)*2)
	}
}

func drawBoxesOnShelf(c *canvas, x0, x1, baseY, sc float64, seed int) {
	bw := 44 * sc
	gap := 6 * sc
	x := x0 + gap
	i := seed
	for x+bw < x1 {
		i++
		bh := (36 + float64((i*37)%18)) * sc
		shade := boxMid
		switch i % 3 {
		case 0:
			shade = boxLight
		case 2:
			shade = boxDark
		}
		top := baseY - bh
		// front face
		c.rect(x, top, x+bw, baseY, shade, 1)
		// top face (slight parallelogram for 3D)
		d := 7 * sc
		c.quad([4][2]float64{{x, top}, {x + d, top - d}, {x + bw + d, top - d}, {x + bw, top}},
			[3]float64{shade[0] + 26, shade[1] + 22, shade[2] + 18}, 1)
		// tape line + edge shade
		c.rect(x+bw/2-1.5*sc, top, x+bw/2+1.5*sc, baseY, [3]float64{shade[0] - 30, shade[1] - 28, shade[2] - 22}, 0.6)
		c.rect(x+bw-2*sc, top, x+bw, baseY, [3]float64{0, 0, 0}, 0.12)
		x += bw + gap
	}
}

func drawPalletStack(c *canvas, x, y, sc float64) {
	// pallet
	c.rect(x-50*sc, y-10*sc, x+50*sc, y, [3]float64{120, 92, 58}, 1)
	for i := 0; i < 4; i++ {
		c.rect(x-50*sc+float64(i)*28*sc, y-10*sc, x-50*sc+float64(i)*28*sc+8*sc, y, [3]float64{90, 68, 42}, 1)
	}
	// two boxes
	bw := 92 * sc
	c.rect(x-bw/2, y-10*sc-70*sc, x+bw/2, y-10*sc, boxMid, 1)
	d := 10 * sc
	c.quad([4][2]float64{{x - bw/2, y - 10*sc - 70*sc}, {x - bw/2 + d, y - 10*sc - 70*sc - d}, {x + bw/2 + d, y - 10*sc - 70*sc - d}, {x + bw/2, y - 10*sc - 70*sc}}, boxLight, 1)
	c.rect(x-bw/2, y-10*sc-70*sc, x+bw/2, y-10*sc, [3]float64{0, 0, 0}, 0.0)
	c.rect(x-2*sc, y-10*sc-70*sc, x+2*sc, y-10*sc, boxDark, 0.6)
}

// drawPerson draws a hi-vis worker with hard hat. (x,y) is at the feet; walk is
// a phase in radians that swings the legs for a simple stride.
func drawPerson(c *canvas, x, y, sc float64, vest [3]float64, walk float64) {
	if sc <= 0 {
		return
	}
	sw := math.Sin(walk) * 6 * sc // stride offset
	// contact shadow
	c.ellipse(x, y, 34*sc, 9*sc, [3]float64{10, 12, 16}, 0.35)
	legH := 46 * sc
	torsoH := 50 * sc
	bodyW := 30 * sc
	hipY := y - legH
	// legs (left/right swing in opposition)
	c.rect(x-bodyW/2+sw, hipY, x-2*sc+sw, y, [3]float64{40, 44, 70}, 1)
	c.rect(x+2*sc-sw, hipY, x+bodyW/2-sw, y, [3]float64{40, 44, 70}, 1)
	// boots
	c.rect(x-bodyW/2+sw, y-6*sc, x-2*sc+sw, y, [3]float64{20, 20, 24}, 1)
	c.rect(x+2*sc-sw, y-6*sc, x+bodyW/2-sw, y, [3]float64{20, 20, 24}, 1)
	// torso (hi-vis vest)
	shY := hipY - torsoH
	c.rect(x-bodyW/2, shY, x+bodyW/2, hipY, vest, 1)
	// reflective stripes
	c.rect(x-bodyW/2, shY+torsoH*0.5, x+bodyW/2, shY+torsoH*0.5+4*sc, [3]float64{235, 238, 242}, 0.85)
	c.rect(x-bodyW/2, shY+torsoH*0.72, x+bodyW/2, shY+torsoH*0.72+4*sc, [3]float64{235, 238, 242}, 0.85)
	// arms
	c.rect(x-bodyW/2-9*sc, shY+4*sc, x-bodyW/2, hipY-6*sc, vest, 1)
	c.rect(x+bodyW/2, shY+4*sc, x+bodyW/2+9*sc, hipY-6*sc, vest, 1)
	// neck + head
	headR := 13 * sc
	c.rect(x-5*sc, shY-6*sc, x+5*sc, shY, skin, 1)
	c.ellipse(x, shY-6*sc-headR, headR, headR*1.08, skin, 1)
	// hard hat
	c.ellipse(x, shY-6*sc-headR*1.5, headR*1.15, headR*0.8, [3]float64{245, 210, 40}, 1)
	c.rect(x-headR*1.2, shY-6*sc-headR*1.5, x+headR*1.2, shY-6*sc-headR*1.5+3*sc, [3]float64{220, 185, 30}, 1)
}

func drawForklift(c *canvas, x, y, sc float64, ts float64) {
	// shadow
	c.ellipse(x, y+4*sc, 95*sc, 16*sc, [3]float64{8, 10, 14}, 0.4)
	bw := 120 * sc
	bodyTop := y - 86*sc
	// counterweight body
	c.rect(x-bw/2, bodyTop+30*sc, x+bw/2, y-18*sc, forkYellow, 1)
	c.rect(x-bw/2, bodyTop+30*sc, x+bw/2, bodyTop+40*sc, [3]float64{255, 230, 120}, 0.6) // top highlight
	c.rect(x-bw/2, y-28*sc, x+bw/2, y-18*sc, [3]float64{30, 30, 34}, 1)                  // chassis trim
	// overhead guard
	c.rect(x-bw/2+6*sc, bodyTop, x-bw/2+14*sc, bodyTop+30*sc, forkDark, 1)
	c.rect(x+bw/2-26*sc, bodyTop, x+bw/2-18*sc, bodyTop+30*sc, forkDark, 1)
	c.rect(x-bw/2+6*sc, bodyTop, x+bw/2-18*sc, bodyTop+6*sc, forkDark, 1)
	// seat + driver
	c.rect(x-6*sc, bodyTop+14*sc, x+18*sc, bodyTop+32*sc, [3]float64{30, 32, 38}, 1)
	drawPerson(c, x+4*sc, bodyTop+20*sc, sc*0.42, hiVis2, 0)
	// mast (front, left side)
	mastX := x - bw/2 - 6*sc
	c.rect(mastX-6*sc, bodyTop-6*sc, mastX, y-14*sc, rackSteel, 1)
	c.rect(mastX+2*sc, bodyTop-6*sc, mastX+8*sc, y-14*sc, rackSteel, 1)
	// forks + a pallet with box
	forkY := y - 20*sc
	c.rect(mastX-30*sc, forkY, mastX, forkY+6*sc, [3]float64{60, 62, 68}, 1)
	c.rect(mastX-30*sc, forkY-40*sc, mastX-4*sc, forkY, boxMid, 1)
	c.quad([4][2]float64{{mastX - 30*sc, forkY - 40*sc}, {mastX - 24*sc, forkY - 46*sc}, {mastX + 2*sc, forkY - 46*sc}, {mastX - 4*sc, forkY - 40*sc}}, boxLight, 1)
	// wheels
	c.ellipse(x-bw/2+22*sc, y-10*sc, 20*sc, 20*sc, forkDark, 1)
	c.ellipse(x+bw/2-22*sc, y-10*sc, 22*sc, 22*sc, forkDark, 1)
	c.ellipse(x-bw/2+22*sc, y-10*sc, 8*sc, 8*sc, [3]float64{120, 122, 126}, 1)
	c.ellipse(x+bw/2-22*sc, y-10*sc, 9*sc, 9*sc, [3]float64{120, 122, 126}, 1)
	// beacon (blinks)
	if int(ts*4)%2 == 0 {
		c.ellipse(x-bw/2+10*sc, bodyTop-4*sc, 5*sc, 5*sc, [3]float64{255, 140, 40}, 1)
	}
}

func vignette(c *canvas) {
	cx, cy := float64(W)/2, float64(H)/2
	maxd := math.Hypot(cx, cy)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / maxd
			if d > 0.62 {
				c.blend(x, y, 0, 0, 0, (d-0.62)*0.55)
			}
		}
	}
}

// grain adds faint deterministic noise so flat fills read as a little filmic.
func grain(c *canvas, frame int) {
	seed := uint32(frame) + 1
	for y := 0; y < H; y += 1 {
		for x := 0; x < W; x += 1 {
			h := (uint32(x)*73856093 ^ uint32(y)*19349663 ^ seed*83492791)
			n := float64(h%32) - 16
			i := c.img.PixOffset(x, y)
			p := c.img.Pix
			p[i] = clamp8(float64(p[i]) + n*0.12)
			p[i+1] = clamp8(float64(p[i+1]) + n*0.12)
			p[i+2] = clamp8(float64(p[i+2]) + n*0.12)
		}
	}
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

var _ = color.RGBA{}
