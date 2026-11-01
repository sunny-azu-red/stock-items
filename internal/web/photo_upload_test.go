package web

import (
	"context"
	"image/color"
	"io"
	"net/http"
	"strings"
	"testing"
)

// readCounter counts the bytes read from a request body.
type readCounter struct {
	r io.Reader
	n int
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// fillPhotoQueue takes every place in photoQueue, as uploads in flight
// would, until the test ends.
func fillPhotoQueue(t *testing.T) {
	t.Helper()
	for range cap(photoQueue) {
		photoQueue <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(photoQueue) {
			<-photoQueue
		}
	})
}

// An upload that will be refused is answered before its body is read, so
// it neither waits for 15 MB to arrive nor holds them in memory.
func TestRefusedPhotoUploadsReadNothing(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil, pads := itemID(t, a.store, "Silkolene"), itemID(t, a.store, "Brake Pads")
	upload := func(item string) (*readCounter, Flash, string) {
		t.Helper()
		req := photoUpload(t, ctx, item, smallPNG(t, color.Black))
		body := &readCounter{r: req.Body}
		req.Body = io.NopCloser(body)
		rec := a.do(req)
		expect(t, rec, http.StatusSeeOther)
		return body, flashOf(t, rec), rec.Header().Get("Location")
	}

	// Archived: no new photo, and the page offers none.
	if _, err := a.store.ArchiveItem(ctx, oil, cur(t, a.store, "item", oil)); err != nil {
		t.Fatal(err)
	}
	expectNot(t, a.get(itemPath(oil)), `action="`+itemPath(oil)+`/photo"`)
	body, f, to := upload(itemPath(oil))
	if body.n != 0 || f.Kind != "error" || !strings.Contains(f.Message, "Restore") || to != itemPath(oil) {
		t.Errorf("archived: read %d bytes, flash %+v, to %q", body.n, f, to)
	}

	// Deleted in another tab: to the trash, as for any button.
	expect(t, a.postV(itemPath(pads)+"/delete", nil), http.StatusSeeOther)
	body, f, to = upload(itemPath(pads))
	if body.n != 0 || f.Kind != "error" || !strings.Contains(f.Message, "deleted") || to != "/trash" {
		t.Errorf("deleted: read %d bytes, flash %+v, to %q", body.n, f, to)
	}

	// Too many uploads in flight: try again.
	fillPhotoQueue(t)
	filter := itemPath(itemID(t, a.store, "Hiflofiltro"))
	body, f, to = upload(filter)
	if body.n != 0 || f.Kind != "error" || !strings.Contains(f.Message, "Try again") || to != filter {
		t.Errorf("busy: read %d bytes, flash %+v, to %q", body.n, f, to)
	}
	expect(t, a.get(filter+"/photo"), http.StatusNotFound)
}
