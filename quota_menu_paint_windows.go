package main

import (
	"fmt"
	"math"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	qmGDI           = windows.NewLazySystemDLL("gdi32.dll")
	qmBeginPaint    = user32.NewProc("BeginPaint")
	qmEndPaint      = user32.NewProc("EndPaint")
	qmFillRect      = user32.NewProc("FillRect")
	qmDrawText      = user32.NewProc("DrawTextW")
	qmGetDC         = user32.NewProc("GetDC")
	qmReleaseDC     = user32.NewProc("ReleaseDC")
	qmCreateDC      = qmGDI.NewProc("CreateCompatibleDC")
	qmDeleteDC      = qmGDI.NewProc("DeleteDC")
	qmCreateBitmap  = qmGDI.NewProc("CreateCompatibleBitmap")
	qmCreateDIB     = qmGDI.NewProc("CreateDIBSection")
	qmDeleteObject  = qmGDI.NewProc("DeleteObject")
	qmSelectObject  = qmGDI.NewProc("SelectObject")
	qmBitBlt        = qmGDI.NewProc("BitBlt")
	qmCreateBrush   = qmGDI.NewProc("CreateSolidBrush")
	qmCreatePen     = qmGDI.NewProc("CreatePen")
	qmMoveTo        = qmGDI.NewProc("MoveToEx")
	qmLineTo        = qmGDI.NewProc("LineTo")
	qmRoundRect     = qmGDI.NewProc("RoundRect")
	qmEllipse       = qmGDI.NewProc("Ellipse")
	qmArc           = qmGDI.NewProc("Arc")
	qmCreateFont    = qmGDI.NewProc("CreateFontW")
	qmSetTextColor  = qmGDI.NewProc("SetTextColor")
	qmSetBkMode     = qmGDI.NewProc("SetBkMode")
	qmTextExtent    = qmGDI.NewProc("GetTextExtentPoint32W")
	qmSaveDC        = qmGDI.NewProc("SaveDC")
	qmRestoreDC     = qmGDI.NewProc("RestoreDC")
	qmIntersectClip = qmGDI.NewProc("IntersectClipRect")
	qmFlush         = qmGDI.NewProc("GdiFlush")
)

const (
	qmInk   = 0x202630
	qmMuted = 0x616b78
	qmLine  = 0xe6eaf0
	qmBlue  = 0x367de5
	qmTrack = 0xe9eef5
	qmWhite = 0xffffff
)

type qmPaintStruct struct {
	DC                 uintptr
	Erase              int32
	Rect               windows.Rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}

type qmPainter struct {
	w       *quotaMenuWindow
	dc      uintptr
	brushes map[uint32]uintptr
}

func qmColor(rgb uint32) uintptr { return uintptr((rgb>>16)&255 | rgb&0xff00 | (rgb&255)<<16) }

func (w *quotaMenuWindow) freeFonts() {
	for i, font := range w.fonts {
		if font != 0 {
			qmDeleteObject.Call(font)
			w.fonts[i] = 0
		}
	}
}

func (w *quotaMenuWindow) makeFonts() {
	w.freeFonts()
	for i, spec := range [][2]int{{14, 400}, {13, 400}, {15, 600}, {13, 600}} {
		font, _, _ := qmCreateFont.Call(uintptr(-int64(w.scale(spec[0]))), 0, 0, 0, uintptr(spec[1]), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Segoe UI"))))
		w.fonts[i] = font
	}
}

func (p *qmPainter) brush(color uint32) uintptr {
	if brush := p.brushes[color]; brush != 0 {
		return brush
	}
	brush, _, _ := qmCreateBrush.Call(qmColor(color))
	p.brushes[color] = brush
	return brush
}

func (p *qmPainter) close() {
	for _, brush := range p.brushes {
		qmDeleteObject.Call(brush)
	}
}

func (p *qmPainter) native(r quotaRect) windows.Rect {
	return windows.Rect{Left: int32(p.w.scale(r.X)), Top: int32(p.w.scale(r.Y)), Right: int32(p.w.scale(r.X + r.W)), Bottom: int32(p.w.scale(r.Y + r.H))}
}

func (p *qmPainter) fill(r quotaRect, color uint32) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	rect := p.native(r)
	qmFillRect.Call(p.dc, uintptr(unsafe.Pointer(&rect)), p.brush(color))
}

