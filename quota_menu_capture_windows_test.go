package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"unsafe"
)

// capture paints the actual GDI implementation into an isolated test bitmap.
// It does not capture the desktop or another application's window.
func (w *quotaMenuWindow) capture(path string) error {
	width, height := w.scale(w.width), w.scale(w.height)
	dc, _, _ := qmGetDC.Call(w.hwnd)
	defer qmReleaseDC.Call(w.hwnd, dc)
	buffer, _, _ := qmCreateDC.Call(dc)
	defer qmDeleteDC.Call(buffer)
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		Xppm, Yppm             int32
		Used, Important        uint32
	}{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, BitCount: 32}
	var pixels unsafe.Pointer
	bitmap, _, err := qmCreateDIB.Call(dc, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 || pixels == nil {
		return fmt.Errorf("capture native menu: %w", err)
	}
	defer qmDeleteObject.Call(bitmap)
	old, _, _ := qmSelectObject.Call(buffer, bitmap)
	defer qmSelectObject.Call(buffer, old)
	w.draw(buffer)
	qmFlush.Call()
	bgra := unsafe.Slice((*byte)(pixels), width*height*4)
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(bgra); i += 4 {
		picture.Pix[i], picture.Pix[i+1], picture.Pix[i+2], picture.Pix[i+3] = bgra[i+2], bgra[i+1], bgra[i], 255
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, picture)
}
