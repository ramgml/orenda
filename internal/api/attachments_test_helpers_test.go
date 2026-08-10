// Helpers shared with attachments_endpoint_test.go. Tiny module —
// keeps the regression test itself scannable.

package api_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramgml/orenda/internal/api"
)

func apiNewRouter(deps api.Dependencies) http.Handler {
	return api.NewRouter(deps)
}

// uploadOneAttachment drives the public POST /tasks/:id/attachments
// route and returns the HTTP status code. Anything other than 201
// is a setup failure — the parent test asserts on the value.
func uploadOneAttachment(t *testing.T, router http.Handler, cookie string, taskID, filename string, r io.Reader) int {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(fw, r); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/tasks/"+taskID+"/attachments",
		&buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr.Code
}
