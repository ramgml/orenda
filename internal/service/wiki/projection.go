package wiki

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// tableContent mirrors the BlockNote table content shape.
type tableContent struct {
	Rows []tableRow `json:"rows"`
}

type tableRow struct {
	Cells [][]inlineItem `json:"cells"`
}

// inlineItem is one element of a block's inline content array.
type inlineItem struct {
	Type    string       `json:"type"`
	Text    string       `json:"text,omitempty"`
	Styles  *textStyles  `json:"styles,omitempty"`
	Href    string       `json:"href,omitempty"`
	Content []inlineItem `json:"content,omitempty"`
	Props   *inlineProps `json:"props,omitempty"`
}

type textStyles struct {
	Bold          bool `json:"bold"`
	Italic        bool `json:"italic"`
	Strikethrough bool `json:"strikethrough"`
	Code          bool `json:"code"`
}

type inlineProps struct {
	Slug    string `json:"slug"`
	URL     string `json:"url"`
	Caption string `json:"caption"`
	Name    string `json:"name"`
}

// headingProps carries the heading level.
type headingProps struct {
	Level int `json:"level"`
}

// checkListItemProps carries the checked state.
type checkListItemProps struct {
	Checked bool `json:"checked"`
}

// codeBlockProps carries the language.
type codeBlockProps struct {
	Language string `json:"language"`
}

// BlocksToMarkdown converts a tree of wiki blocks into GFM markdown.
// Unknown block types are silently skipped (no panic).
func BlocksToMarkdown(tree []*wiki.Block) string {
	if len(tree) == 0 {
		return ""
	}

	var b strings.Builder
	renderBlocks(&b, tree, 0)
	return b.String()
}

// renderBlocks processes a slice of blocks at a given list nesting depth.
func renderBlocks(b *strings.Builder, blocks []*wiki.Block, depth int) {
	for _, block := range blocks {
		renderBlock(b, block, depth)
	}
}

// renderBlock dispatches a single block to its type-specific renderer.
func renderBlock(b *strings.Builder, block *wiki.Block, depth int) {
	switch block.Type {
	case "paragraph":
		writeInlineContent(b, block.Content)
		b.WriteString("\n\n")
	case "heading":
		renderHeading(b, block)
	case "bulletListItem":
		renderListItem(b, block, depth, "bullet")
	case "numberedListItem":
		renderListItem(b, block, depth, "numbered")
	case "checkListItem":
		renderListItem(b, block, depth, "check")
	case "quote":
		b.WriteString("> ")
		writeInlineContent(b, block.Content)
		b.WriteString("\n")
	case "codeBlock":
		renderCodeBlock(b, block)
	case "divider":
		b.WriteString("---\n\n")
	case "table":
		renderTable(b, block)
	case "image":
		renderImage(b, block)
	case "file":
		renderFile(b, block)
	default:
		// Unknown type: render inline content if present, skip otherwise.
		if len(block.Content) > 0 {
			writeInlineContent(b, block.Content)
			b.WriteString("\n")
		}
	}
}

// renderHeading writes "# heading\n\n" (level 1-3).
func renderHeading(b *strings.Builder, block *wiki.Block) {
	level := 1
	if len(block.Props) > 0 {
		var hp headingProps
		if json.Unmarshal(block.Props, &hp) == nil && hp.Level >= 1 && hp.Level <= 6 {
			level = hp.Level
		}
	}
	b.WriteString(strings.Repeat("#", level))
	b.WriteString(" ")
	writeInlineContent(b, block.Content)
	b.WriteString("\n\n")
}

// renderListItem writes a list item with proper prefix and indented children.
func renderListItem(b *strings.Builder, block *wiki.Block, depth int, kind string) {
	indent := strings.Repeat("  ", depth)

	switch kind {
	case "bullet":
		b.WriteString(indent + "- ")
	case "numbered":
		b.WriteString(indent + "1. ")
	case "check":
		checked := false
		if len(block.Props) > 0 {
			var cp checkListItemProps
			if json.Unmarshal(block.Props, &cp) == nil {
				checked = cp.Checked
			}
		}
		if checked {
			b.WriteString(indent + "- [x] ")
		} else {
			b.WriteString(indent + "- [ ] ")
		}
	}

	writeInlineContent(b, block.Content)
	b.WriteString("\n")

	if len(block.Children) > 0 {
		renderBlocks(b, block.Children, depth+1)
	}
}

