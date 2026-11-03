package main

import (
	"fmt"
	"strconv"
)

func refreshIntervalText(minutes int) string {
	if minutes == 0 {
		return tr("Auto-sync: smart…", "自动同步：智能…")
	}
	return fmt.Sprintf(tr("Auto-sync: every %d min…", "自动同步：每%d分钟…"), minutes)
}

func (w *quotaMenuWindow) openInterval() {
	minutes := w.view.RefreshMinutes
	if minutes == 0 {
		minutes = 2
	}
	w.overlay.Input = strconv.Itoa(minutes)
	w.overlay.SelectAll = true
	w.focus = 0
}

func (w *quotaMenuWindow) intervalChar(ch rune) bool {
	o := w.overlay
	if o == nil || o.Kind != "interval" {
		return false
	}
	if o.Saving || w.focus != 0 {
		return true
	}
	if ch == 8 {
		if o.SelectAll {
			o.Input = ""
		} else if len(o.Input) > 0 {
			o.Input = o.Input[:len(o.Input)-1]
		}
		o.SelectAll = false
	} else if ch >= '0' && ch <= '9' {
		if o.SelectAll {
			o.Input = ""
		}
		o.SelectAll = false
		if len(o.Input) < 4 {
			o.Input += string(ch)
		}
	} else {
		return true
	}
	o.Error = ""
	w.invalidate()
	return true
}

func (w *quotaMenuWindow) intervalKey(key uintptr) bool {
	o := w.overlay
	if o == nil || o.Kind != "interval" {
		return false
	}
	if o.Saving {
		return true
	}
	if w.focus != 0 {
		return false
	}
	if key == 65 {
		ctrl, _, _ := qmKeyState.Call(17)
		if ctrl&0x8000 != 0 {
			o.SelectAll = true
			w.invalidate()
			return true
		}
	}
	if key == 13 {
		w.submitInterval(false)
		return true
	}
	if key == 38 || key == 40 {
		delta := 1
		if key == 40 {
			delta = -1
		}
		w.stepInterval(delta)
		return true
	}
	if key == 46 {
		o.Input = ""
		o.SelectAll = false
		o.Error = ""
		w.invalidate()
		return true
	}
	return false
}

func (w *quotaMenuWindow) stepInterval(delta int) {
	n, _ := strconv.Atoi(w.overlay.Input)
	w.overlay.Input = strconv.Itoa(max(1, min(1440, n+delta)))
	w.overlay.SelectAll = true
	w.overlay.Error = ""
	w.focus = 0
	w.invalidate()
}

func (w *quotaMenuWindow) activateInterval(a quotaMenuAction) bool {
	switch a.Kind {
	case "interval-input", "interval-step", "interval-save", "interval-smart":
	default:
		return false
	}
	o := w.overlay
	if o == nil || o.Kind != "interval" || o.Saving {
		return true
	}
	switch a.Kind {
	case "interval-input":
		w.focus = 0
		o.SelectAll = true
		w.invalidate()
	case "interval-step":
		w.stepInterval(a.Minutes)
	case "interval-save":
		w.submitInterval(false)
	case "interval-smart":
		w.submitInterval(true)
	}
	return true
}

func (w *quotaMenuWindow) submitInterval(smart bool) {
	o := w.overlay
	if o == nil || o.Saving {
		return
	}
	minutes := 0
	if !smart {
		var err error
		minutes, err = strconv.Atoi(o.Input)
		if err != nil || minutes < 1 || !validRefreshMinutes(minutes) {
			o.Error = tr("Enter a whole number from 1 to 1440", "请输入1–1440之间的整数分钟")
			w.focus = 0
			o.SelectAll = true
			w.invalidate()
			return
		}
	}
	o.Saving = true
	o.Error = ""
	w.invalidate()
	go func() {
		err := w.menu.onAction(quotaMenuAction{Kind: "interval", Minutes: minutes})
		_ = w.menu.invoke(func(ui *quotaMenuWindow) {
			if ui.overlay != o {
				if err != nil && ui.menu.onError != nil {
					ui.menu.onError(err.Error())
				}
				return
			}
			o.Saving = false
			if err != nil {
				o.Error = err.Error()
				ui.invalidate()
				return
			}
			ui.closeOverlay()
			ui.reload(false)
		})
	}()
}

