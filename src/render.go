package main

// render.go - draws one 960x160 frame for the current view. Pure: it reads a
// UI snapshot, the chain and the audio status and returns an image. Strings
// that reach the panel are ASCII only (the panel font has nothing else).

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"

	"github.com/federico-pepe/ableton-push-hack/core/gfx"
	"github.com/federico-pepe/ableton-push-hack/core/gfx/text"
	"github.com/federico-pepe/ableton-push-hack/core/gfx/widgets"
	"github.com/federico-pepe/ableton-push-hack/core/push3"
	"golang.org/x/image/font"
)

const (
	screenW = push3.VisW
	screenH = push3.VisH
	cellW   = screenW / 8
	stripH  = 16
	botY    = screenH - stripH
	rowH    = 13
)

var theme = widgets.Default

func pal(name string) color.NRGBA {
	idx, ok := push3.ColorByName(name)
	if !ok {
		panic("render: unknown palette color " + name)
	}
	return push3.ColorForIndex(idx).RGB
}

var (
	colTile     = pal("dk_ocean")
	colTileOff  = pal("dk_void")
	colAccent   = pal("orange")
	colWarn     = pal("red")
	colOK       = pal("green_vv")
	colMeter    = pal("green")
	colMeterHot = pal("amber")
	colDim      = pal("gray_mid")
)

var (
	faceOnce            sync.Once
	faceBig, faceMedium font.Face
)

func faces() (big, medium font.Face) {
	faceOnce.Do(func() {
		faceBig, _ = text.NewFace(text.Bold, 16)
		faceMedium, _ = text.NewFace(text.Bold, 13)
	})
	return faceBig, faceMedium
}

// drawFit draws s in the biggest face that fits w pixels, else cuts it.
func drawFit(img *image.NRGBA, x, baseline, w int, s string, col color.NRGBA, center bool) {
	big, med := faces()
	for _, f := range []font.Face{big, med} {
		if f == nil {
			continue
		}
		if tw := text.WidthWith(s, f); tw <= w {
			if center {
				x += (w - tw) / 2
			}
			text.DrawWith(img, x, baseline, s, col, f)
			return
		}
	}
	cut := text.Truncate(s, w/7)
	if center {
		x += (w - text.Width(cut)) / 2
	}
	text.Draw(img, x, baseline, cut, col)
}

func drawCentered(img *image.NRGBA, cx, baseline int, s string, col color.NRGBA, maxW int) {
	s = text.Truncate(s, maxW/7)
	text.Draw(img, cx-text.Width(s)/2, baseline, s, col)
}

// wrap splits s into lines of at most n characters, at spaces.
func wrap(s string, n int) []string {
	var lines []string
	var cur string
	for _, w := range strings.Fields(s) {
		if cur == "" {
			cur = w
		} else if len(cur)+1+len(w) <= n {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func dB(lin float64) string {
	if lin <= 0.00001 {
		return "-inf dB"
	}
	return fmt.Sprintf("%+.1f dB", 20*math.Log10(lin))
}

// meterFrac maps a peak to 0..1 over -60..0 dBFS.
func meterFrac(peak float64) float64 {
	if peak <= 0.001 {
		return 0
	}
	f := (20*math.Log10(peak) + 60) / 60
	return math.Max(0, math.Min(1, f))
}

func drawMeter(img *image.NRGBA, x, y, w, h int, peak float64) {
	f := meterFrac(peak)
	col := colMeter
	if peak >= 0.89 { // above -1 dBFS
		col = colMeterHot
	}
	widgets.DrawMeter(img, x, y, w, h, f, col, theme.DarkGray)
}

func newFrame() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, screenW, screenH))
	gfx.FillRect(img, 0, 0, screenW, screenH, theme.Black)
	return img
}

// render draws the frame for the current view.
func render(u uiSnapshot, ch *Chain, cat *Catalog, ui *UI, a audioSnapshot) *image.NRGBA {
	switch u.View {
	case viewPlugin:
		return renderPlugin(u, ch, a)
	case viewBrowser:
		return renderBrowser(u, ch, cat, ui)
	default:
		return renderChain(u, ch, a)
	}
}

