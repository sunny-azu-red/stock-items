package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // register decoder
	"image/jpeg"
	_ "image/png" // register decoder

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register decoder
)

const (
	photoMaxSide = 1600
	thumbSide    = 320
	// photoMaxPixels bounds the memory a decode can take. A small file can
	// declare a huge image ("decompression bomb"). 50 MP still takes the
	// largest phone photos. Processing one at the limit takes about 270 MB
	// for an ordinary JPEG, up to about 1.4 GB for a progressive CMYK one.
	photoMaxPixels = 50_000_000
)

var errPhotoTooBig = errors.New("image has too many pixels")

// photoSlot lets one photo decode run at a time, so two or three uploads
// at once can't add up to several gigabytes and run the server out of
// memory. One at a time is plenty for a garage.
var photoSlot = make(chan struct{}, 1)

// processPhotoQueued is processPhoto once the photo slot is free. It gives
// up waiting when ctx ends (the client went away).
func processPhotoQueued(ctx context.Context, data []byte) (full, thumb []byte, err error) {
	select {
	case photoSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	defer func() { <-photoSlot }()
	return processPhoto(data)
}

// processPhoto decodes an uploaded image, applies the EXIF orientation
// (phone photos are often stored sideways), and returns a resized JPEG and
// a square JPEG thumbnail.
func processPhoto(data []byte) (full, thumb []byte, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > photoMaxPixels {
		return nil, nil, errPhotoTooBig
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	// Scale first so the (pixel by pixel) rotation works on a small image.
	img := orient(fit(src, photoMaxSide), exifOrientation(data))

	full, err = encodeJPEG(img, 85)
	if err != nil {
		return nil, nil, err
	}
	thumb, err = encodeJPEG(cover(img, thumbSide), 80)
	return full, thumb, err
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes(), err
}

// fit scales img down so its longest side is at most maxSide.
func fit(img image.Image, maxSide int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return flatten(img)
	}
	if w >= h {
		h = max(h*maxSide/w, 1)
		w = maxSide
	} else {
		w = max(w*maxSide/h, 1)
		h = maxSide
	}
	// Halve a large photo first, until it is under twice the target size:
	// CatmullRom's scratch buffer is 32 bytes × target width × source
	// height, about 360 MB straight from 50 MP. Bilinear at exactly half
	// (an odd last row or column is left out) is the average of each 2×2
	// block, so the halving doesn't alias, and CatmullRom makes the last
	// step.
	for b.Dx()/2 >= w && b.Dy()/2 >= h {
		even := image.Rect(b.Min.X, b.Min.Y, b.Min.X+b.Dx()&^1, b.Min.Y+b.Dy()&^1)
		img = scaleOnWhite(draw.ApproxBiLinear, img, even, b.Dx()/2, b.Dy()/2)
		b = img.Bounds()
	}
	return scaleOnWhite(draw.CatmullRom, img, b, w, h)
}

// cover crops the centre square of img and scales it to side x side.
func cover(img image.Image, side int) image.Image {
	b := img.Bounds()
	s := min(b.Dx(), b.Dy())
	x, y := b.Min.X+(b.Dx()-s)/2, b.Min.Y+(b.Dy()-s)/2
	return scaleOnWhite(draw.CatmullRom, img, image.Rect(x, y, x+s, y+s), side, side)
}

// scaleOnWhite scales the part sr of img to a new w x h image on white
// (JPEG has no transparency).
func scaleOnWhite(s draw.Scaler, img image.Image, sr image.Rectangle, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	s.Scale(dst, dst.Bounds(), img, sr, draw.Over, nil)
	return dst
}

// flatten draws img on white (JPEG has no transparency).
func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// orient applies an EXIF orientation value (1-8) to img.
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5
	dw, dh := w, h
	if swap {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 counter-clockwise
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// exifOrientation reads the orientation tag from a JPEG's EXIF block. It
// returns 1 (normal) when there is none.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		size := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if marker == 0xDA || size < 2 || i+2+size > len(data) { // start of scan: no more metadata
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off : off+2]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:e+2]) == 0x0112 {
			v := int(bo.Uint16(t[e+8 : e+10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
