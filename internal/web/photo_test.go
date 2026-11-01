package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// needVips skips a test that runs libvips when it isn't installed, and
// fails it in CI, which installs it.
func needVips(t *testing.T) {
	t.Helper()
	for _, bin := range []string{vipsBin, vipsheaderBin} {
		if _, err := exec.LookPath(bin); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("%s not found; CI installs libvips-tools: %v", bin, err)
			}
			t.Skipf("%s not found: install libvips-tools to run the photo tests (%v)", bin, err)
		}
	}
}

// vipsMake runs vips to make a test image, in dir.
func vipsMake(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("vips", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vips %v: %v\n%s", args, err, out)
	}
}

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	return img
}

// near reports whether c is within 24 of r, g, b in every channel (JPEG
// is lossy, saturated red the most).
func near(c color.Color, r, g, b uint8) bool {
	cr, cg, cb, _ := c.RGBA()
	d := func(a uint32, b uint8) bool { return int(a>>8)-int(b) <= 24 && int(b)-int(a>>8) <= 24 }
	return d(cr, r) && d(cg, g) && d(cb, b)
}

// Every format photos take comes out as a JPEG of at most 1600 px a side
// (not enlarged), with a 320 px square thumbnail, and without metadata.
func TestProcessPhotoFormats(t *testing.T) {
	needVips(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tall.png"), encodePNG(t, testImage(1000, 2000)), 0o600); err != nil {
		t.Fatal(err)
	}
	vipsMake(t, dir, "copy", "tall.png", "tall.webp")
	webp, err := os.ReadFile(filepath.Join(dir, "tall.webp"))
	if err != nil {
		t.Fatal(err)
	}
	heic, err := os.ReadFile("testdata/photo-12mp.heic") // 4032x3024, all (200, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	var anim bytes.Buffer
	pal := color.Palette{color.RGBA{0, 0, 255, 255}, color.RGBA{255, 0, 0, 255}}
	frame := func(i uint8) *image.Paletted {
		m := image.NewPaletted(image.Rect(0, 0, 400, 300), pal)
		for j := range m.Pix {
			m.Pix[j] = i
		}
		return m
	}
	if err := gif.EncodeAll(&anim, &gif.GIF{Image: []*image.Paletted{frame(0), frame(1)}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	gray := image.NewGray16(image.Rect(0, 0, 3000, 100))
	for i := range gray.Pix {
		gray.Pix[i] = 0x80
	}

	for _, tc := range []struct {
		name string
		data []byte
		w, h int
		// centre, when set, is the colour of the photo's centre.
		centre *color.RGBA
	}{
		{"jpeg", encodeJPEG(t, testImage(2000, 1000)), 1600, 800, nil},
		{"png", encodePNG(t, testImage(2000, 1000)), 1600, 800, nil},
		{"gif, first frame", anim.Bytes(), 400, 300, &color.RGBA{0, 0, 255, 255}},
		{"webp", webp, 800, 1600, nil},
		{"heic", heic, 1600, 1200, &color.RGBA{200, 40, 10, 255}},
		{"16-bit grey png", encodePNG(t, gray), 1600, 53, &color.RGBA{128, 128, 128, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full, thumb, err := processPhoto(context.Background(), tc.data)
			if err != nil {
				t.Fatal(err)
			}
			img := decodeJPEG(t, full)
			if b := img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Errorf("full = %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.w, tc.h)
			}
			if c := tc.centre; c != nil {
				if got := img.At(tc.w/2, tc.h/2); !near(got, c.R, c.G, c.B) {
					t.Errorf("centre = %v, want about %v", got, *c)
				}
			}
			if b := decodeJPEG(t, thumb).Bounds(); b.Dx() != thumbSide || b.Dy() != thumbSide {
				t.Errorf("thumb = %dx%d", b.Dx(), b.Dy())
			}
			if bytes.Contains(full, []byte("Exif\x00")) {
				t.Error("the photo kept its EXIF block")
			}
		})
	}
}

// jpegWithOrientation builds a JPEG with an EXIF APP1 segment carrying the
// given orientation tag.
func jpegWithOrientation(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := encodeJPEG(t, testImage(w, h))
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
	out.Write(img[2:]) // skip the original SOI
	return out.Bytes()
}

// A phone photo stored sideways (EXIF orientation 6) comes out portrait.
func TestProcessPhotoUpright(t *testing.T) {
	needVips(t)
	full, thumb, err := processPhoto(context.Background(), jpegWithOrientation(t, 300, 200, 6))
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeJPEG(t, full).Bounds(); b.Dx() != 200 || b.Dy() != 300 {
		t.Errorf("rotated size = %dx%d, want 200x300", b.Dx(), b.Dy())
	}
	if b := decodeJPEG(t, thumb).Bounds(); b.Dx() != thumbSide || b.Dy() != thumbSide {
		t.Errorf("thumb = %dx%d", b.Dx(), b.Dy())
	}
}

// Transparent parts are white (JPEG has no transparency), not black.
func TestProcessPhotoTransparencyOnWhite(t *testing.T) {
	needVips(t)
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100)) // all transparent
	for y := 40; y < 60; y++ {
		for x := 40; x < 60; x++ {
			img.Set(x, y, color.NRGBA{200, 0, 0, 255})
		}
	}
	full, thumb, err := processPhoto(context.Background(), encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	f, th := decodeJPEG(t, full), decodeJPEG(t, thumb)
	if c := f.At(5, 5); !near(c, 255, 255, 255) {
		t.Errorf("transparent corner = %v, want white", c)
	}
	if c := f.At(50, 50); !near(c, 200, 0, 0) {
		t.Errorf("centre = %v, want red", c)
	}
	if c := th.At(10, 10); !near(c, 255, 255, 255) {
		t.Errorf("thumbnail corner = %v, want white", c)
	}
}

// The thumbnail is the centre square, not the left or top edge.
func TestProcessPhotoThumbIsTheCentre(t *testing.T) {
	needVips(t)
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
	_, thumb, err := processPhoto(context.Background(), encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeJPEG(t, thumb).At(8, thumbSide/2); !near(c, 0, 255, 0) {
		t.Errorf("thumbnail edge = %v: not the centre crop", c)
	}
}

// Files that aren't photos are refused as such, and a damaged photo is
// refused rather than saved half grey.
func TestProcessPhotoRefusesOtherFiles(t *testing.T) {
	needVips(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), encodePNG(t, testImage(64, 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	vipsMake(t, dir, "copy", "a.png", "a.tif")
	tif, err := os.ReadFile(filepath.Join(dir, "a.tif"))
	if err != nil {
		t.Fatal(err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)
	whole := encodeJPEG(t, testImage(400, 400))
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"garbage", []byte("not an image"), errPhotoNotImage},
		{"empty", nil, errPhotoNotImage},
		{"tiff", tif, errPhotoNotImage},
		{"svg", svg, errPhotoNotImage},
		{"truncated jpeg", whole[:len(whole)/2], errPhotoBroken},
	} {
		_, _, err := processPhoto(context.Background(), tc.data)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// pngWithSize is a 1x1 PNG of img's colour type whose header declares w x
// h, as a small file can ("decompression bomb"), interlaced or not.
func pngWithSize(t *testing.T, img image.Image, w, h uint32, interlaced bool) []byte {
	t.Helper()
	b := encodePNG(t, img)
	// Patch width, height and interlace in the IHDR chunk and fix its CRC.
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	if interlaced {
		b[28] = 1
	}
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}

// gifWithSize is a 1x1 GIF whose screen and frame declare w x h.
func gifWithSize(t *testing.T, w, h uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	binary.LittleEndian.PutUint16(b[6:], w)
	binary.LittleEndian.PutUint16(b[8:], h)
	i := 13 + 6 + bytes.IndexByte(b[13+6:], 0x2C) // the image descriptor, after the 2-colour palette
	binary.LittleEndian.PutUint16(b[i+5:], w)
	binary.LittleEndian.PutUint16(b[i+7:], h)
	return b
}

// A huge canvas is refused from its header, before anything is decoded:
// over 100 MP for any format, and for decoders that hold the whole image
// (interlaced PNG, GIF), over the decode budget.
func TestProcessPhotoRefusesDecompressionBombs(t *testing.T) {
	needVips(t)
	gray := image.NewGray(image.Rect(0, 0, 1, 1))
	rgba := image.NewNRGBA(image.Rect(0, 0, 1, 1)) // transparent: an RGBA PNG
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"400 MP", pngWithSize(t, gray, 20000, 20000, false), errPhotoTooBig},
		{"just over 100 MP", pngWithSize(t, gray, 10001, 10000, false), errPhotoTooBig},
		// Decoded (as it streams) and found truncated.
		{"exactly 100 MP", pngWithSize(t, gray, 10000, 10000, false), errPhotoBroken},
		{"interlaced 81 MP RGBA", pngWithSize(t, rgba, 9000, 9000, true), errPhotoTooHeavy},
		{"interlaced 81 MP grey", pngWithSize(t, gray, 9000, 9000, true), errPhotoBroken},
		{"72 MP gif", gifWithSize(t, 9000, 8000), errPhotoTooHeavy},
		// Within the budget: decoded (libnsgif pads the short frame).
		{"40 MP gif", gifWithSize(t, 8000, 5000), nil},
	} {
		start := time.Now()
		_, _, err := processPhoto(context.Background(), tc.data)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("%s: %v in %v", tc.name, err, time.Since(start).Round(time.Millisecond))
	}
}

func TestParsePhotoHeader(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want photoHeader
		ok   bool
	}{
		{"/tmp/pitbox-photo-1: 4032x3024 uchar, 3 bands, srgb, jpegload\n", photoHeader{4032, 3024, 3, "uchar", "jpegload"}, true},
		{"/tmp/x: 10x20 ushort, 1 band, grey16, pngload", photoHeader{10, 20, 1, "ushort", "pngload"}, true},
		{"/tmp/x: 10x20 uchar, 3 bands, srgb", photoHeader{}, false},
		{"/tmp/x: axb uchar, 3 bands, srgb, pngload", photoHeader{}, false},
		{"", photoHeader{}, false},
	} {
		got, ok := parsePhotoHeader(tc.out)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parsePhotoHeader(%q) = %+v, %v; want %+v, %v", tc.out, got, ok, tc.want, tc.ok)
		}
	}
}

// swap sets the package variable *p to v until the test ends, even when
// it fails half way.
func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// holdPhotoSlot takes the photo slot, as a photo being processed would.
// release gives it back; the test's end does, if it didn't.
func holdPhotoSlot(t *testing.T) (release func()) {
	t.Helper()
	photoSlot <- struct{}{}
	var once sync.Once
	release = func() { once.Do(func() { <-photoSlot }) }
	t.Cleanup(release)
	return release
}

// fakeVips makes vips a shell script with body for the test.
func fakeVips(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vips")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	swap(t, &vipsBin, path)
}

// When vips fails, the user gets a plain message, and what vips said
// stays in the log (the error).
func TestProcessPhotoVipsFails(t *testing.T) {
	needVips(t)
	data := encodePNG(t, testImage(40, 30))
	for _, tc := range []struct {
		script string
		want   error
	}{
		{`echo "VipsForeignLoad: raw detail" >&2; exit 1`, errPhotoBroken},
		{`echo "vips_tracked: out of memory --- size == 381MB" >&2; exit 1`, errPhotoTooHeavy},
		{`echo "VipsJpeg: Backing store not supported" >&2; exit 1`, errPhotoTooHeavy},
		// Thousands of tile warnings first: the reason at the end counts.
		{`i=0; while [ $i -lt 2000 ]; do echo "(vips:1): VIPS-WARNING **: error in tile 0 x $i" >&2; i=$((i+1)); done; echo "VipsJpeg: Backing store not supported" >&2; exit 1`, errPhotoTooHeavy},
		{`kill -SEGV $$`, errPhotoBroken},
	} {
		fakeVips(t, tc.script)
		_, _, err := processPhoto(context.Background(), data)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.script, err, tc.want)
			continue
		}
		if msg := photoError(err); strings.Contains(msg, "Vips") || strings.Contains(msg, "vips_") {
			t.Errorf("the user is shown what vips said: %q", msg)
		}
		if errors.Is(err, errPhotoBroken) && !strings.Contains(err.Error(), "raw detail") && !strings.Contains(err.Error(), "signal") {
			t.Errorf("the error lost what vips said: %v", err)
		}
	}
}

