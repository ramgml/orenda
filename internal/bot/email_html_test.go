package bot

import (
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// splitMIMEParts parses a multipart/alternative body and returns the
// two parts as raw text bodies. Sufficient for assertions: not a
// general-purpose MIME parser.
func splitMIMEParts(t *testing.T, raw string) (header, plain, htmlBody string) {
	t.Helper()
	headersEnd := strings.Index(raw, "\r\n\r\n")
	require.True(t, headersEnd > 0, "expected headers end")
	header = raw[:headersEnd]
	body := raw[headersEnd+4:]

	start := strings.Index(header, "boundary=\"")
	require.True(t, start > 0, "expected boundary in headers")
	start += len("boundary=\"")
	end := strings.Index(header[start:], "\"")
	boundary := header[start : start+end]

	parts := strings.Split(body, "--"+boundary)
	require.GreaterOrEqual(t, len(parts), 3, "expected >=2 parts plus epilogue")
	plainPart := parts[1]
	htmlPart := parts[2]

	plain = stripPartHeaders(t, plainPart)
	htmlBody = stripPartHeaders(t, htmlPart)
	return header, plain, htmlBody
}

func stripPartHeaders(t *testing.T, part string) string {
	t.Helper()
	end := strings.Index(part, "\r\n\r\n")
	require.True(t, end > 0, "part missing blank line")
	if len(part) >= end+6 && part[len(part)-2:] == "\r\n" {
		return part[end+4 : len(part)-2]
	}
	return part[end+4:]
}

// TestRenderPlain_BasicTitleAndBody pins the canonical text rendering
// without any layout or actions.
func TestRenderPlain_BasicTitleAndBody(t *testing.T) {
	s := renderPlain(Message{
		Title: "Test task",
		Body:  "Line 1\nLine 2",
		Link:  "https://orenda.example.com/inbox",
	})
	assert.Contains(t, s, "Test task")
	assert.Contains(t, s, "Line 1\nLine 2")
	assert.Contains(t, s, "https://orenda.example.com/inbox")
}

// TestRenderPlain_URLActionLabelsAsPlainLine: pre-built URL actions
// render as labelled lines so plain-only clients can still copy/paste.
func TestRenderPlain_URLActionLabelsAsPlainLine(t *testing.T) {
	s := renderPlain(Message{
		Title: "Click me",
		Actions: []Action{
			{Label: "Open", URL: "https://example.com/x"},
		},
	})
	assert.Contains(t, s, "Open: https://example.com/x")
}

// TestRenderPlain_CallbackActionSurfacesIntent: callback-style
// actions (verb + id, no URL) surface the verb and id so the reader
// knows what would happen.
func TestRenderPlain_CallbackActionSurfacesIntent(t *testing.T) {
	s := renderPlain(Message{
		Title:      "Review",
		CallbackID: "task-1",
		Actions: []Action{
			{Label: "✅ Approve", Callback: "approve"},
		},
	})
	assert.Contains(t, s, "[✅ Approve]")
	assert.Contains(t, s, "task-1")
}

// TestRenderHTML_BrandingAndStructuredLayout pins the HTML shape:
// DOCTYPE header, brand bar, heading, body, optional link, footer.
func TestRenderHTML_BrandingAndStructuredLayout(t *testing.T) {
	html := renderHTML(Message{
		Title: "Test task",
		Body:  "Body here.",
		Link:  "https://orenda.example.com/inbox",
	}, "")
	assert.Contains(t, html, "<!DOCTYPE html>")
	assert.Contains(t, html, "Orenda")
	assert.Contains(t, html, "<h1")
	assert.Contains(t, html, "Test task")
	assert.Contains(t, html, "<p")
	assert.Contains(t, html, `href="https://orenda.example.com/inbox"`)
}

// TestRenderHTML_NewlinesBecameBR: the multi-line body uses <br/>
// so recipients see line breaks. Pure markdown would be overkill —
// notifier's body rarely goes beyond 5 lines.
func TestRenderHTML_NewlinesBecameBR(t *testing.T) {
	html := renderHTML(Message{
		Title: "Multi-line",
		Body:  "Line 1\nLine 2\nLine 3",
	}, "")
	assert.Contains(t, html, "Line 1<br/>Line 2<br/>Line 3")
}

// TestRenderHTML_EscapesUserSuppliedHTML: any HTML-like characters
// in the title or body must be escaped to prevent script injection
// via the title field.
func TestRenderHTML_EscapesUserSuppliedHTML(t *testing.T) {
	html := renderHTML(Message{
		Title: "<script>alert(1)</script>",
		Body:  "Hello & welcome < there",
	}, "")
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, "&lt;script&gt;")
	assert.Contains(t, html, "&amp;")
	assert.Contains(t, html, "&lt;")
}

// TestRenderHTML_NoActionButtonsWhenBaseURLEmpty: when
// PublicBaseURL is the empty string (dev default), actions are not
// rendered as buttons because the URL can't be wired. Plain part
// surfaces the action verb so the user still gets intent.
func TestRenderHTML_NoActionButtonsWhenBaseURLEmpty(t *testing.T) {
	html := renderHTML(Message{
		Title: "Review",
		Actions: []Action{
			{Label: "✅ Approve", Callback: "approve"},
			{Label: "↩️ Reject", Callback: "reject"},
		},
		CallbackID: "task-abc",
	}, "")
	assert.NotContains(t, html, "<a ")
	assert.NotContains(t, html, "/api/v1/tasks/task-abc/review")
}

