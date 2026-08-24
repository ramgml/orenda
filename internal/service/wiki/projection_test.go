package wiki

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// block is a test helper that builds a wiki.Block with JSON-encoded Props/Content.
func block(typ string, props any, content any, children ...*wiki.Block) *wiki.Block {
	b := &wiki.Block{Type: typ, Children: children}
	if props != nil {
		b.Props, _ = json.Marshal(props)
	}
	if content != nil {
		b.Content, _ = json.Marshal(content)
	}
	return b
}

// inlineText returns a text inlineItem.
func inlineText(text string) inlineItem {
	return inlineItem{Type: "text", Text: text}
}

func inlineBold(text string) inlineItem {
	return inlineItem{Type: "text", Text: text, Styles: &textStyles{Bold: true}}
}

func inlineItalic(text string) inlineItem {
	return inlineItem{Type: "text", Text: text, Styles: &textStyles{Italic: true}}
}

func inlineStrike(text string) inlineItem {
	return inlineItem{Type: "text", Text: text, Styles: &textStyles{Strikethrough: true}}
}

func inlineCode(text string) inlineItem {
	return inlineItem{Type: "text", Text: text, Styles: &textStyles{Code: true}}
}

func inlineLink(text, href string) inlineItem {
	return inlineItem{Type: "link", Href: href, Content: []inlineItem{inlineText(text)}}
}

func inlineWikiLink(slug string) inlineItem {
	return inlineItem{Type: "wikiLink", Props: &inlineProps{Slug: slug}}
}

func TestBlocksToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []*wiki.Block
		expected string
	}{
		{
			name:     "empty tree",
			blocks:   nil,
			expected: "",
		},
		{
			name:     "single paragraph",
			blocks:   []*wiki.Block{block("paragraph", nil, []inlineItem{inlineText("Hello world")})},
			expected: "Hello world\n\n",
		},
		{
			name: "paragraph trailing newline stripped",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineText("A")}),
				block("paragraph", nil, []inlineItem{inlineText("B")}),
			},
			expected: "A\n\nB\n\n",
		},
		{
			name:     "heading level 1",
			blocks:   []*wiki.Block{block("heading", headingProps{Level: 1}, []inlineItem{inlineText("Title")})},
			expected: "# Title\n\n",
		},
		{
			name:     "heading level 3",
			blocks:   []*wiki.Block{block("heading", headingProps{Level: 3}, []inlineItem{inlineText("Sub")})},
			expected: "### Sub\n\n",
		},
		{
			name: "bullet list",
			blocks: []*wiki.Block{
				block("bulletListItem", nil, []inlineItem{inlineText("One")}),
				block("bulletListItem", nil, []inlineItem{inlineText("Two")}),
			},
			expected: "- One\n- Two\n",
		},
		{
			name: "numbered list",
			blocks: []*wiki.Block{
				block("numberedListItem", nil, []inlineItem{inlineText("First")}),
				block("numberedListItem", nil, []inlineItem{inlineText("Second")}),
			},
			expected: "1. First\n1. Second\n",
		},
		{
			name: "checkListItem unchecked",
			blocks: []*wiki.Block{
				block("checkListItem", checkListItemProps{Checked: false}, []inlineItem{inlineText("Todo")}),
			},
			expected: "- [ ] Todo\n",
		},
		{
			name: "checkListItem checked",
			blocks: []*wiki.Block{
				block("checkListItem", checkListItemProps{Checked: true}, []inlineItem{inlineText("Done")}),
			},
			expected: "- [x] Done\n",
		},
		{
			name: "nested bullet list 2 levels",
			blocks: []*wiki.Block{
				block("bulletListItem", nil, []inlineItem{inlineText("A")},
					block("bulletListItem", nil, []inlineItem{inlineText("B")},
						block("bulletListItem", nil, []inlineItem{inlineText("C")}),
					),
				),
			},
			expected: "- A\n  - B\n    - C\n",
		},
		{
			name: "nested numbered list with mixed children",
			blocks: []*wiki.Block{
				block("numberedListItem", nil, []inlineItem{inlineText("One")},
					block("bulletListItem", nil, []inlineItem{inlineText("Sub-bullet")}),
				),
			},
			expected: "1. One\n  - Sub-bullet\n",
		},
		{
			name: "quote",
			blocks: []*wiki.Block{
				block("quote", nil, []inlineItem{inlineText("Wise words")}),
			},
			expected: "> Wise words\n",
		},
		{
			name: "codeBlock with language",
			blocks: []*wiki.Block{
				block("codeBlock", codeBlockProps{Language: "go"}, "fmt.Println(\"hi\")"),
			},
			expected: "```go\nfmt.Println(\"hi\")\n```\n\n",
		},
		{
			name:     "codeBlock no language",
			blocks:   []*wiki.Block{block("codeBlock", nil, "x = 1")},
			expected: "```\nx = 1\n```\n\n",
		},
		{
			name:     "divider",
			blocks:   []*wiki.Block{block("divider", nil, nil)},
			expected: "---\n\n",
		},
		{
			name: "table with header and data",
			blocks: []*wiki.Block{
				block("table", nil, tableContent{
					Rows: []tableRow{
						{Cells: [][]inlineItem{
							{inlineText("Name")},
							{inlineText("Age")},
						}},
						{Cells: [][]inlineItem{
							{inlineText("Alice")},
							{inlineText("30")},
						}},
					},
				}),
			},
			expected: "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n\n",
		},
		{
			name: "table with pipe in cell escaped",
			blocks: []*wiki.Block{
				block("table", nil, tableContent{
					Rows: []tableRow{
						{Cells: [][]inlineItem{
							{inlineText("a|b")},
						}},
					},
				}),
			},
			expected: "| a\\|b |\n| --- |\n\n",
		},
		{
			name:     "table 0 rows skipped",
			blocks:   []*wiki.Block{block("table", nil, tableContent{})},
			expected: "",
		},
		{
			name: "image",
			blocks: []*wiki.Block{
				block("image", inlineProps{URL: "https://example.com/img.png", Caption: "A photo"}, nil),
			},
			expected: "![A photo](https://example.com/img.png)\n\n",
		},
		{
			name: "file",
			blocks: []*wiki.Block{
				block("file", inlineProps{URL: "https://example.com/doc.pdf", Name: "doc.pdf"}, nil),
			},
			expected: "[doc.pdf](https://example.com/doc.pdf)\n\n",
		},
		{
			name: "inline bold",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineText("normal "), inlineBold("bold")}),
			},
			expected: "normal **bold**\n\n",
		},
		{
			name: "inline italic",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineItalic("em")}),
			},
			expected: "*em*\n\n",
		},
		{
			name: "inline strikethrough",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineStrike("old")}),
			},
			expected: "~~old~~\n\n",
		},
		{
			name: "inline code",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineCode("x")}),
			},
			expected: "`x`\n\n",
		},
		{
			name: "inline link",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineLink("click", "https://example.com")}),
			},
			expected: "[click](https://example.com)\n\n",
		},
		{
			name: "wikiLink",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{inlineWikiLink("other-page")}),
			},
			expected: "[[other-page]]\n\n",
		},
		{
			name: "unknown type with inline content renders",
			blocks: []*wiki.Block{
				block("futureBlock", nil, []inlineItem{inlineText("still here")}),
			},
			expected: "still here\n",
		},
		{
			name:     "unknown type without content skipped",
			blocks:   []*wiki.Block{block("mystery", nil, nil)},
			expected: "",
		},
		{
			name: "unknown type does not panic with no content",
			blocks: []*wiki.Block{
				block("mystery", struct{}{}, nil),
			},
			expected: "",
		},
		{
			name: "mixed content — heading + paragraphs + list + table",
			blocks: []*wiki.Block{
				block("heading", headingProps{Level: 1}, []inlineItem{inlineText("Doc")}),
				block("paragraph", nil, []inlineItem{inlineText("Intro.")}),
				block("bulletListItem", nil, []inlineItem{inlineText("Item 1")}),
				block("bulletListItem", nil, []inlineItem{inlineText("Item 2")}),
				block("table", nil, tableContent{
					Rows: []tableRow{
						{Cells: [][]inlineItem{{inlineText("Col")}}},
						{Cells: [][]inlineItem{{inlineText("Val")}}},
					},
				}),
			},
			expected: "# Doc\n\nIntro.\n\n- Item 1\n- Item 2\n| Col |\n| --- |\n| Val |\n\n",
		},
		{
			name: "multi-style inline text",
			blocks: []*wiki.Block{
				block("paragraph", nil, []inlineItem{
					inlineText("a "),
					inlineBold("b "),
					inlineItalic("c"),
				}),
			},
			expected: "a **b ***c*\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlocksToMarkdown(tt.blocks)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestBlocksToMarkdown_NestedListIndentation verifies 2-space indent per depth level.
func TestBlocksToMarkdown_NestedListIndentation(t *testing.T) {
	tree := []*wiki.Block{
		block("bulletListItem", nil, []inlineItem{inlineText("L0")},
			block("bulletListItem", nil, []inlineItem{inlineText("L1")},
				block("bulletListItem", nil, []inlineItem{inlineText("L2")},
					block("bulletListItem", nil, []inlineItem{inlineText("L3")}),
				),
			),
		),
	}

	expected := "- L0\n  - L1\n    - L2\n      - L3\n"
	require.Equal(t, expected, BlocksToMarkdown(tree))
}

// TestBlocksToMarkdown_TableMultiRow confirms multi-row data rendering.
func TestBlocksToMarkdown_TableMultiRow(t *testing.T) {
	tree := []*wiki.Block{
		block("table", nil, tableContent{
			Rows: []tableRow{
				{Cells: [][]inlineItem{{inlineText("H1")}, {inlineText("H2")}}},
				{Cells: [][]inlineItem{{inlineText("a")}, {inlineText("b")}}},
				{Cells: [][]inlineItem{{inlineText("c")}, {inlineText("d")}}},
			},
		}),
	}

	expected := "| H1 | H2 |\n| --- | --- |\n| a | b |\n| c | d |\n\n"
	require.Equal(t, expected, BlocksToMarkdown(tree))
}

// TestBlocksToMarkdown_PanicSafety confirms no panic on nil/empty/weird inputs.
func TestBlocksToMarkdown_PanicSafety(t *testing.T) {
	assert.NotPanics(t, func() { BlocksToMarkdown(nil) })
	assert.NotPanics(t, func() { BlocksToMarkdown([]*wiki.Block{}) })
	assert.NotPanics(t, func() {
		BlocksToMarkdown([]*wiki.Block{
			{Type: "unknown"},
			{Type: "paragraph"},
			{Type: "heading", Props: json.RawMessage(`{"level":99}`)},
		})
	})
}
