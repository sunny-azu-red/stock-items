package web

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func TestProcessPhoto(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(2000, 1000)); err != nil {
		t.Fatal(err)
	}
	full, thumb, err := processPhoto(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	fi, err := jpeg.DecodeConfig(bytes.NewReader(full))
	if err != nil || fi.Width != 1600 || fi.Height != 800 {
		t.Errorf("full = %dx%d, %v", fi.Width, fi.Height, err)
	}
	ti, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || ti.Width != thumbSide || ti.Height != thumbSide {
		t.Errorf("thumb = %dx%d, %v", ti.Width, ti.Height, err)
	}
	if _, _, err := processPhoto([]byte("not an image")); err == nil {
		t.Error("expected an error for garbage input")
	}
}

// jpegWithOrientation builds a JPEG with an EXIF APP1 segment carrying the
// given orientation tag.
func jpegWithOrientation(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	var img bytes.Buffer
	if err := jpeg.Encode(&img, testImage(w, h), nil); err != nil {
		t.Fatal(err)
	}
	var tiff bytes.Buffer
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(42))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD offset
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(1)) // one entry
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(3)) // SHORT
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(1))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(orientation))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0)) // next IFD
	app1 := append([]byte("Exif\x00\x00"), tiff.Bytes()...)

	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(app1)+2))
	out.Write(app1)
	out.Write(img.Bytes()[2:]) // skip the original SOI
	return out.Bytes()
}

func TestEXIFOrientation(t *testing.T) {
	data := jpegWithOrientation(t, 300, 200, 6)
	if got := exifOrientation(data); got != 6 {
		t.Fatalf("orientation = %d, want 6", got)
	}
	full, _, err := processPhoto(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := jpeg.DecodeConfig(bytes.NewReader(full))
	if cfg.Width != 200 || cfg.Height != 300 {
		t.Errorf("rotated size = %dx%d, want 200x300", cfg.Width, cfg.Height)
	}
	if got := exifOrientation([]byte("nope")); got != 1 {
		t.Errorf("non-jpeg orientation = %d", got)
	}
}
