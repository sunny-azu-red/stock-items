package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Photos are shrunk by the libvips command line tools (libvips-tools) in a
// child process, not decoded in Go. libvips shrinks while it loads (JPEG
// is decoded at 1/2, 1/4 or 1/8 straight away, PNG and WebP are streamed),
// so its memory follows the output size, not the photo's: about 50-150 MB
// for a 100 MP JPEG or PNG. Running it as a subprocess keeps the binary
// pure Go (CGO_ENABLED=0), and whatever the decoders do, they do it in a
// process of their own with its own memory limit.

const (
	photoMaxSide = 1600
	thumbSide    = 320
	// photoMaxPixels is a sanity limit, read from the header before
	// anything is decoded. A small file can declare a huge image
	// ("decompression bomb").
	photoMaxPixels = 100_000_000
	// photoDecodeBudget is what a decoder that can't shrink while it loads
	// may hold at once: libjpeg's buffer for a progressive JPEG (JPEGMEM),
	// and the whole image for HEIC, GIF and interlaced PNG (estimated from
	// the header). 48 MP HEIC from an iPhone fits.
	photoDecodeBudget = 256 << 20
	// photoMemoryLimit is the vips child's hard data limit (RLIMIT_DATA on
	// Linux), in case a decoder needs more than anyone expected.
	photoMemoryLimit = 512 << 20
)

var (
	// vipsBin and vipsheaderBin are looked up on PATH at every upload, so
	// installing libvips-tools takes effect without a restart.
	vipsBin       = "vips"
	vipsheaderBin = "vipsheader"
	// photoTimeout bounds one photo's processing, after its turn came.
	photoTimeout = 30 * time.Second
	// vipsExited, when set (tests), sees every vips child that finished.
	vipsExited func(*os.ProcessState)
	// limitChild sets a vips child's memory limit (tests make it fail).
	limitChild = limitMemory
)

// The errors processPhoto returns wrap one of these, with what vips said.
var (
	errPhotoTooBig   = errors.New("image has too many pixels")
	errPhotoTooHeavy = errors.New("image needs too much memory to decode")
	errPhotoNotImage = errors.New("not a JPEG, PNG, GIF, WebP or HEIC image")
	errPhotoBroken   = errors.New("image could not be processed")
	errPhotoTimeout  = errors.New("image processing took too long")
	errVipsMissing   = errors.New("libvips-tools (vips, vipsheader) is not installed")
)

// photoLoaders are the libvips loaders an upload may use. libvips knows
// many more formats (TIFF, PDF, SVG...); VIPS_BLOCK_UNTRUSTED blocks the
// riskiest, and this list the rest.
var photoLoaders = map[string]bool{"jpegload": true, "pngload": true, "gifload": true, "webpload": true, "heifload": true}

// photoSlot lets one photo be processed at a time, so a few uploads at
// once can't add up. One at a time is plenty for a garage.
var photoSlot = make(chan struct{}, 1)

// photoQueue has a place for each upload from before its body is read
// until it is answered: waiting for photoSlot, each holds its photo (up
// to 15 MB) in memory, so beyond a few the next is told to try again.
var photoQueue = make(chan struct{}, 4)

// photoPlace takes a place in photoQueue, to give back with done; ok is
// false when there is none.
func photoPlace() (done func(), ok bool) {
	select {
	case photoQueue <- struct{}{}:
		return func() { <-photoQueue }, true
	default:
		return nil, false
	}
}