func (p *qmPainter) round(r quotaRect, radius int, fill, border uint32) {
	pen, _, _ := qmCreatePen.Call(0, uintptr(max(1, p.w.scale(1))), qmColor(border))
	oldPen, _, _ := qmSelectObject.Call(p.dc, pen)
	oldBrush, _, _ := qmSelectObject.Call(p.dc, p.brush(fill))
	rect := p.native(r)
	qmRoundRect.Call(p.dc, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Right), uintptr(rect.Bottom), uintptr(p.w.scale(radius)), uintptr(p.w.scale(radius)))
	qmSelectObject.Call(p.dc, oldBrush)
	qmSelectObject.Call(p.dc, oldPen)
	qmDeleteObject.Call(pen)
}

func (p *qmPainter) line(x, y, x2, y2 int, color uint32, weight int) {
	pen, _, _ := qmCreatePen.Call(0, uintptr(max(1, p.w.scale(weight))), qmColor(color))
	old, _, _ := qmSelectObject.Call(p.dc, pen)
	qmMoveTo.Call(p.dc, uintptr(p.w.scale(x)), uintptr(p.w.scale(y)), 0)
	qmLineTo.Call(p.dc, uintptr(p.w.scale(x2)), uintptr(p.w.scale(y2)))
	qmSelectObject.Call(p.dc, old)
	qmDeleteObject.Call(pen)
}

func (p *qmPainter) text(text string, r quotaRect, font int, color uint32, right bool) {
	if r.W <= 0 || r.H <= 0 || text == "" {
		return
	}
	text = menuSingleLine(text)
	chars := windows.StringToUTF16(text)
	rect := p.native(r)
	old, _, _ := qmSelectObject.Call(p.dc, p.w.fonts[font])
	qmSetTextColor.Call(p.dc, qmColor(color))
	qmSetBkMode.Call(p.dc, 1)
	flags := uintptr(0x20 | 4 | 0x800 | 0x8000) // single line, center vertically, no '&' shortcuts, ellipsis
	if right {
		flags |= 2
	}
	qmDrawText.Call(p.dc, uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)-1), uintptr(unsafe.Pointer(&rect)), flags)
	qmSelectObject.Call(p.dc, old)
	runtime.KeepAlive(chars)
}

func (p *qmPainter) measure(text string, font int) int {
	chars := windows.StringToUTF16(menuSingleLine(text))
	if len(chars) == 1 {
		return 0
	}
	old, _, _ := qmSelectObject.Call(p.dc, p.w.fonts[font])
	size := struct{ X, Y int32 }{}
	qmTextExtent.Call(p.dc, uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)-1), uintptr(unsafe.Pointer(&size)))
	qmSelectObject.Call(p.dc, old)
	return int(math.Ceil(float64(size.X) * 96 / float64(p.w.dpi)))
}

func (w *quotaMenuWindow) paintWindow() {
	var ps qmPaintStruct
	dc, _, _ := qmBeginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer qmEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	if dc == 0 {
		return
	}
	var rect windows.Rect
	qmGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&rect)))
	buffer, _, _ := qmCreateDC.Call(dc)
	bitmap, _, _ := qmCreateBitmap.Call(dc, uintptr(rect.Right), uintptr(rect.Bottom))
	if buffer == 0 || bitmap == 0 {
		if bitmap != 0 {
			qmDeleteObject.Call(bitmap)
		}
		if buffer != 0 {
			qmDeleteDC.Call(buffer)
		}
		w.draw(dc)
		return
	}
	old, _, _ := qmSelectObject.Call(buffer, bitmap)
	w.draw(buffer)
	qmBitBlt.Call(dc, 0, 0, uintptr(rect.Right), uintptr(rect.Bottom), buffer, 0, 0, 0x00CC0020)
	qmSelectObject.Call(buffer, old)
	qmDeleteObject.Call(bitmap)
	qmDeleteDC.Call(buffer)
}

