package imageutil

import (
	"testing"
)

func TestParseImages_NoMarkers(t *testing.T) {
	content := "Just some plain text result"
	cleaned, images := ParseImages(content)
	if cleaned != content {
		t.Errorf("cleaned = %q, want %q", cleaned, content)
	}
	if images != nil {
		t.Errorf("images = %v, want nil", images)
	}
}

func TestParseImages_SingleImage(t *testing.T) {
	content := "Before [MCP_IMAGE:image/png:AAAA] After"
	cleaned, images := ParseImages(content)

	wantCleaned := "Before [Screenshot delivered] After"
	if cleaned != wantCleaned {
		t.Errorf("cleaned = %q, want %q", cleaned, wantCleaned)
	}
	if len(images) != 1 {
		t.Fatalf("len(images) = %d, want 1", len(images))
	}
	if images[0].MimeType != "image/png" {
		t.Errorf("MimeType = %q, want %q", images[0].MimeType, "image/png")
	}
	if images[0].Base64Data != "AAAA" {
		t.Errorf("Base64Data = %q, want %q", images[0].Base64Data, "AAAA")
	}
}

func TestParseImages_MultipleImages(t *testing.T) {
	content := "[MCP_IMAGE:image/png:AAAA] text [MCP_IMAGE:image/jpeg:BBBB]"
	cleaned, images := ParseImages(content)

	wantCleaned := "[Screenshot delivered] text [Screenshot delivered]"
	if cleaned != wantCleaned {
		t.Errorf("cleaned = %q, want %q", cleaned, wantCleaned)
	}
	if len(images) != 2 {
		t.Fatalf("len(images) = %d, want 2", len(images))
	}
	if images[0].MimeType != "image/png" || images[0].Base64Data != "AAAA" {
		t.Errorf("images[0] = %+v", images[0])
	}
	if images[1].MimeType != "image/jpeg" || images[1].Base64Data != "BBBB" {
		t.Errorf("images[1] = %+v", images[1])
	}
}

func TestParseImages_MalformedMarker(t *testing.T) {
	// Marker without the colon separator between mime and data.
	content := "Before [MCP_IMAGE:nocolon] After"
	cleaned, _ := ParseImages(content)

	wantCleaned := "Before [malformed image marker] After"
	if cleaned != wantCleaned {
		t.Errorf("cleaned = %q, want %q", cleaned, wantCleaned)
	}
}

func TestParseImages_UnclosedMarker(t *testing.T) {
	content := "Before [MCP_IMAGE:image/png:data no closing bracket"
	cleaned, images := ParseImages(content)
	if cleaned != content {
		t.Errorf("cleaned = %q, want %q (unchanged)", cleaned, content)
	}
	if images != nil {
		t.Errorf("images = %v, want nil", images)
	}
}

func TestDataURL(t *testing.T) {
	img := Image{MimeType: "image/png", Base64Data: "iVBOR"}
	got := DataURL(img)
	want := "data:image/png;base64,iVBOR"
	if got != want {
		t.Errorf("DataURL() = %q, want %q", got, want)
	}
}

func TestShouldAutoScreenshot(t *testing.T) {
	tests := []struct {
		toolName string
		want     bool
	}{
		{"mcp_playwright_browser_click", true},
		{"mcp_playwright_browser_navigate", true},
		{"mcp_playwright_browser_type", true},
		{"mcp_playwright_browser_take_screenshot", false},
		{"mcp_playwright_browser_console_messages", false},
		{"read_file", false},
		{"web_fetch", false},
		{"browser_click", false}, // no mcp_playwright_ prefix
	}
	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := ShouldAutoScreenshot(tt.toolName)
			if got != tt.want {
				t.Errorf("ShouldAutoScreenshot(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}