// A vips that hangs is stopped at the timeout, which is reported as such;
// a client that goes away stops it too.
func TestProcessPhotoTimeout(t *testing.T) {
	needVips(t)
	fakeVips(t, "exec sleep 20")
	swap(t, &photoTimeout, 200*time.Millisecond)
	data := encodePNG(t, testImage(40, 30))

	start := time.Now()
	_, _, err := processPhoto(context.Background(), data)
	if !errors.Is(err, errPhotoTimeout) {
		t.Errorf("err = %v, want the timeout", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v to give up", d)
	}
	if got := photoError(err); got != "Processing the photo took too long. Try a smaller photo." {
		t.Errorf("message = %q", got)
	}

	photoTimeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, _, err := processPhoto(ctx, data); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Without libvips installed, uploads are refused with a clear message,
// and the startup check says why.
func TestProcessPhotoVipsMissing(t *testing.T) {
	swap(t, &vipsBin, "pitbox-no-such-vips")
	_, _, err := processPhoto(context.Background(), encodePNG(t, testImage(4, 4)))
	if !errors.Is(err, errVipsMissing) {
		t.Fatalf("err = %v, want errVipsMissing", err)
	}
	if msg := photoError(err); !strings.Contains(msg, "libvips") {
		t.Errorf("message = %q", msg)
	}
	if _, err := PhotoTools(context.Background()); !errors.Is(err, errVipsMissing) {
		t.Errorf("PhotoTools: %v", err)
	}
}

func TestPhotoTools(t *testing.T) {
	needVips(t)
	v, err := PhotoTools(context.Background())
	if err != nil || !strings.HasPrefix(v, "vips-") {
		t.Errorf("PhotoTools = %q, %v", v, err)
	}
}

// Only one photo is processed at a time; the next waits for its turn, and
// stops waiting when its client goes away.
func TestPhotoProcessedOneAtATime(t *testing.T) {
	needVips(t)
	data := encodePNG(t, testImage(40, 30))
	process := func(ctx context.Context) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, _, err := processPhotoQueued(ctx, data)
			done <- err
		}()
		return done
	}
	waiting := func(done <-chan error) {
		t.Helper()
		select {
		case err := <-done:
			t.Fatalf("processed while another photo held the slot (err = %v)", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	finished := func(done <-chan error) error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("still waiting")
			return nil
		}
	}

	release := holdPhotoSlot(t) // another upload is being processed
	done := process(context.Background())
	waiting(done)
	release()
	if err := finished(done); err != nil {
		t.Fatal(err)
	}
	if len(photoSlot) != 0 {
		t.Fatal("a finished photo kept the slot")
	}

	holdPhotoSlot(t)
	ctx, cancel := context.WithCancel(context.Background())
	done = process(ctx)
	waiting(done)
	cancel()
	if err := finished(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(photoSlot) != 1 {
		t.Error("a cancelled wait freed the slot it never had")
	}
}

// The memory a photo takes stays bounded however large and awkward it is:
// the vips child's peak RSS for the worst inputs, and what the app itself
// allocates. The inputs are made with vips, zoomed up from a small image
// (smooth, so they stay near upload size; the decoders' memory doesn't
// depend on the content).
func TestPhotoMemory(t *testing.T) {
	needVips(t)
	if testing.Short() {
		t.Skip("makes 50-100 MP images")
	}
	dir := t.TempDir()
	vipsMake(t, dir, "sines", "s.v", "1224", "816", "--hfreq", "3", "--vfreq", "2")
	vipsMake(t, dir, "linear", "s.v", "g.v", "100", "128", "--uchar")
	vipsMake(t, dir, "bandjoin", "g.v g.v g.v", "rgb0.v")
	vipsMake(t, dir, "copy", "rgb0.v", "rgb.v", "--interpretation", "srgb")
	vipsMake(t, dir, "bandjoin", "g.v g.v g.v g.v", "cmyk0.v")
	vipsMake(t, dir, "copy", "cmyk0.v", "cmyk.v", "--interpretation", "cmyk")
	vipsMake(t, dir, "cast", "g.v", "g16a.v", "ushort")
	vipsMake(t, dir, "linear", "g16a.v", "g16b.v", "257", "0")
	vipsMake(t, dir, "cast", "g16b.v", "g16.v", "ushort")
	vipsMake(t, dir, "bandjoin", "g16.v g16.v g16.v g16.v", "rgba16a.v")
	vipsMake(t, dir, "copy", "rgba16a.v", "rgba16.v", "--interpretation", "rgb16")
	// zoom x10 gives 12240x8160 (99.9 MP); x7 gives 8568x5712 (48.9 MP).
	inputs := []struct {
		name, file string
		args       []string
		want       error // nil: processed
		ceiling    int64 // MB of peak RSS
	}{
		{"50 MP progressive CMYK JPEG", "cmyk50.jpg", []string{"zoom", "cmyk.v", "cmyk50.jpg[Q=90,interlace]", "7", "7"}, errPhotoTooHeavy, 200},
		{"100 MP progressive CMYK JPEG", "cmyk100.jpg", []string{"zoom", "cmyk.v", "cmyk100.jpg[Q=90,interlace]", "10", "10"}, errPhotoTooHeavy, 200},
		{"100 MP baseline JPEG", "rgb100.jpg", []string{"zoom", "rgb.v", "rgb100.jpg[Q=90]", "10", "10"}, nil, 200},
		{"100 MP baseline CMYK JPEG", "cmykb100.jpg", []string{"zoom", "cmyk.v", "cmykb100.jpg[Q=90]", "10", "10"}, nil, 200},
		{"50 MP progressive JPEG 4:2:0", "p420.jpg", []string{"zoom", "rgb.v", "p420.jpg[Q=90,interlace,subsample-mode=on]", "7", "7"}, nil, 250},
		{"100 MP 16-bit RGBA PNG", "rgba16.png", []string{"zoom", "rgba16.v", "rgba16.png[compression=6]", "10", "10"}, nil, 200},
		{"50 MP interlaced 16-bit PNG", "rgba16i.png", []string{"zoom", "rgba16.v", "rgba16i.png[compression=6,interlace]", "7", "7"}, errPhotoTooHeavy, 200},
		{"50 MP WebP", "rgb50.webp", []string{"zoom", "rgb.v", "rgb50.webp[Q=80]", "7", "7"}, nil, 200},
	}
	var peak int64
	swap(t, &vipsExited, func(ps *os.ProcessState) {
		if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
			peak = max(peak, ru.Maxrss<<10) // KB on Linux
		}
	})
	for _, in := range inputs {
		vipsMake(t, dir, in.args...)
		data, err := os.ReadFile(filepath.Join(dir, in.file))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(filepath.Join(dir, in.file))

		// A child started from Go shares the app's memory until it execs,
		// so its Maxrss counts the app's peak RSS too: make that as small
		// as it goes (release freed memory, reset the high-water mark), and
		// report it next to the child's.
		peak = 0
		debug.FreeOSMemory()
		_ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		full, _, err := processPhoto(context.Background(), data)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, in.want) {
			t.Errorf("%s: err = %v, want %v", in.name, err, in.want)
		}
		if in.want == nil && err == nil {
			if b := decodeJPEG(t, full).Bounds(); max(b.Dx(), b.Dy()) != photoMaxSide {
				t.Errorf("%s: full = %v", in.name, b)
			}
		}
		allocated := int64(after.TotalAlloc - before.TotalAlloc)
		t.Logf("%-29s %5.1f MB file: vips peak RSS %4d MB (app's own peak %3d MB), app allocated %4.1f MB, heap %+5.1f MB, %v",
			in.name, float64(len(data))/1e6, peak>>20, appPeakRSS()>>20, float64(allocated)/(1<<20),
			float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20), took.Round(10*time.Millisecond))
		if peak>>20 > in.ceiling {
			t.Errorf("%s: vips peak RSS %d MB, over %d MB", in.name, peak>>20, in.ceiling)
		}
		// The app holds the upload and the results, and copies the upload
		// to a temporary file and a pipe: nothing in proportion to the pixels.
		if allocated > int64(len(data))+16<<20 {
			t.Errorf("%s: the app allocated %d MB for a %d MB file", in.name, allocated>>20, len(data)>>20)
		}
	}
}

// appPeakRSS is this process's peak RSS (VmHWM), or 0 where /proc isn't.
func appPeakRSS() int64 {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return kb << 10
		}
	}
	return 0
}