func (p *qmPainter) intervalEditor(o *quotaMenuOverlay) {
	w := p.w
	width, height := min(440, w.width-24), 280
	x, y := (w.width-width)/2, max(8, (w.height-height)/2)
	o.Rect = quotaRect{x, y, width, height}
	p.round(o.Rect, 6, qmWhite, 0xcfd6e0)
	p.text(tr("Automatic usage sync", "自动同步间隔"), quotaRect{x + 18, y + 12, width - 36, 30}, 2, qmInk, false)
	p.text(tr("Enter 1–1440 minutes for all accounts.", "输入1–1440分钟，对所有账号生效。"), quotaRect{x + 18, y + 47, width - 36, 23}, 1, qmMuted, false)
	field := quotaRect{x + width/2 - 55, y + 82, 110, 36}
	border := uint32(0xcfd6e0)
	if w.focus == 0 {
		border = qmBlue
	}
	p.round(field, 4, qmWhite, border)
	if w.focus == 0 && o.SelectAll {
		p.fill(quotaRect{field.X + 8, field.Y + 5, field.W - 16, 26}, 0xeaf1fd)
	}
	p.centeredText(o.Input, field, 0, qmInk)
	if w.focus == 0 && !o.SelectAll && !o.Saving {
		cx := field.X + (field.W+p.measure(o.Input, 0))/2 + 2
		p.line(cx, field.Y+9, cx, field.Y+27, qmBlue, 1)
	}
	w.addHit(quotaMenuHit{Rect: field, Action: quotaMenuAction{Kind: "interval-input"}}, false)
	for i, delta := range []int{-1, 1} {
		r := quotaRect{field.X - 42, y + 82, 32, 36}
		label := "−"
		if i == 1 {
			r.X = field.X + field.W + 10
			label = "+"
		}
		p.round(r, 4, qmWhite, 0xcfd6e0)
		p.centeredText(label, r, 0, qmInk)
		w.addHit(quotaMenuHit{Rect: r, Action: quotaMenuAction{Kind: "interval-step", Minutes: delta}}, false)
	}
	p.text(tr("Smart: active 2 min / near limit 1 min", "智能模式：当前2分钟，临近上限1分钟"), quotaRect{x + 18, y + 134, width - 36, 22}, 1, qmMuted, false)
	p.text(tr("Others 10 min. Server retry waits take priority.", "其他账号10分钟；接口等待时间优先。"), quotaRect{x + 18, y + 157, width - 36, 22}, 1, qmMuted, false)
	message := o.Error
	if o.Saving {
		message = tr("Saving…", "正在保存…")
	}
	p.text(message, quotaRect{x + 18, y + 186, width - 36, 23}, 1, 0xb52c3b, false)
	w.addFixedTooltip(quotaRect{x + 18, y + 186, width - 36, 23}, message)
	labels := []string{tr("Smart mode", "智能模式"), tr("Cancel", "取消"), tr("Save", "保存")}
	for i, label := range labels {
		r := quotaRect{x + 18 + i*(width-36)/3, y + 232, (width - 48) / 3, 32}
		fill, ink := uint32(qmWhite), uint32(qmInk)
		if i == 2 {
			fill = qmBlue
			ink = qmWhite
		}
		p.round(r, 4, fill, 0xcfd6e0)
		p.centeredText(label, r, 0, ink)
		h := quotaMenuHit{Rect: r}
		if i == 0 {
			h.Action.Kind = "interval-smart"
		} else if i == 1 {
			h.Dismiss = true
		} else {
			h.Action.Kind = "interval-save"
		}
		if !o.Saving {
			w.addHit(h, false)
		}
	}
}