// statusLine is the flash message, a problem with the audio, or the meters.
func drawStatusLine(img *image.NRGBA, y int, u uiSnapshot, ch *Chain, a audioSnapshot) {
	gfx.FillRect(img, 0, y, screenW, rowH+3, theme.Black)
	base := y + rowH - 1
	switch {
	case u.Flash != "":
		text.Draw(img, 8, base, u.Flash, theme.StatusCol)
		return
	case a.Message() != "":
		text.Draw(img, 8, base, a.Message(), colWarn)
	default:
		text.Draw(img, 8, base, "IN", theme.Gray)
		drawMeter(img, 30, y+3, 150, 8, a.PeakIn)
		text.Draw(img, 196, base, "OUT", theme.Gray)
		drawMeter(img, 225, y+3, 150, 8, a.PeakOut)
	}
	_, og := ch.Gains()
	right := fmt.Sprintf("DSP %d%%   OUT GAIN %s", int(math.Round(a.DSPLoad*100)), dB(og))
	if u.Delete {
		right = "DELETE: PRESS A SLOT TO REMOVE IT"
	}
	text.Draw(img, screenW-8-text.Width(right), base, right, theme.Gray)
}

// ---- chain view ----

func renderChain(u uiSnapshot, ch *Chain, a audioSnapshot) *image.NRGBA {
	img := newFrame()
	slots := ch.Slots()
	n := len(slots)

	var top, bot [8]widgets.SoftButton
	for i := 0; i < 8; i++ {
		x := i * cellW
		cx := x + cellW/2
		switch {
		case i < n:
			s := slots[i]
			tile, nameCol := colTile, theme.White
			if s.Bypass {
				tile, nameCol = colTileOff, colDim
				top[i] = widgets.SoftButton{Label: "BYPASSED", State: widgets.SoftOff}
			} else {
				top[i] = widgets.SoftButton{Label: "ON", State: widgets.SoftOn}
			}
			if u.Delete {
				top[i] = widgets.SoftButton{Label: "REMOVE", State: widgets.SoftConfirm}
				bot[i] = widgets.SoftButton{Label: "REMOVE", State: widgets.SoftConfirm}
			} else {
				bot[i] = widgets.SoftButton{Label: "EDIT"}
			}
			gfx.FillRect(img, x+3, stripH+4, cellW-6, 88, tile)
			text.Draw(img, x+8, stripH+18, fmt.Sprintf("%d", i+1), colAccent)
			cat := text.Truncate(s.Category, 13)
			text.Draw(img, x+cellW-8-text.Width(cat), stripH+18, cat, theme.Gray)
			drawFit(img, x+6, stripH+52, cellW-12, s.Name, nameCol, true)
			info := fmt.Sprintf("%d PARAMS", len(s.Params))
			if s.Bypass {
				info = "BYPASSED"
			}
			drawCentered(img, cx, stripH+80, info, theme.Gray, cellW-12)
		case i == n:
			widgets.DrawBorder(img, x+3, stripH+4, cellW-6, 88, theme.DarkGray)
			drawCentered(img, cx, stripH+52, "+ ADD", theme.Gray, cellW)
			bot[i] = widgets.SoftButton{Label: "+ ADD"}
		case n > 0:
			widgets.DrawBorder(img, x+3, stripH+4, cellW-6, 88, theme.DarkGray)
		}
		// signal flow between slots
		if i > 0 && i <= n {
			text.Draw(img, x-3, stripH+52, ">", colAccent)
		}
	}
	if n == 0 {
		// over the empty slots 2-8
		msg := "Empty chain: press + ADD or the Add button to pick an effect"
		text.Draw(img, cellW+(7*cellW-text.Width(msg))/2, stripH+52, msg, theme.Gray)
	}
	widgets.DrawBotStrip(img, theme, 0, screenW, cellW, stripH, top, "")
	drawStatusLine(img, stripH+96, u, ch, a)
	widgets.DrawBotStrip(img, theme, botY, screenW, cellW, stripH, bot, "")
	return img
}

