package main

func (w *quotaMenuWindow) accountDragHandleAt(x, y int) int {
	if w.overlay != nil || w.orderSaving || w.view.SortMode == "reset" || len(w.view.Accounts) < 2 || x < 6 || x >= 19 || y < w.bodyTop || y >= w.bodyBottom {
		return -1
	}
	rowY := w.bodyTop - w.scroll
	for i, account := range w.view.Accounts {
		height := quotaAccountHeight(account)
		handle := quotaRect{X: 6, Y: rowY + 13, W: 13, H: 16}
		if handle.contains(x, y) {
			return i
		}
		rowY += height
	}
	return -1
}

func (w *quotaMenuWindow) beginAccountOrderDrag(index int, point qmPoint) {
	if index < 0 || index >= len(w.view.Accounts) {
		return
	}
	w.orderDrag = &quotaMenuOrderDrag{Source: index, Slot: index, Start: point}
	w.focus, w.hover, w.pressed = -1, -1, -1
	qmSetCapture.Call(w.hwnd)
	w.invalidate()
}

func (w *quotaMenuWindow) updateAccountOrderDrag(x, y int) {
	drag := w.orderDrag
	if drag == nil {
		return
	}
	dx, dy := x-int(drag.Start.X), y-int(drag.Start.Y)
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	changed := false
	if !drag.Active && dx+dy >= 4 {
		drag.Active = true
		changed = true
	}
	if !drag.Active {
		return
	}
	slot := w.accountOrderSlotAt(y)
	if slot != drag.Slot {
		drag.Slot = slot
		changed = true
	}
	if changed {
		w.invalidate()
	}
}

func (w *quotaMenuWindow) accountOrderSlotAt(y int) int {
	if y < w.bodyTop {
		return 0
	}
	if y >= w.bodyBottom {
		return len(w.view.Accounts)
	}
	contentY := y + w.scroll
	rowY := w.bodyTop
	for i, account := range w.view.Accounts {
		height := quotaAccountHeight(account)
		if contentY < rowY+height/2 {
			return i
		}
		rowY += height
	}
	return len(w.view.Accounts)
}

func (w *quotaMenuWindow) finishAccountOrderDrag(commit bool) {
	drag := w.orderDrag
	if drag == nil {
		return
	}
	w.orderDrag = nil
	if capture, _, _ := qmGetCapture.Call(); capture == w.hwnd {
		qmReleaseCapture.Call()
	}
	if !commit || !drag.Active || len(w.view.Accounts) < 2 {
		w.invalidate()
		return
	}
	to := drag.Slot
	if to > drag.Source {
		to--
	}
	to = max(0, min(to, len(w.view.Accounts)-1))
	if to == drag.Source {
		w.invalidate()
		return
	}
	accounts := append([]quotaMenuAccount(nil), w.view.Accounts...)
	moved := accounts[drag.Source]
	accounts = append(accounts[:drag.Source], accounts[drag.Source+1:]...)
	accounts = append(accounts, quotaMenuAccount{})
	copy(accounts[to+1:], accounts[to:])
	accounts[to] = moved
	w.view.Accounts = accounts
	order := make([]string, 0, len(accounts))
	for _, account := range accounts {
		order = append(order, account.ID)
	}
	w.orderSaving = true
	w.invalidate()
	go func() {
		err := w.menu.onAction(quotaMenuAction{Kind: "order", AccountOrder: order})
		if err != nil && w.menu.onError != nil {
			w.menu.onError(err.Error())
		}
		_ = w.menu.invoke(func(ui *quotaMenuWindow) {
			ui.orderSaving = false
			ui.reload(false)
		})
	}()
}

func (w *quotaMenuWindow) drawAccountOrderDrag(p *qmPainter) {
	drag := w.orderDrag
	if drag == nil || !drag.Active || drag.Source < 0 || drag.Source >= len(w.view.Accounts) {
		return
	}
	y := w.bodyTop - w.scroll
	for i, account := range w.view.Accounts {
		height := quotaAccountHeight(account)
		if i == drag.Source {
			left, right := 4, w.width-4
			top, bottom := y+2, y+height-2
			p.line(left, top, right, top, qmBlue, 1)
			p.line(left, bottom, right, bottom, qmBlue, 1)
			p.line(left, top, left, bottom, qmBlue, 1)
			p.line(right, top, right, bottom, qmBlue, 1)
			break
		}
		y += height
	}
	y = w.bodyTop - w.scroll
	for i := 0; i < drag.Slot && i < len(w.view.Accounts); i++ {
		y += quotaAccountHeight(w.view.Accounts[i])
	}
	y = max(w.bodyTop, min(y, w.bodyBottom-1))
	p.line(12, y, w.width-16, y, qmBlue, 3)
}
