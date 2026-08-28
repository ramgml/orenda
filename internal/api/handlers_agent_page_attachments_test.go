package api_test

// T80: page attachments on the agent surface.
//
// Contract pinned here:
//
//   - POST /api/v1/agent/pages/{slug}/attachments (multipart `file`)
//     with an agent bearer token → 201, the file streams back through
//     the global /api/v1/attachments/{id}/download route.
//   - The attachment row carries uploaded_by_type=agent and
//     uploaded_by_id=<agent id> — attribution follows the identity,
//     not a hardcoded user.
//   - GET /api/v1/agent/pages/{slug}/attachments lists the page's
//     files so upload scripts can be idempotent by filename.
//   - Namespace split: the user session cookie is NOT an agent
//     credential on the agent routes (401), and the agent bearer
//     token is NOT a user credential on the user-side upload route
//     (401) — the Phase 27.11 pattern.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentUploadFile POSTs a multipart body with the agent bearer token
// and returns the recorder. The part always carries image/png — every
// fixture page upload in these tests is an image, matching the
// Obsidian-vault migration case; the handler reads the part header,
// not a sniffed type.
func (fx *agentWikiFixture) agentUploadFile(path, filename, body string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", "image/png")
	fw, _ := mw.CreatePart(hdr)
	_, _ = fw.Write([]byte(body))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

// agentUploadStatus drives the same upload with an arbitrary auth
// override (cookie-only requests drop the bearer header).
func agentUploadStatus(t *testing.T, fx *agentWikiFixture, path, cookie string) int {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="x.png"`)
	hdr.Set("Content-Type", "image/png")
	fw, _ := mw.CreatePart(hdr)
	_, _ = fw.Write([]byte("png-bytes"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	}
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr.Code
}

func TestAgentPageAttachments_HappyPath(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/att-guide", map[string]any{
		"title":      "Att Guide",
		"content_md": "Page that will carry an image.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create page: %s", rr.Body.String())

	// Upload a png as the agent.
	rr = fx.agentUploadFile("/api/v1/agent/pages/att-guide/attachments", "diagram.png", "png-bytes-here")
	require.Equal(t, http.StatusCreated, rr.Code, "upload: %s", rr.Body.String())
	var up struct {
		ID             string `json:"id"`
		TargetType     string `json:"target_type"`
		Filename       string `json:"filename"`
		UploadedByType string `json:"uploaded_by_type"`
		UploadedByID   string `json:"uploaded_by_id"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&up))
	assert.Equal(t, "diagram.png", up.Filename)
	assert.Equal(t, "page", up.TargetType)
	assert.Equal(t, "agent", up.UploadedByType, "attribution must follow the agent identity")
	assert.NotEmpty(t, up.UploadedByID, "agent id must be recorded")
	// The file streams back through the global download route. That
	// route is a USER surface (RequireUser): the browser renders the
	// wiki page and fetches images with the owner's cookie, so the
	// user session must read agent-uploaded bytes...
	req := httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+up.ID+"/download", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "download: %s", rr.Body.String())
	assert.Equal(t, "png-bytes-here", rr.Body.String())

	// ...while the agent bearer stays locked out of the user
	// namespace (Phase 27.11 split). The agent verifies its uploads
	// through the list endpoint below.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+up.ID+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code, "agent token on user download route must 401")

	// List: the upload is visible, and its filename enables the
	// idempotency check (skip already-uploaded files).
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/att-guide/attachments", nil)
	require.Equal(t, http.StatusOK, rr.Code, "list: %s", rr.Body.String())
	var list struct {
		Attachments []struct {
			ID       string `json:"id"`
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list.Attachments, 1)
	assert.Equal(t, up.ID, list.Attachments[0].ID)
	assert.Equal(t, "diagram.png", list.Attachments[0].Filename)

	// W-ref resolution: the same upload against "W1" resolves the page.
	rr = fx.agentUploadFile("/api/v1/agent/pages/W1/attachments", "second.png", "more-png")
	require.Equal(t, http.StatusCreated, rr.Code, "upload by W-ref: %s", rr.Body.String())
}

func TestAgentPageAttachments_NamespaceSplit(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/split-page", map[string]any{
		"title":      "Split",
		"content_md": "x",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	// User cookie on the agent upload route → 401.
	assert.Equal(t, http.StatusUnauthorized,
		agentUploadStatus(t, fx, "/api/v1/agent/pages/split-page/attachments", fx.cookie),
		"cookie must not authenticate the agent namespace")
	// User cookie on the agent list route → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/pages/split-page/attachments", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code, "cookie must not authenticate the agent namespace")

	// Agent bearer token on the user-side upload route → 401
	// (RequireUser accepts cookie/JWT only).
	assert.Equal(t, http.StatusUnauthorized,
		agentUploadStatus(t, fx, "/api/v1/pages/split-page/attachments", ""),
		"agent token must not authenticate the user namespace")

	// No credentials at all → 401.
	assert.Equal(t, http.StatusUnauthorized,
		agentUploadStatus(t, fx, "/api/v1/agent/pages/split-page/attachments", ""),
		"anonymous upload must 401")
}

func TestAgentPageAttachments_ReuploadSameFilename(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/idem-page", map[string]any{
		"title":      "Idem",
		"content_md": "x",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	// First upload creates, second upload of the same bytes hits the
	// sha256 dedup and returns the existing row with the duplicate
	// marker — a migration script can rely on filename from the list
	// endpoint (or the marker) to stay idempotent.
	rr = fx.agentUploadFile("/api/v1/agent/pages/idem-page/attachments", "shot.png", "same-bytes")
	require.Equal(t, http.StatusCreated, rr.Code, "first: %s", rr.Body.String())
	var first struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&first))

	rr = fx.agentUploadFile("/api/v1/agent/pages/idem-page/attachments", "shot.png", "same-bytes")
	require.Equal(t, http.StatusCreated, rr.Code, "second: %s", rr.Body.String())
	assert.Equal(t, "true", rr.Header().Get("X-Attachment-Duplicate"), "dedup must flag the replay")
	var second struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&second))
	assert.Equal(t, first.ID, second.ID, "dedup returns the original row")
}