// ---- plugin view ----

func renderPlugin(u uiSnapshot, ch *Chain, a audioSnapshot) *image.NRGBA {
	img := newFrame()
	s, eff, ok := ch.Slot(u.Slot)
	if !ok {
		return renderChain(u, ch, a)
	}
	params := ch.Params(u.Slot)
	n := ch.Len()

	var top [8]widgets.SoftButton
	for i, l := range pluginTopLabels {
		b := widgets.SoftButton{Label: l}
		switch i {
		case topBypass:
			if s.Bypass {
				b = widgets.SoftButton{Label: "BYPASSED", State: widgets.SoftOff}
			} else {
				b = widgets.SoftButton{Label: "ON", State: widgets.SoftOn}
			}
		case topMoveLeft, topPrevFX:
			if u.Slot == 0 {
				b.Label = ""
			}
		case topMoveRight, topNextFX:
			if u.Slot >= n-1 {
				b.Label = ""
			}
		case topRemove:
			b.State = widgets.SoftOff
		}
		top[i] = b
	}
	widgets.DrawBotStrip(img, theme, 0, screenW, cellW, stripH, top, "")

	// header: slot, name, category, description
	hy := stripH + 2
	gfx.FillRect(img, 0, hy, screenW, 20, colTile)
	slotTxt := fmt.Sprintf("%d/%d", u.Slot+1, n)
	text.Draw(img, 8, hy+15, slotTxt, colAccent)
	big, _ := faces()
	nameX := 8 + text.Width(slotTxt) + 10
	nameCol := theme.White
	if s.Bypass {
		nameCol = colDim
	}
	if big != nil {
		text.DrawWith(img, nameX, hy+16, eff.Name, nameCol, big)
		nameX += text.WidthWith(eff.Name, big) + 12
	} else {
		text.Draw(img, nameX, hy+15, eff.Name, nameCol)
		nameX += text.Width(eff.Name) + 12
	}
	right := strings.ToUpper(eff.Category)
	text.Draw(img, screenW-8-text.Width(right), hy+15, right, theme.Gray)
	if u.Flash != "" {
		text.Draw(img, nameX, hy+15, text.Truncate(u.Flash, (screenW-nameX-text.Width(right)-24)/7), theme.StatusCol)
	} else {
		text.Draw(img, nameX, hy+15, text.Truncate(eff.What, (screenW-nameX-text.Width(right)-24)/7), theme.Gray)
	}

	// knobs
	for i := 0; i < 8; i++ {
		p := u.Bank*8 + i
		if p >= len(params) {
			continue
		}
		pr := params[p]
		cx := i*cellW + cellW/2
		drawCentered(img, cx, stripH+38, pr.Name, theme.White, cellW-6)
		col := colAccent
		if s.Bypass {
			col = colDim
		}
		widgets.DrawKnobArc(img, theme, cx, stripH+70, 22, widgets.Knob{Value: pr.Value * 100, Min: 0, Max: 100, Color: col})
		if math.Abs(pr.Value-pr.Default) < 0.0005 {
			gfx.FillRect(img, cx-2, stripH+44, 4, 3, theme.Gray) // dot: at the default
		}
		drawCentered(img, cx, stripH+110, pr.Display, theme.Gray, cellW-6)
	}
	if len(params) == 0 {
		msg := "This effect has no parameters"
		text.Draw(img, (screenW-text.Width(msg))/2, stripH+76, msg, theme.Gray)
	}
	if u.Delete {
		msg := "DELETE: TOUCH OR TURN A KNOB TO RESET IT"
		text.Draw(img, screenW-8-text.Width(msg), stripH+124, msg, theme.StatusCol)
	} else if m := a.Message(); m != "" {
		text.Draw(img, 8, stripH+124, m, colWarn)
	}

	var bot [8]widgets.SoftButton
	if nb := banks(len(params)); nb > 1 {
		for b := 0; b < nb && b < 8; b++ {
			hi := (b + 1) * 8
			if hi > len(params) {
				hi = len(params)
			}
			label := fmt.Sprintf("%d-%d", b*8+1, hi)
			if hi == b*8+1 {
				label = fmt.Sprintf("%d", hi)
			}
			btn := widgets.SoftButton{Label: label}
			if b == u.Bank {
				btn.State = widgets.SoftOn
			}
			bot[b] = btn
		}
	}
	widgets.DrawBotStrip(img, theme, botY, screenW, cellW, stripH, bot, "")
	return img
}