func (w *quotaMenuWindow) draw(dc uintptr) {
	p := &qmPainter{w: w, dc: dc, brushes: map[uint32]uintptr{}}
	defer p.close()
	w.hits = nil
	w.resetTooltips()
	p.fill(quotaRect{0, 0, w.width, w.height}, qmWhite)
	p.ring(30, 23)
	p.text(tr("Claude Account Switcher", "Claude 账号切换器"), quotaRect{50, 8, 250, 30}, 2, qmInk, false)
	updated := tr("Waiting for usage", "等待用量更新")
	if !w.view.Updated.IsZero() {
		updated = fmt.Sprintf(tr("%d accounts · %s", "%d个账号 · %s 更新"), len(w.view.Accounts), w.view.Updated.Local().Format("15:04"))
	}
	p.text(updated, quotaRect{300, 9, w.width - 320, 27}, 1, qmMuted, true)
	p.fill(quotaRect{1, quotaMenuHeaderHeight - 1, w.width - 2, 1}, qmLine)
	if w.view.Status != "" {
		p.fill(quotaRect{1, quotaMenuHeaderHeight, w.width - 2, 30}, 0xfaf7ef)
		p.text(w.view.Status, quotaRect{20, quotaMenuHeaderHeight + 4, w.width - 40, 23}, 1, qmMuted, false)
		w.addTooltip(quotaRect{10, quotaMenuHeaderHeight, w.width - 20, 30}, w.view.Status)
	}
	saved, _, _ := qmSaveDC.Call(dc)
	qmIntersectClip.Call(dc, 0, uintptr(w.scale(w.bodyTop)), uintptr(w.scale(w.width)), uintptr(w.scale(w.bodyBottom)))
	y := w.bodyTop - w.scroll
	if len(w.view.Accounts) == 0 {
		p.text(tr("Open Claude and sign in to add an account", "请先在Claude中登录账号"), quotaRect{20, y + 25, w.width - 40, 30}, 0, qmMuted, false)
	}
	for _, a := range w.view.Accounts {
		height := quotaAccountHeight(a)
		if y+height > w.bodyTop && y < w.bodyBottom {
			p.account(a, y, height)
		}
		y += height
	}
	qmRestoreDC.Call(dc, saved)
	if track, thumb := w.scrollbar(); track.H > 0 {
		p.round(track, 6, 0xf1f4f8, 0xf1f4f8)
		p.round(thumb, 6, 0xaeb7c4, 0xaeb7c4)
	}
	p.footer()
	if w.overlay != nil {
		p.overlay()
	}
	// The outline is painted last, preserving a single one-pixel menu edge.
	p.fill(quotaRect{0, 0, w.width, 1}, 0xcfd6e0)
	p.fill(quotaRect{0, w.height - 1, w.width, 1}, 0xcfd6e0)
	p.fill(quotaRect{0, 0, 1, w.height}, 0xcfd6e0)
	p.fill(quotaRect{w.width - 1, 0, 1, w.height}, 0xcfd6e0)
	if w.focus >= len(w.hits) {
		w.focus = -1
	}
	if w.hover >= 0 && w.hover < len(w.hits) {
		r := w.hits[w.hover].Rect
		p.line(r.X, r.Y+r.H-1, r.X+r.W, r.Y+r.H-1, 0x9eacc0, 1)
	}
	if w.focus >= 0 {
		r := w.hits[w.focus].Rect
		p.line(r.X, r.Y+r.H-1, r.X+r.W, r.Y+r.H-1, qmBlue, 2)
	}
}

func (p *qmPainter) ring(cx, cy int) {
	for _, part := range []struct {
		color uint32
		arc   bool
	}{{0xdbe4f2, false}, {qmBlue, true}} {
		pen, _, _ := qmCreatePen.Call(0, uintptr(p.w.scale(3)), qmColor(part.color))
		old, _, _ := qmSelectObject.Call(p.dc, pen)
		if part.arc {
			qmArc.Call(p.dc, uintptr(p.w.scale(cx-9)), uintptr(p.w.scale(cy-9)), uintptr(p.w.scale(cx+9)), uintptr(p.w.scale(cy+9)), uintptr(p.w.scale(cx)), uintptr(p.w.scale(cy-9)), uintptr(p.w.scale(cx+8)), uintptr(p.w.scale(cy+4)))
		} else {
			brushOld, _, _ := qmSelectObject.Call(p.dc, p.brush(qmWhite))
			qmEllipse.Call(p.dc, uintptr(p.w.scale(cx-9)), uintptr(p.w.scale(cy-9)), uintptr(p.w.scale(cx+9)), uintptr(p.w.scale(cy+9)))
			qmSelectObject.Call(p.dc, brushOld)
		}
		qmSelectObject.Call(p.dc, old)
		qmDeleteObject.Call(pen)
	}
}

