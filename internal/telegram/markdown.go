// Package telegram provides LLM markdown → Telegram HTML conversion.
//
// Telegram HTML is much simpler than MarkdownV2:
//   - <b>bold</b>, <i>italic</i>, <u>underline</u>, <s>strike</s>
//   - <code>inline</code>, <pre>block</pre>, <pre><code class="language-go">...</code></pre>
//   - <a href="url">text</a>
//   - Only &, <, > need escaping (and only outside tags we generate)
//
// Strategy: extract code blocks first (preserve verbatim), then convert
// markdown formatting to HTML tags, escaping everything else.
package telegram

import (
	"regexp"
	"strings"
)

// placeholder sentinels for code block preservation.
const (
	codeBlockPrefix = "\x00CB_"
	inlinePrefix    = "\x00IC_"
	sentinel        = "\x00"
)

var (
	reCodeBlock  = regexp.MustCompile("(?s)```([a-zA-Z0-9]*)\n(.*?)```")
	reInlineCode = regexp.MustCompile("`([^`\n]+)`")
	reBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic     = regexp.MustCompile(`(?:^|[^*])_(.+?)_`)
	reStrike     = regexp.MustCompile(`~~(.+?)~~`)
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeading    = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
)

// FormatHTML converts standard LLM markdown to Telegram-safe HTML.
// Falls back to escapeHTML(text) if anything goes wrong.
func FormatHTML(text string) string {
	if text == "" {
		return ""
	}

	// 1. Extract fenced code blocks → placeholders.
	var codeBlocks []string
	text = reCodeBlock.ReplaceAllStringFunc(text, func(match string) string {
		parts := reCodeBlock.FindStringSubmatch(match)
		lang := parts[1]
		code := parts[2]
		var html string
		if lang != "" {
			html = "<pre><code class=\"language-" + lang + "\">" + escapeHTMLContent(code) + "</code></pre>"
		} else {
			html = "<pre>" + escapeHTMLContent(code) + "</pre>"
		}
		idx := len(codeBlocks)
		codeBlocks = append(codeBlocks, html)
		return codeBlockPrefix + string(rune('A'+idx)) + sentinel
	})

	// 2. Extract inline code → placeholders.
	var inlineCodes []string
	text = reInlineCode.ReplaceAllStringFunc(text, func(match string) string {
		parts := reInlineCode.FindStringSubmatch(match)
		code := parts[1]
		html := "<code>" + escapeHTMLContent(code) + "</code>"
		idx := len(inlineCodes)
		inlineCodes = append(inlineCodes, html)
		return inlinePrefix + string(rune('A'+idx)) + sentinel
	})

	// 3. Escape HTML entities in remaining text.
	text = escapeHTMLContent(text)

	// 4. Convert markdown formatting to HTML.
	// Bold: **text** → <b>text</b>
	text = reBold.ReplaceAllString(text, "<b>$1</b>")

	// Italic: _text_ → <i>text</i>
	// Careful: don't match inside words like some_variable_name
	text = reItalic.ReplaceAllStringFunc(text, func(match string) string {
		parts := reItalic.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		prefix := ""
		if len(match) > 0 && match[0] != '_' {
			prefix = string(match[0])
		}
		return prefix + "<i>" + parts[1] + "</i>"
	})

	// Strikethrough: ~~text~~ → <s>text</s>
	text = reStrike.ReplaceAllString(text, "<s>$1</s>")

	// Links: [text](url) → <a href="url">text</a>
	text = reLink.ReplaceAllString(text, `<a href="$2">$1</a>`)

	// Headings: # Text → <b>Text</b> (Telegram has no heading tag)
	text = reHeading.ReplaceAllString(text, "<b>$1</b>")

	// 5. Restore code blocks.
	for i, block := range codeBlocks {
		placeholder := codeBlockPrefix + string(rune('A'+i)) + sentinel
		text = strings.Replace(text, escapeHTMLContent(placeholder), block, 1)
	}

	// 6. Restore inline code.
	for i, code := range inlineCodes {
		placeholder := inlinePrefix + string(rune('A'+i)) + sentinel
		text = strings.Replace(text, escapeHTMLContent(placeholder), code, 1)
	}

	return text
}

// escapeHTMLContent escapes &, <, > for Telegram HTML.
func escapeHTMLContent(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
