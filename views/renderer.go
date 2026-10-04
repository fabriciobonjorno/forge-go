// Package views renders server-side HTML templates with contextual escaping.
package views

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
)

// Renderer is an immutable, concurrency-safe collection of HTML templates.
type Renderer struct {
	templates *template.Template
}

// New parses every *.gohtml file in templates. Template functions and
// template names remain explicit in application code; no global registry is
// modified.
func New(templates fs.FS) (*Renderer, error) {
	if templates == nil {
		return nil, errors.New("views: template filesystem is required")
	}
	parsed, err := template.ParseFS(templates, "*.gohtml")
	if err != nil {
		return nil, fmt.Errorf("views: parse templates: %w", err)
	}
	if len(parsed.Templates()) == 0 {
		return nil, errors.New("views: no templates found; expected *.gohtml")
	}
	return &Renderer{templates: parsed}, nil
}

// Render executes name before writing any response bytes, so template errors
// cannot leave a partial HTML response. html/template applies contextual
// escaping to all data values. A non-positive status defaults to 200.
func (r *Renderer) Render(w http.ResponseWriter, name string, status int, data any) error {
	if r == nil || r.templates == nil {
		return errors.New("views: renderer is required")
	}
	if name == "" {
		return errors.New("views: template name is required")
	}
	if status <= 0 {
		status = http.StatusOK
	}
	if status < 100 || status > 999 {
		return fmt.Errorf("views: invalid HTTP status %d", status)
	}
	var body bytes.Buffer
	if err := r.templates.ExecuteTemplate(&body, name, data); err != nil {
		return fmt.Errorf("views: execute %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		return fmt.Errorf("views: write response: %w", err)
	}
	return nil
}