func (w *quotaMenuWindow) addHit(hit quotaMenuHit, body bool) {
	if body {
		bottom := min(hit.Rect.Y+hit.Rect.H, w.bodyBottom)
		hit.Rect.Y = max(hit.Rect.Y, w.bodyTop)
		hit.Rect.H = bottom - hit.Rect.Y
	}
	if hit.Rect.H > 0 {
		w.hits = append(w.hits, hit)
	}
}

func (p *qmPainter) account(a quotaMenuAccount, y, height int) {
	w := p.w
	bg := uint32(qmWhite)
	if a.Current {
		bg = 0xf9fbff
	}
	p.fill(quotaRect{1, y, w.width - 2, height}, bg)
	p.fill(quotaRect{1, y + height - 1, w.width - 2, 1}, qmLine)
	buttonW := 88
	if language != "zh-CN" {
		buttonW = 102
	}
	buttonRect := quotaRect{w.width - 20 - buttonW, y + 9, buttonW, 27}
	leftEnd := buttonRect.X - 10
	identityWidth := leftEnd - 20
	emailW := min(p.measure(a.Email, 1), max(65, identityWidth-96))
	planW := p.measure(a.Plan, 1)
	nameW := min(p.measure(a.Name, 2), max(35, identityWidth-emailW-planW-18))
	p.text(a.Name, quotaRect{20, y + 9, nameW, 27}, 2, qmInk, false)
	x := 20 + nameW + 9
	p.text(a.Plan, quotaRect{x, y + 9, planW, 27}, 1, qmMuted, false)
	x += planW + 8
	p.text(a.Email, quotaRect{x, y + 9, max(0, leftEnd-x), 27}, 1, qmMuted, false)
	w.addTooltip(quotaRect{20, y + 7, max(1, leftEnd-20), 31}, a.Name+"\n"+a.Plan+"\n"+a.Email)
	if a.Current {
		p.round(buttonRect, 4, 0xeaf1fd, 0xeaf1fd)
		p.text(tr("✓ Current", "✓ 当前账号"), quotaRect{buttonRect.X + 4, buttonRect.Y, buttonRect.W - 8, 27}, 1, 0x215daa, true)
	} else {
		color := uint32(qmInk)
		if w.view.Busy {
			color = qmMuted
		}
		p.round(buttonRect, 5, qmWhite, 0xd2d9e3)
		p.text(tr("Switch", "切换账号"), quotaRect{buttonRect.X + 6, buttonRect.Y, buttonRect.W - 12, 27}, 1, color, false)
		if !w.view.Busy {
			w.addHit(quotaMenuHit{Rect: buttonRect, Action: quotaMenuAction{Kind: "switch", Account: a.ID, Active: w.view.Active}}, true)
		}
	}
	qy := y + quotaAccountHeaderHeight
	if a.Problem != "" || a.Stale {
		problem := a.Problem
		if problem == "" {
			problem = tr("Last known usage · waiting for an update", "上次用量 · 等待更新")
		}
		p.text(problem, quotaRect{20, qy - 1, w.width - 40, 21}, 1, qmMuted, false)
		w.addTooltip(quotaRect{20, qy - 1, w.width - 40, 21}, problem)
		qy += 26
	}
	for _, q := range a.Quotas {
		p.quota(q, qy)
		qy += quotaReadingHeight
	}
}