// processPhotoQueued is processPhoto once the photo slot is free. It gives
// up waiting when ctx ends (the client went away).
func processPhotoQueued(ctx context.Context, data []byte) (full, thumb []byte, err error) {
	select {
	case photoSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	defer func() { <-photoSlot }()
	return processPhoto(ctx, data)
}

// processPhoto turns an uploaded image into a JPEG of at most 1600 px a
// side and a 320 px square thumbnail of its centre, upright (EXIF
// orientation applied), in sRGB, on white where it was transparent, and
// without metadata. It returns ctx's error when ctx ends.
func processPhoto(ctx context.Context, data []byte) (full, thumb []byte, err error) {
	vips, err1 := exec.LookPath(vipsBin)
	header, err2 := exec.LookPath(vipsheaderBin)
	if err := errors.Join(err1, err2); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errVipsMissing, err)
	}
	tctx, cancel := context.WithTimeout(ctx, photoTimeout)
	defer cancel()
	defer func() {
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if tctx.Err() != nil {
			err = fmt.Errorf("%w (%s)", errPhotoTimeout, photoTimeout)
		}
	}()

	h, err := readPhotoHeader(tctx, header, data)
	if err != nil {
		return nil, nil, err
	}
	if h.width <= 0 || h.height <= 0 || int64(h.width)*int64(h.height) > photoMaxPixels {
		return nil, nil, fmt.Errorf("%w: %dx%d", errPhotoTooBig, h.width, h.height)
	}
	if n := h.decodeBytes(data); n > photoDecodeBudget {
		return nil, nil, fmt.Errorf("%w: %s %dx%d needs about %d MB", errPhotoTooHeavy, h.loader, h.width, h.height, n>>20)
	}

	size := strconv.Itoa(photoMaxSide)
	full, err = runVips(tctx, vips, data, "--vips-cache-max=0", "thumbnail_source", "[descriptor=0]",
		".jpg[Q=85,keep=none,background=255]", size, "--height", size, "--size", "down",
		// A damaged file is refused, not saved half grey. (thumbnail_source
		// ignores its own --fail-on in libvips 8.15; the loader option works.)
		"--export-profile", "srgb", "--option-string", "fail-on=truncated")
	if err != nil {
		return nil, nil, vipsFailed(err)
	}
	// The thumbnail comes from the small photo: it is upright and flat
	// already, and shrinking 1600 px is quicker than the original again.
	side := strconv.Itoa(thumbSide)
	thumb, err = runVips(tctx, vips, full, "--vips-cache-max=0", "thumbnail_source", "[descriptor=0]",
		".jpg[Q=80,keep=none]", side, "--height", side, "--crop", "centre")
	if err != nil {
		return nil, nil, vipsFailed(err)
	}
	return full, thumb, nil
}

// vipsFailed says why shrinking failed: a decoder over its memory limit,
// or anything else (a damaged file).
func vipsFailed(err error) error {
	msg := err.Error()
	for _, s := range []string{"Backing store not supported", "out of memory", "failed to allocate"} {
		if strings.Contains(msg, s) {
			return fmt.Errorf("%w: %v", errPhotoTooHeavy, err)
		}
	}
	return fmt.Errorf("%w: %v", errPhotoBroken, err)
}

// photoHeader is what vipsheader says about an image without decoding it.
type photoHeader struct {
	width, height, bands int
	format, loader       string // "uchar", "jpegload"
}

