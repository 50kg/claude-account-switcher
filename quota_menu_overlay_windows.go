package main

// Submenus and restart confirmations share the quota menu's painter and
// message loop. They never open a second system menu or dispatch background
// account actions while the user is only reading the menu.
func (p *qmPainter) overlay() {
	w := p.w
	o := w.overlay
	w.hits = nil
	w.resetTooltips()
	if o.Kind == "confirm" {
		p.confirmation(o)
		return
	}
	choices := w.choices(o.Kind)
	width := min(languageOffset(360, 314), w.width-24)
	height := quotaChoicesHeight(choices)
	x := max(12, min(o.Anchor.X, w.width-width-12))
	y := max(8, min(o.Anchor.Y-height-6, w.height-height-8))
	o.Rect = quotaRect{x, y, width, height}
	p.round(quotaRect{x + 2, y + 3, width, height}, 6, 0xe6eaf0, 0xe6eaf0)
	p.round(o.Rect, 6, qmWhite, 0xcfd6e0)
	title := tr("Settings", "设置")
	if o.Kind == "threshold" {
		title = tr("Auto-switch threshold", "自动切换阈值")
	}
	p.text(title, quotaRect{x + 14, y + 5, width - 28, 30}, 3, qmMuted, false)
	p.fill(quotaRect{x + 12, y + 37, width - 24, 1}, qmLine)
	rowY := y + 40
	for _, choice := range choices {
		if choice.Separator {
			p.fill(quotaRect{x + 12, rowY + 4, width - 24, 1}, qmLine)
			rowY += 10
			continue
		}
		r := quotaRect{x + 6, rowY, width - 12, 32}
		rowY += 34
		color := uint32(qmInk)
		if choice.Disabled {
			color = qmMuted
		} else {
			index := len(w.hits)
			if index == w.hover || index == w.focus {
				p.round(r, 4, 0xeaf1fd, 0xeaf1fd)
			}
			w.addHit(quotaMenuHit{Rect: r, Action: choice.Action}, false)
		}
		p.text(choice.Label, quotaRect{r.X + 10, r.Y, r.W - 40, r.H}, 0, color, false)
		if choice.Checked {
			p.text("✓", quotaRect{r.X + r.W - 28, r.Y, 18, r.H}, 3, qmBlue, true)
		}
	}
}

func (p *qmPainter) confirmation(o *quotaMenuOverlay) {
	w := p.w
	width := min(440, w.width-24)
	height := 184
	if o.Confirm.Kind == "switch" {
		height += 44
	}
	x, y := (w.width-width)/2, max(8, (w.height-height)/2)
	o.Rect = quotaRect{x, y, width, height}
	p.round(quotaRect{x + 2, y + 3, width, height}, 6, 0xe6eaf0, 0xe6eaf0)
	p.round(o.Rect, 6, qmWhite, 0xcfd6e0)
	title := tr("Switch account?", "切换账号？")
	if o.Confirm.Kind == "add" {
		title = tr("Add an account?", "添加账号？")
	} else if o.Confirm.Kind == "import" {
		title = tr("Import chats from all accounts?", "导入所有账号的聊天？")
	}
	p.text(title, quotaRect{x + 18, y + 12, width - 36, 30}, 2, qmInk, false)
	textY := y + 48
	if o.Confirm.Kind == "switch" {
		for _, a := range w.view.Accounts {
			if a.ID == o.Confirm.Account {
				p.text(a.Name+"  "+a.Plan, quotaRect{x + 18, textY, width - 36, 23}, 0, qmInk, false)
				p.text(a.Email, quotaRect{x + 18, textY + 22, width - 36, 23}, 1, qmMuted, false)
				break
			}
		}
		textY += 48
	}
	p.text(tr("Claude will close and restart.", "此操作会关闭并重新启动 Claude。"), quotaRect{x + 18, textY, width - 36, 24}, 0, qmInk, false)
	if o.Confirm.Kind == "add" {
		p.text(tr("Import chats manually from Settings after signing in.", "添加成功后，请从“设置”手动导入聊天。"), quotaRect{x + 18, textY + 25, width - 36, 24}, 1, qmMuted, false)
	}
	cancel := quotaRect{x + width - 240, y + height - 48, 90, 32}
	accept := quotaRect{x + width - 138, cancel.Y, 120, 32}
	p.round(cancel, 4, qmWhite, 0xcfd6e0)
	p.centeredText(tr("Cancel", "取消"), cancel, 0, qmInk)
	w.addHit(quotaMenuHit{Rect: cancel, Dismiss: true}, false)
	color := uint32(qmBlue)
	if w.view.Busy {
		color = qmMuted
	} else {
		a := o.Confirm
		a.Confirmed = true
		w.addHit(quotaMenuHit{Rect: accept, Action: a}, false)
	}
	p.round(accept, 4, color, color)
	p.centeredText(tr("Restart Claude", "确认并重启"), accept, 0, qmWhite)
}
