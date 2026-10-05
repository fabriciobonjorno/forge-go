package views

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRendererEscapesHTMLAndSetsResponse(t *testing.T) {
	t.Parallel()
	renderer, err := New(fstest.MapFS{
		"welcome.gohtml": {Data: []byte("<h1>{{.Title}}</h1>")},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, "welcome.gohtml", http.StatusCreated, struct{ Title string }{"<script>alert(1)</script>"}); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), "&lt;script&gt;") {
		t.Fatalf("template data was not contextually escaped: %s", response.Body.String())
	}
}

func TestRendererDoesNotWritePartialResponseOnExecutionError(t *testing.T) {
	t.Parallel()
	renderer, err := New(fstest.MapFS{
		"broken.gohtml": {Data: []byte("prefix {{.Missing.Value}}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, "broken.gohtml", http.StatusOK, struct{}{}); err == nil {
		t.Fatal("expected template execution error")
	}
	if response.Body.Len() != 0 || response.Code != http.StatusOK {
		t.Fatalf("partial response written: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRendererRejectsInvalidStatusBeforeWriting(t *testing.T) {
	t.Parallel()
	renderer, err := New(fstest.MapFS{
		"page.gohtml": {Data: []byte("<p>hello</p>")},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, "page.gohtml", 99, nil); err == nil {
		t.Fatal("expected invalid status error")
	}
	if response.Body.Len() != 0 {
		t.Fatalf("invalid status wrote body: %q", response.Body.String())
	}
}

func TestNewRejectsMissingTemplates(t *testing.T) {
	t.Parallel()
	for _, filesystem := range []struct {
		name string
		fs   fstest.MapFS
	}{
		{name: "empty filesystem", fs: fstest.MapFS{}},
		{name: "wrong extension", fs: fstest.MapFS{"page.html": {Data: []byte("<p>hello</p>")}}},
	} {
		t.Run(filesystem.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(filesystem.fs); err == nil {
				t.Fatal("expected missing-template error")
			}
		})
	}
}