// readPhotoHeader runs vipsheader on a temporary copy of data (it reads
// only files) and checks the format is one photos may use.
func readPhotoHeader(ctx context.Context, vipsheader string, data []byte) (photoHeader, error) {
	f, err := os.CreateTemp("", "pitbox-photo-*")
	if err != nil {
		return photoHeader{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err := errors.Join(err, f.Close()); err != nil {
		return photoHeader{}, err
	}
	out, err := runVips(ctx, vipsheader, nil, f.Name())
	if err != nil {
		return photoHeader{}, fmt.Errorf("%w: %v", errPhotoNotImage, err)
	}
	h, ok := parsePhotoHeader(string(out))
	if !ok {
		return photoHeader{}, fmt.Errorf("%w: unexpected vipsheader output %q", errPhotoNotImage, out)
	}
	if !photoLoaders[h.loader] {
		return photoHeader{}, fmt.Errorf("%w: %s", errPhotoNotImage, h.loader)
	}
	return h, nil
}

// parsePhotoHeader reads vipsheader's summary line, like
// "/tmp/x: 4032x3024 uchar, 3 bands, srgb, jpegload".
func parsePhotoHeader(out string) (photoHeader, bool) {
	line := strings.TrimSpace(out)
	if i := strings.LastIndex(line, ": "); i >= 0 {
		line = line[i+2:]
	}
	f := strings.Split(line, ", ")
	if len(f) != 4 {
		return photoHeader{}, false
	}
	var h photoHeader
	dims, format, ok := strings.Cut(f[0], " ")
	w, hgt, ok2 := strings.Cut(dims, "x")
	bands, _, ok3 := strings.Cut(f[1], " ")
	if !ok || !ok2 || !ok3 {
		return photoHeader{}, false
	}
	var err1, err2, err3 error
	h.width, err1 = strconv.Atoi(w)
	h.height, err2 = strconv.Atoi(hgt)
	h.bands, err3 = strconv.Atoi(bands)
	h.format, h.loader = format, f[3]
	return h, errors.Join(err1, err2, err3) == nil
}

// decodeBytes estimates the memory a decoder holds that can't shrink
// while it loads: libheif and GIF decode the whole image, and so does an
// interlaced PNG. A progressive JPEG's buffer is libjpeg's to limit
// (JPEGMEM), and the rest stream. The factors are measured peaks.
func (h photoHeader) decodeBytes(data []byte) int64 {
	px := int64(h.width) * int64(h.height)
	sample := int64(1)
	switch h.format {
	case "ushort", "short":
		sample = 2
	case "uint", "int", "float":
		sample = 4
	case "double", "complex":
		sample = 8
	}
	switch h.loader {
	case "heifload": // the decoded YCbCr planes and the RGB image
		return px * (int64(h.bands)*sample + 2)
	case "gifload": // the frame and its RGBA copy
		return px * 6
	case "pngload":
		// IHDR is the first chunk; its last byte is the interlace method.
		if len(data) > 28 && data[28] == 1 {
			return px * int64(h.bands) * sample
		}
	}
	return 0
}

// vipsError is a vips command that failed, with what it wrote to stderr.
type vipsError struct {
	err    error
	stderr string
}

func (e *vipsError) Error() string { return fmt.Sprintf("%v: %s", e.err, e.stderr) }
func (e *vipsError) Unwrap() error { return e.err }

// runVips runs one libvips command with stdin (nil for none) and returns
// its stdout. The child gets only the environment it needs: no untrusted
// loaders, one thread, libjpeg's memory limit, and none of the app's
// settings (DB_PASSWORD). The callers turn the operation cache off.
func runVips(ctx context.Context, bin string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{
		"VIPS_BLOCK_UNTRUSTED=1",
		"VIPS_CONCURRENCY=1",
		"JPEGMEM=" + strconv.Itoa(photoDecodeBudget/1000), // in thousands of bytes
		"MALLOC_ARENA_MAX=2",
	}
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "LD_LIBRARY_PATH"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.WaitDelay = time.Second
	var stdout bytes.Buffer
	stderr := &tail{max: 4 << 10}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	var in interface {
		Write([]byte) (int, error)
		Close() error
	}
	if stdin != nil {
		pipe, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		in = pipe
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Limited before it gets its input, so before it decodes anything.
	if err := limitChild(cmd.Process.Pid, photoMemoryLimit); err != nil {
		// It still runs, but nothing stops a decoder that wants more.
		slog.Warn("photo: could not limit the memory of vips", "bin", bin, "err", err)
	}
	if in != nil {
		go func() {
			_, _ = in.Write(stdin) // fails when vips gave up early; Wait says why
			_ = in.Close()
		}()
	}
	err := cmd.Wait()
	if vipsExited != nil {
		vipsExited(cmd.ProcessState)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, &vipsError{err: err, stderr: vipsMessage(stderr.String())}
	}
	return stdout.Bytes(), nil
}

// vipsMessage is what vips said without its per-tile warnings, which
// only repeat that decoding failed.
func vipsMessage(stderr string) string {
	var keep []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.Contains(line, "VIPS-WARNING") {
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return strings.TrimSpace(stderr)
	}
	return strings.Join(keep, "; ")
}

// tail keeps the last max bytes written to it: vips warns once per tile
// of a damaged image, and says why it failed last.
type tail struct {
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	if len(t.buf) > t.max {
		return string(t.buf[len(t.buf)-t.max:])
	}
	return string(t.buf)
}

// PhotoTools reports the libvips version photos are processed with, or
// why they can't be: a missing vips is a warning at startup, not a
// failure, and uploads are refused until it is installed.
func PhotoTools(ctx context.Context) (string, error) {
	vips, err1 := exec.LookPath(vipsBin)
	_, err2 := exec.LookPath(vipsheaderBin)
	if err := errors.Join(err1, err2); err != nil {
		return "", fmt.Errorf("%w: %v", errVipsMissing, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runVips(ctx, vips, nil, "--version")
	return strings.TrimSpace(string(out)), err
}