// renderCodeBlock writes a fenced code block with optional language tag.
func renderCodeBlock(b *strings.Builder, block *wiki.Block) {
	lang := ""
	if len(block.Props) > 0 {
		var cp codeBlockProps
		if json.Unmarshal(block.Props, &cp) == nil {
			lang = cp.Language
		}
	}
	b.WriteString("```")
	b.WriteString(lang)
	b.WriteString("\n")

	// CodeBlock content is an inline-item array (content:"plain" in
	// BlockNote 0.54 — @blocknote/core/src/blocks/Code/block.ts).
	// Styles are ignored — code blocks don't need formatting.
	if len(block.Content) > 0 {
		var items []inlineItem
		if json.Unmarshal(block.Content, &items) == nil {
			for _, item := range items {
				b.WriteString(item.Text)
			}
		}
	}
	b.WriteString("\n```\n\n")
}

// renderTable writes a GFM table from tableContent. The first row is the
// header; a separator line of "---" is always emitted. Pipes in cell text
// are escaped as \|.
func renderTable(b *strings.Builder, block *wiki.Block) {
	if len(block.Content) == 0 {
		return
	}

	var tc tableContent
	if json.Unmarshal(block.Content, &tc) != nil || len(tc.Rows) == 0 {
		return
	}

	// Render header row.
	cells := tc.Rows[0].Cells
	b.WriteString("| ")
	for i, cell := range cells {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(escapePipes(renderCellInline(cell)))
	}
	b.WriteString(" |\n")

	// Separator.
	b.WriteString("| ")
	for i := range cells {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("---")
	}
	b.WriteString(" |\n")

	// Data rows.
	for _, row := range tc.Rows[1:] {
		b.WriteString("| ")
		for i, cell := range row.Cells {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(escapePipes(renderCellInline(cell)))
		}
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// renderImage writes ![caption](url).
func renderImage(b *strings.Builder, block *wiki.Block) {
	var p inlineProps
	if len(block.Props) > 0 {
		_ = json.Unmarshal(block.Props, &p)
	}
	fmt.Fprintf(b, "![%s](%s)", p.Caption, p.URL)
	b.WriteString("\n\n")
}

// renderFile writes [name](url).
func renderFile(b *strings.Builder, block *wiki.Block) {
	var p inlineProps
	if len(block.Props) > 0 {
		_ = json.Unmarshal(block.Props, &p)
	}
	fmt.Fprintf(b, "[%s](%s)", p.Name, p.URL)
	b.WriteString("\n\n")
}

// writeInlineContent marshals and renders a JSON inline content array.
func writeInlineContent(b *strings.Builder, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var items []inlineItem
	if json.Unmarshal(raw, &items) != nil {
		return
	}
	for _, item := range items {
		renderInlineItem(b, item)
	}
}

// renderInlineItem handles text (with styles), link, wikiLink, and unknown types.
func renderInlineItem(b *strings.Builder, item inlineItem) {
	switch item.Type {
	case "text":
		text := item.Text
		if item.Styles != nil {
			if item.Styles.Code {
				text = "`" + text + "`"
			}
			if item.Styles.Strikethrough {
				text = "~~" + text + "~~"
			}
			if item.Styles.Italic {
				text = "*" + text + "*"
			}
			if item.Styles.Bold {
				text = "**" + text + "**"
			}
		}
		b.WriteString(text)
	case "link":
		var inner strings.Builder
		for _, c := range item.Content {
			renderInlineItem(&inner, c)
		}
		fmt.Fprintf(b, "[%s](%s)", inner.String(), item.Href)
	case "wikiLink":
		if item.Props != nil {
			b.WriteString("[[" + item.Props.Slug + "]]")
		}
	default:
		// Unknown inline: render nested content if present.
		for _, c := range item.Content {
			renderInlineItem(b, c)
		}
	}
}

// renderCellInline renders inline items for a table cell.
func renderCellInline(items []inlineItem) string {
	var b strings.Builder
	for _, item := range items {
		renderInlineItem(&b, item)
	}
	return b.String()
}

// escapePipes replaces | with \| for GFM table cells.
func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
