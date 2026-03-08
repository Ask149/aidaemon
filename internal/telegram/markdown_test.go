package telegram

import (
	"strings"
	"testing"
)

func TestFormatHTML_Empty(t *testing.T) {
	if got := FormatHTML(""); got != "" {
		t.Errorf("FormatHTML empty: got %q, want empty", got)
	}
}

func TestFormatHTML_PlainText(t *testing.T) {
	got := FormatHTML("Hello, world!")
	if got != "Hello, world!" {
		t.Errorf("FormatHTML plain: got %q", got)
	}
}

func TestFormatHTML_Bold(t *testing.T) {
	got := FormatHTML("This is **bold** text")
	if !strings.Contains(got, "<b>bold</b>") {
		t.Errorf("FormatHTML bold: got %q, want <b>bold</b>", got)
	}
}

func TestFormatHTML_Italic(t *testing.T) {
	got := FormatHTML("This is _italic_ text")
	if !strings.Contains(got, "<i>italic</i>") {
		t.Errorf("FormatHTML italic: got %q, want <i>italic</i>", got)
	}
}

func TestFormatHTML_Strikethrough(t *testing.T) {
	got := FormatHTML("This is ~~struck~~ text")
	if !strings.Contains(got, "<s>struck</s>") {
		t.Errorf("FormatHTML strike: got %q, want <s>struck</s>", got)
	}
}

func TestFormatHTML_InlineCode(t *testing.T) {
	got := FormatHTML("Use `fmt.Println` here")
	if !strings.Contains(got, "<code>fmt.Println</code>") {
		t.Errorf("FormatHTML inline code: got %q", got)
	}
}

func TestFormatHTML_FencedCodeBlock(t *testing.T) {
	input := "Look:\n```go\nfmt.Println(\"hello\")\n```"
	got := FormatHTML(input)

	if !strings.Contains(got, `<pre><code class="language-go">`) {
		t.Errorf("FormatHTML code block missing language tag: got %q", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("FormatHTML code block missing content: got %q", got)
	}
}

func TestFormatHTML_FencedCodeBlock_NoLang(t *testing.T) {
	input := "Look:\n```\nsome code\n```"
	got := FormatHTML(input)

	if !strings.Contains(got, "<pre>") {
		t.Errorf("FormatHTML code block without lang: got %q", got)
	}
	if strings.Contains(got, "<code class=") {
		t.Errorf("FormatHTML code block without lang should not have language class: got %q", got)
	}
}

func TestFormatHTML_Link(t *testing.T) {
	got := FormatHTML("Visit [Google](https://google.com) now")
	if !strings.Contains(got, `<a href="https://google.com">Google</a>`) {
		t.Errorf("FormatHTML link: got %q", got)
	}
}

func TestFormatHTML_Heading(t *testing.T) {
	got := FormatHTML("# My Title")
	if !strings.Contains(got, "<b>My Title</b>") {
		t.Errorf("FormatHTML heading: got %q", got)
	}
}

func TestFormatHTML_HTMLEscaping(t *testing.T) {
	got := FormatHTML("Use <div> & >tags<")
	if strings.Contains(got, "<div>") {
		t.Errorf("FormatHTML should escape <div>: got %q", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("FormatHTML should escape &: got %q", got)
	}
	if !strings.Contains(got, "&lt;div&gt;") {
		t.Errorf("FormatHTML should produce &lt;div&gt;: got %q", got)
	}
}

func TestFormatHTML_CodeBlockPreservesSpecialChars(t *testing.T) {
	input := "```\nif a < b && c > d {\n}\n```"
	got := FormatHTML(input)

	// Inside code blocks, < > & should be escaped.
	if !strings.Contains(got, "&lt;") {
		t.Errorf("FormatHTML code block should escape <: got %q", got)
	}
	if !strings.Contains(got, "&amp;&amp;") {
		t.Errorf("FormatHTML code block should escape &&: got %q", got)
	}
}

func TestFormatHTML_MultipleFormats(t *testing.T) {
	input := "**Bold** and _italic_ and `code`"
	got := FormatHTML(input)

	if !strings.Contains(got, "<b>Bold</b>") {
		t.Errorf("missing bold: got %q", got)
	}
	if !strings.Contains(got, "<i>italic</i>") {
		t.Errorf("missing italic: got %q", got)
	}
	if !strings.Contains(got, "<code>code</code>") {
		t.Errorf("missing code: got %q", got)
	}
}

func TestEscapeHTMLContent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"a < b", "a &lt; b"},
		{"a > b", "a &gt; b"},
		{"a & b", "a &amp; b"},
		{"<script>alert('xss')</script>", "&lt;script&gt;alert('xss')&lt;/script&gt;"},
	}

	for _, tt := range tests {
		got := escapeHTMLContent(tt.input)
		if got != tt.want {
			t.Errorf("escapeHTMLContent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
