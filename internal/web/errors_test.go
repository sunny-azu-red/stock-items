package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMalformedFormsAre400(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	for _, tt := range []struct{ path, contentType, body string }{
		{"/vehicles", "application/x-www-form-urlencoded", "name=%zz"},
		{"/vehicles/" + itoa(k390), "application/x-www-form-urlencoded", "name=a&notes=%"},
		{"/items", "application/x-www-form-urlencoded", "name=%G0"},
		{"/shopping", ";;;", "free_text=Oil"},
	} {
		req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
		req.Header.Set("Content-Type", tt.contentType)
		expect(t, a.do(req), http.StatusBadRequest, "could not be read")
	}
	// Too large is still 413.
	TestOversizedFormIs413(t)
}