func (p *qmPainter) quota(q quotaMenuReading, y int) {
	w := p.w
	color := uint32(qmBlue)
	textColor := uint32(qmInk)
	if q.Stale || q.Used == nil {
		color = 0xaab4c2
		textColor = qmMuted
	} else if *q.Used >= 100 {
		color = 0xc23b47
		textColor = 0xb52c3b
	} else if *q.Used >= warnAt {
		color = 0xc8952d
		textColor = 0x8c5c00
	}
	values := q.values()
	valuesW := p.measure(values, 1)
	valueRect := quotaRect{w.width - 20 - valuesW, y, valuesW, 21}
	p.text(values, valueRect, 1, textColor, true)
	resetW := p.measure(q.Reset, 1)
	noticeW := 0
	if q.Notice != "" {
		noticeW = p.measure(q.Notice, 1) + 10
	}
	labelW := max(25, valueRect.X-20-resetW-noticeW-23)
	labelW = min(labelW, p.measure(q.Name, 0))
	p.text(q.Name, quotaRect{20, y, labelW, 21}, 0, qmInk, false)
	x := 20 + labelW + 8
	p.text(q.Reset, quotaRect{x, y, resetW, 21}, 1, qmMuted, false)
	if q.Notice != "" {
		fill := uint32(0xfcf1d9)
		if q.Used != nil && *q.Used >= 100 {
			fill = 0xfcebee
		}
		x += resetW + 7
		p.round(quotaRect{x, y + 1, noticeW, 19}, 3, fill, fill)
		p.text(q.Notice, quotaRect{x + 5, y, noticeW - 10, 21}, 1, textColor, false)
	}
	track := quotaRect{20, y + 24, w.width - 40, 4}
	p.round(track, 4, qmTrack, qmTrack)
	if q.Used != nil {
		fillW := int(math.Round(float64(track.W) * math.Max(0, math.Min(100, *q.Used)) / 100))
		if fillW > 0 {
			p.round(quotaRect{track.X, track.Y, fillW, track.H}, 4, color, color)
		}
	}
	w.addTooltip(quotaRect{20, y, w.width - 40, quotaReadingHeight}, q.Name+" · "+q.Reset+"\n"+q.values())
}

func (p *qmPainter) footer() {
	w := p.w
	y := w.bodyBottom
	p.fill(quotaRect{1, y, w.width - 2, quotaMenuFooterHeight}, 0xfafbfc)
	p.fill(quotaRect{1, y, w.width - 2, 1}, qmLine)
	p.text(tr("Auto-switch", "自动切换"), quotaRect{20, y + 2, 90, 30}, 0, qmInk, false)
	thresholdText := fmt.Sprintf(tr("At %.0f%% used", "阈值：已用 %.0f%%"), w.view.Threshold)
	thresholdRect := quotaRect{languageOffset(112, 86), y + 2, p.measure(thresholdText, 1) + 22, 30}
	p.text(thresholdText, quotaRect{thresholdRect.X, thresholdRect.Y, thresholdRect.W - 18, 30}, 1, qmMuted, false)
	arrowX := thresholdRect.X + thresholdRect.W - 10
	p.line(arrowX-3, y+16, arrowX, y+19, qmMuted, 1)
	p.line(arrowX, y+19, arrowX+3, y+16, qmMuted, 1)
	w.addHit(quotaMenuHit{Rect: thresholdRect, Popup: "threshold"}, false)
	label := tr("Off", "已关闭")
	fill := uint32(0xe7ebf0)
	if w.view.Auto {
		label = tr("On", "已开启")
		fill = 0x367de5
	}
	p.text(label, quotaRect{w.width - 125, y + 2, 65, 30}, 1, qmMuted, true)
	toggle := quotaRect{w.width - 50, y + 9, 31, 18}
	p.round(toggle, 18, fill, 0xb1bac7)
	knobX := toggle.X + 3
	if w.view.Auto {
		knobX += 13
	}
	knobColor := uint32(qmMuted)
	if w.view.Auto {
		knobColor = qmWhite
	}
	p.round(quotaRect{knobX, y + 12, 12, 12}, 12, knobColor, knobColor)
	w.addHit(quotaMenuHit{Rect: quotaRect{w.width - 135, y + 1, 125, 33}, Action: quotaMenuAction{Kind: "auto", Enabled: !w.view.Auto}}, false)
	p.fill(quotaRect{1, y + 35, w.width - 2, 1}, qmLine)
	labels := []string{tr("Add account", "添加账号"), tr("Settings", "设置"), tr("More", "更多"), tr("Quit", "退出")}
	for i, label := range labels {
		r := quotaRect{12 + i*(w.width-24)/4, y + 39, (w.width - 24) / 4, 30}
		hit := quotaMenuHit{Rect: r}
		switch i {
		case 0:
			hit.Action = quotaMenuAction{Kind: "add", Active: w.view.Active}
		case 1:
			hit.Popup = "settings"
		case 2:
			hit.Popup = "more"
		case 3:
			hit.Action = quotaMenuAction{Kind: "quit"}
		}
		if i == 0 && w.view.Busy {
			p.text(label, quotaRect{r.X + 22, r.Y, r.W - 22, r.H}, 0, qmMuted, false)
			continue
		}
		p.footerIcon(i, r.X+8, r.Y+15)
		p.text(label, quotaRect{r.X + 22, r.Y, r.W - 22, r.H}, 0, qmInk, false)
		w.addHit(hit, false)
	}
}

