package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"
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

// pngWithSize is a 1x1 PNG whose header declares w x h, as a small file
// can ("decompression bomb").
func pngWithSize(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// Patch width and height in the IHDR chunk and fix up its CRC.
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}

// A huge canvas must be refused before decoding.
func TestProcessPhotoRefusesDecompressionBomb(t *testing.T) {
	for _, tc := range []struct {
		w, h    uint32
		refused bool
	}{
		{20000, 20000, true},
		{7072, 7071, true},   // 50,006,112 pixels: just over the limit
		{10000, 5000, false}, // exactly 50 MP: decoded (and found truncated)
	} {
		_, _, err := processPhoto(pngWithSize(t, tc.w, tc.h))
		if got := errors.Is(err, errPhotoTooBig); got != tc.refused || err == nil {
			t.Errorf("%dx%d: err = %v, want refused = %v", tc.w, tc.h, err, tc.refused)
		}
	}
}

// Only one photo is decoded at a time; the next waits for its turn, and
// stops waiting when its client goes away.
func TestPhotoDecodesOneAtATime(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(40, 30)); err != nil {
		t.Fatal(err)
	}
	decode := func(ctx context.Context) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, _, err := processPhotoQueued(ctx, buf.Bytes())
			done <- err
		}()
		return done
	}
	waiting := func(done <-chan error) {
		t.Helper()
		select {
		case err := <-done:
			t.Fatalf("decoded while another decode held the slot (err = %v)", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	finished := func(done <-chan error) error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("still waiting")
			return nil
		}
	}

	photoSlot <- struct{}{} // another upload is being decoded
	done := decode(context.Background())
	waiting(done)
	<-photoSlot
	if err := finished(done); err != nil {
		t.Fatal(err)
	}
	if len(photoSlot) != 0 {
		t.Fatal("a finished decode kept the slot")
	}

	photoSlot <- struct{}{}
	defer func() { <-photoSlot }()
	ctx, cancel := context.WithCancel(context.Background())
	done = decode(ctx)
	waiting(done)
	cancel()
	if err := finished(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(photoSlot) != 1 {
		t.Error("a cancelled wait freed the slot it never had")
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

// The thumbnail is the centre square, not the left or top edge.
func TestCoverCropsTheCentre(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 300; x++ {
			c := color.RGBA{255, 0, 0, 255} // red edges
			if x >= 100 && x < 200 {
				c = color.RGBA{0, 255, 0, 255} // green centre
			}
			img.Set(x, y, c)
		}
	}
	r, g, _, _ := cover(img, 10).At(1, 5).RGBA()
	if r > g {
		t.Errorf("thumbnail edge is red (r=%d g=%d): not the centre crop", r>>8, g>>8)
	}
}

// A large image is halved before the last step; that keeps its shape, and
// its colours right to the edges (no white seam where an odd row or column
// was left out).
func TestFitHalvesLargeImages(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 641, 321))
	for i := 0; i < len(src.Pix); i += 4 {
		copy(src.Pix[i:], []byte{200, 40, 10, 255})
	}
	img := fit(src, 100)
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("size = %v, want 100x50", b)
	}
	for _, p := range []image.Point{{0, 0}, {99, 0}, {0, 49}, {99, 49}, {50, 25}} {
		if r, g, b, _ := img.At(p.X, p.Y).RGBA(); r>>8 != 200 || g>>8 != 40 || b>>8 != 10 {
			t.Errorf("pixel %v = %d,%d,%d, want 200,40,10", p, r>>8, g>>8, b>>8)
		}
	}
}
