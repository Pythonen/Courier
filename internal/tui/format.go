package tui

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"golang.org/x/net/html"
)

// TODO: We have to either wrap the lines or make the viewport scrollable sideways
func formatResponseBody(body []byte, contentType string) string {
	// Strip charset params: "text/html; charset=utf-8" → "text/html"
	if i := strings.Index(contentType, ";"); i != -1 {
		contentType = strings.TrimSpace(contentType[:i])
	}

	src := string(body)

	// Pretty-print known formats before highlighting
	switch contentType {
	case "application/json", "text/json":
		src = prettyJSON(src)
	case "text/html":
		src = prettyHTML(src)
	}

	return highlight(src, contentType)
}

// prettyJSON indents compact JSON. If it fails to parse, returns the original.
func prettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// prettyHTML parses and re-renders HTML with indentation.
// If parsing fails, returns the original string.
func prettyHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}

	var buf bytes.Buffer
	renderIndented(&buf, doc, 0)
	return buf.String()
}

// renderIndented walks the HTML node tree and writes indented output.
func renderIndented(buf *bytes.Buffer, n *html.Node, depth int) {
	indent := strings.Repeat("  ", depth)

	switch n.Type {
	case html.DocumentNode:
		// Just recurse into children
	case html.DoctypeNode:
		buf.WriteString(indent + "<!DOCTYPE " + n.Data + ">\n")
	case html.ElementNode:
		buf.WriteString(indent + "<" + n.Data)
		for _, a := range n.Attr {
			buf.WriteString(" " + a.Key + `="` + a.Val + `"`)
		}

		// Self-closing tags
		if n.FirstChild == nil {
			buf.WriteString(" />\n")
			return
		}

		buf.WriteString(">\n")

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderIndented(buf, c, depth+1)
		}

		buf.WriteString(indent + "</" + n.Data + ">\n")
		return
	case html.TextNode:
		text := strings.TrimSpace(n.Data)
		if text != "" {
			buf.WriteString(indent + text + "\n")
		}
		return
	case html.CommentNode:
		buf.WriteString(indent + "<!--" + n.Data + "-->\n")
		return
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderIndented(buf, c, depth)
	}
}

// highlight applies chroma syntax highlighting to the source string.
func highlight(src, contentType string) string {
	lexer := lexers.MatchMimeType(contentType)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	iterator, err := lexer.Tokenise(nil, src)
	if err != nil {
		return src
	}

	var buf strings.Builder
	style := styles.Get("monokai")
	formatter := formatters.Get("terminal256")
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return src
	}

	return buf.String()
}