func (p *qmPainter) footerIcon(i, x, y int) {
	switch i {
	case 0:
		p.line(x-5, y, x+5, y, qmMuted, 1)
		p.line(x, y-5, x, y+5, qmMuted, 1)
	case 1:
		p.round(quotaRect{x - 5, y - 5, 10, 10}, 10, 0xfafbfc, qmMuted)
		p.round(quotaRect{x - 2, y - 2, 4, 4}, 4, 0xfafbfc, qmMuted)
		for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
			p.line(x+d[0]*5, y+d[1]*5, x+d[0]*7, y+d[1]*7, qmMuted, 1)
		}
	case 2:
		for _, offset := range []int{-5, 0, 5} {
			p.round(quotaRect{x + offset, y - 1, 2, 2}, 2, qmMuted, qmMuted)
		}
	case 3:
		pen, _, _ := qmCreatePen.Call(0, uintptr(max(1, p.w.scale(1))), qmColor(qmMuted))
		old, _, _ := qmSelectObject.Call(p.dc, pen)
		qmArc.Call(p.dc, uintptr(p.w.scale(x-6)), uintptr(p.w.scale(y-6)), uintptr(p.w.scale(x+6)), uintptr(p.w.scale(y+6)), uintptr(p.w.scale(x-4)), uintptr(p.w.scale(y-5)), uintptr(p.w.scale(x+4)), uintptr(p.w.scale(y-5)))
		qmSelectObject.Call(p.dc, old)
		qmDeleteObject.Call(pen)
		p.line(x, y-7, x, y, qmMuted, 1)
	}
}

func languageOffset(english, chinese int) int {
	if language == "zh-CN" {
		return chinese
	}
	return english
}

type qmToolInfo struct {
	Size, Flags     uint32
	Window, ID      uintptr
	Rect            windows.Rect
	Instance        uintptr
	Text            *uint16
	Param, Reserved uintptr
}

func (w *quotaMenuWindow) createTooltip() {
	controls := windows.NewLazySystemDLL("comctl32.dll").NewProc("InitCommonControlsEx")
	init := struct{ Size, Classes uint32 }{8, 0xff}
	controls.Call(uintptr(unsafe.Pointer(&init)))
	w.tooltip, _, _ = qmCreateWindow.Call(8, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("tooltips_class32"))), 0, 0x80000003, 0, 0, 0, 0, w.hwnd, 0, 0, 0)
	if w.tooltip != 0 {
		procSendMessage.Call(w.tooltip, 0x418, 0, uintptr(w.scale(520))) // TTM_SETMAXTIPWIDTH
		procSendMessage.Call(w.tooltip, 0x403, 3, 600)                   // TTM_SETDELAYTIME, initial
	}
}

func (w *quotaMenuWindow) resetTooltips() {
	if w.tooltip == 0 {
		return
	}
	for i := range w.tooltipText {
		info := qmToolInfo{Window: w.hwnd, ID: uintptr(i + 1)}
		info.Size = uint32(unsafe.Sizeof(info))
		procSendMessage.Call(w.tooltip, 0x433, 0, uintptr(unsafe.Pointer(&info))) // TTM_DELTOOLW
	}
	w.tooltipText = nil
}

func (w *quotaMenuWindow) addTooltip(r quotaRect, text string) {
	if w.tooltip == 0 || text == "" {
		return
	}
	// Only the currently visible part of a body row owns its tooltip.
	if r.Y < w.bodyTop || r.Y+r.H > w.bodyBottom {
		return
	}
	chars := windows.StringToUTF16(text)
	w.tooltipText = append(w.tooltipText, chars)
	p := qmPainter{w: w}
	info := qmToolInfo{Flags: 0x10, Window: w.hwnd, ID: uintptr(len(w.tooltipText)), Rect: p.native(r), Text: &chars[0]}
	info.Size = uint32(unsafe.Sizeof(info))
	procSendMessage.Call(w.tooltip, 0x432, 0, uintptr(unsafe.Pointer(&info))) // TTM_ADDTOOLW
	runtime.KeepAlive(chars)
}