// TestRenderHTML_ActionButtonsUseBaseURL: with a public base URL
// set, callback actions render as anchor buttons linking into the
// review endpoint with the verb.
func TestRenderHTML_ActionButtonsUseBaseURL(t *testing.T) {
	html := renderHTML(Message{
		Title: "Review",
		Actions: []Action{
			{Label: "✅ Approve", Callback: "approve"},
		},
		CallbackID: "task-abc",
	}, "https://orenda.example.com")
	assert.Contains(t, html, `href="https://orenda.example.com/api/v1/tasks/task-abc/review?action=approve"`)
	assert.Contains(t, html, "✅ Approve")
}

// TestRenderHTML_PreBuiltURLActionBypassesCallbackID: if an action
// carries its own URL (e.g. a login deep-link), we use it verbatim
// and ignore CallbackID.
func TestRenderHTML_PreBuiltURLActionBypassesCallbackID(t *testing.T) {
	html := renderHTML(Message{
		Title: "Re-login required",
		Actions: []Action{
			{Label: "Open", URL: "https://orenda.example.com/login?from=hook"},
		},
	}, "https://orenda.example.com")
	assert.Contains(t, html, `href="https://orenda.example.com/login?from=hook"`)
	assert.Contains(t, html, "Open")
}

// TestRenderHTML_TrimsTrailingSlashOnBaseURL: base URLs with trailing
// slashes don't produce double-slashed action URLs.
func TestRenderHTML_TrimsTrailingSlashOnBaseURL(t *testing.T) {
	html := renderHTML(Message{
		Title: "Review",
		Actions: []Action{
			{Label: "✅ Approve", Callback: "approve"},
		},
		CallbackID: "task-abc",
	}, "https://orenda.example.com/")
	assert.Contains(t, html, `href="https://orenda.example.com/api/v1/tasks/task-abc/review?action=approve"`)
	assert.NotContains(t, html, "example.com//api")
}

// TestBuildMultipartAlternative_Headers pins the MIME envelope shape.
// Phase 30.4 wired this so email clients receive a real
// multipart/alternative body (rather than a single text/plain body
// pretending to be HTML, which Gmail quietly strips).
func TestBuildMultipartAlternative_Headers(t *testing.T) {
	body := buildMultipartAlternative("from@x", "to@x", "Hi", "plain-body", "<p>html-body</p>")

	header, plain, htmlBody := splitMIMEParts(t, body)
	parsed := textproto.MIMEHeader{}
	for _, line := range strings.Split(header, "\r\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			parsed.Add(k, strings.TrimSpace(v))
		}
	}
	assert.Equal(t, "1.0", parsed.Get("MIME-Version"))
	ct := parsed.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "multipart/alternative;"))
	assert.Contains(t, ct, "boundary=")

	// Subject + From + To are duplicated: once at the top-level
	// envelope (good practice) and … we just check once each.
	assert.Contains(t, header, "Subject: Hi")
	assert.Contains(t, header, "From: from@x")
	assert.Contains(t, header, "To: to@x")

	// Both parts present (with optional trailing CRLF stripped).
	assert.Equal(t, "plain-body", strings.TrimRight(plain, "\r\n"))
	assert.Equal(t, "<p>html-body</p>", strings.TrimRight(htmlBody, "\r\n"))
}

// TestBuildMultipartAlternative_BoundaryIsUniqueAcrossCalls ensures
// that two consecutive Send invocations produce boundaries that
// don't collide (otherwise some clients could mash the parts
// together).
func TestBuildMultipartAlternative_BoundaryIsUniqueAcrossCalls(t *testing.T) {
	b1 := buildMultipartAlternative("a", "b", "s", "x", "y")
	b2 := buildMultipartAlternative("a", "b", "s", "x", "y")
	b1Marker := extractBoundary(t, b1)
	b2Marker := extractBoundary(t, b2)
	assert.NotEqual(t, b1Marker, b2Marker, "boundary should differ between Send calls")
}

func extractBoundary(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "boundary=\"")
	require.True(t, start > 0)
	start += len("boundary=\"")
	end := strings.Index(body[start:], "\"")
	return body[start : start+end]
}

// TestEnd2End_FullEmailBody covers the whole pipeline:
// renderPlain + renderHTML + buildMultipartAlternative in one go.
// Verifies the full multipart payload has both parts and uses
// boundary correctly.
func TestEnd2End_FullEmailBody(t *testing.T) {
	body := buildMultipartAlternative("from@x.com", "to@x.com", "Review needed",
		renderPlain(Message{Title: "Review needed", Body: "Task Foo awaits review."}),
		renderHTML(Message{
			Title: "Review needed", Body: "Task Foo awaits review.",
		}, "https://orenda.example.com"),
	)

	header, plain, html := splitMIMEParts(t, body)
	assert.Contains(t, header, "multipart/alternative")
	assert.Contains(t, plain, "Review needed")
	assert.Contains(t, plain, "Task Foo awaits review.")
	assert.Contains(t, html, "<!DOCTYPE html>")
	assert.Contains(t, html, "Task Foo awaits review.")
}