// ---- browser ----

func drawList(img *image.NRGBA, x, y, w, rows int, items []string, cursor int, focus bool) {
	scroll := 0
	if cursor >= rows {
		scroll = cursor - rows/2
	}
	if max := len(items) - rows; scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	for r := 0; r < rows && scroll+r < len(items); r++ {
		i := scroll + r
		ry := y + r*rowH
		col := theme.White
		if i == cursor {
			bg := theme.DarkGray
			if focus {
				bg = theme.Select
			}
			gfx.FillRect(img, x, ry, w, rowH, bg)
		} else {
			col = theme.Gray
		}
		text.Draw(img, x+4, ry+rowH-2, text.Truncate(items[i], (w-8)/7), col)
	}
	if len(items) > rows {
		widgets.DrawScrollbar(img, theme, y, y+rows*rowH, x+w+6, len(items), rows, scroll)
	}
}

func renderBrowser(u uiSnapshot, ch *Chain, cat *Catalog, ui *UI) *image.NRGBA {
	img := newFrame()
	var top, bot [8]widgets.SoftButton
	top[0] = widgets.SoftButton{Label: "CATEGORY"}
	top[1] = widgets.SoftButton{Label: "EFFECT"}
	widgets.DrawBotStrip(img, theme, 0, screenW, cellW, stripH, top, "")

	const listY = stripH + 4
	const rows = 9
	drawList(img, 0, listY, 2*cellW-10, rows, cat.Categories, u.BrCat, false)

	items := ui.browserItems(u.BrCat)
	names := make([]string, len(items))
	for i, e := range items {
		names[i] = e.Name
	}
	drawList(img, 2*cellW, listY, 3*cellW-10, rows, names, u.BrItem, true)

	// details of the highlighted effect
	px := 5*cellW + 6
	pw := screenW - px - 6
	title := fmt.Sprintf("ADD TO SLOT %d", ch.Len()+1)
	if u.BrMode == browseReplace {
		if s, _, ok := ch.Slot(u.BrPos); ok {
			title = fmt.Sprintf("REPLACE SLOT %d: %s", u.BrPos+1, s.Name)
		}
	} else if u.BrPos >= 0 {
		title = fmt.Sprintf("INSERT AT SLOT %d", u.BrPos+1)
	}
	text.Draw(img, px, listY+10, text.Truncate(title, pw/7), colAccent)
	if u.BrItem >= 0 && u.BrItem < len(items) {
		e := items[u.BrItem]
		drawFit(img, px, listY+32, pw, e.Name, theme.White, false)
		text.Draw(img, px, listY+48, fmt.Sprintf("%s - %d PARAMS", e.Category, e.NParams), theme.Gray)
		for i, l := range wrap(e.What, pw/7) {
			if i >= 5 {
				break
			}
			text.Draw(img, px, listY+66+i*rowH, l, theme.White)
		}
	}
	if u.Flash != "" {
		text.Draw(img, px, listY+rows*rowH, text.Truncate(u.Flash, pw/7), theme.StatusCol)
	}

	bot[0] = widgets.SoftButton{Label: "BACK"}
	if u.BrMode == browseReplace {
		bot[7] = widgets.SoftButton{Label: "REPLACE", State: widgets.SoftOn}
	} else {
		bot[7] = widgets.SoftButton{Label: "ADD", State: widgets.SoftOn}
	}
	widgets.DrawBotStrip(img, theme, botY, screenW, cellW, stripH, bot, "")
	return img
}
