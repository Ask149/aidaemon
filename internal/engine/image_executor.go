package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/imageutil"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/tools"
)

// ImageCallback is called when images are extracted from tool results.
// toolName identifies which tool produced the images (or "auto-screenshot").
type ImageCallback func(ctx context.Context, toolName string, images []imageutil.Image)

// ImageAwareExecutor wraps tool execution with image extraction and
// automatic screenshot support. It detects [MCP_IMAGE:...] markers in
// tool results, delivers images via the OnImage callback, and triggers
// auto-screenshots after Playwright state-changing actions.
type ImageAwareExecutor struct {
	Registry *tools.Registry
	OnImage  ImageCallback // called when images are found; nil = images silently stripped

	// ScreenshotDelay is how long to wait after a state-changing Playwright
	// action before taking the auto-screenshot. Zero uses the default (800ms).
	// Tests can set this to a small value to avoid sleeping.
	ScreenshotDelay time.Duration
}

// screenshotToolName is the fully-qualified registry name for the
// Playwright screenshot tool used by auto-screenshot.
const screenshotToolName = "mcp_playwright_browser_take_screenshot"

// autoScreenshotDelay is how long to wait after a state-changing
// Playwright action before taking the auto-screenshot.
const autoScreenshotDelay = 800 * time.Millisecond

// autoScreenshotRetryDelay is the pause between retry attempts.
const autoScreenshotRetryDelay = 500 * time.Millisecond

// ExecuteToolCalls implements ToolExecutor. For each tool call it:
//  1. Executes the tool via the Registry
//  2. Parses [MCP_IMAGE:...] markers from the result
//  3. Delivers found images via OnImage
//  4. Returns cleaned content (markers replaced with placeholders)
//  5. Triggers auto-screenshot for Playwright state-changing tools
func (e *ImageAwareExecutor) ExecuteToolCalls(ctx context.Context, calls []provider.ToolCall) []provider.Message {
	if e.Registry == nil {
		return nil
	}

	results := make([]provider.Message, len(calls))

	for i, call := range calls {
		log.Printf("[engine] executing tool: %s (id=%s)", call.Function.Name, call.ID)

		result, err := e.Registry.Execute(ctx, call.Function.Name, call.Function.Arguments)

		content := result
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
			log.Printf("[engine] tool error: %s: %v", call.Function.Name, err)
		} else {
			// Extract and deliver images from tool result.
			content = e.handleImages(ctx, content, call.Function.Name)

			// Auto-screenshot after state-changing Playwright actions.
			e.maybeAutoScreenshot(ctx, call.Function.Name)

			// Truncate log output for readability.
			logContent := content
			if len(logContent) > 200 {
				logContent = logContent[:200] + "..."
			}
			log.Printf("[engine] tool result: %s -> %s", call.Function.Name, logContent)
		}

		results[i] = provider.Message{
			Role:       "tool",
			Content:    content,
			ToolCallID: call.ID,
		}
	}

	return results
}

// handleImages extracts MCP image markers from content, delivers images
// via OnImage, and returns the cleaned content.
func (e *ImageAwareExecutor) handleImages(ctx context.Context, content, toolName string) string {
	if !strings.Contains(content, "[MCP_IMAGE:") {
		return content
	}

	cleaned, images := imageutil.ParseImages(content)
	if len(images) > 0 && e.OnImage != nil {
		e.OnImage(ctx, toolName, images)
	}

	return cleaned
}

// maybeAutoScreenshot triggers an automatic screenshot after Playwright
// state-changing tool calls. It waits briefly for the page to render,
// then takes a screenshot and delivers it via OnImage.
func (e *ImageAwareExecutor) maybeAutoScreenshot(ctx context.Context, toolName string) {
	if !imageutil.ShouldAutoScreenshot(toolName) {
		return
	}

	// Verify the screenshot tool is available.
	if e.Registry.Get(screenshotToolName) == nil {
		return
	}

	// Wait for the page to settle after state-changing actions.
	delay := e.ScreenshotDelay
	if delay == 0 {
		delay = autoScreenshotDelay
	}
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return
	}

	log.Printf("[engine] auto-screenshot after %s", toolName)

	// Try taking screenshot with one retry on failure.
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := e.Registry.Execute(ctx, screenshotToolName, "{}")
		if err != nil {
			log.Printf("[engine] auto-screenshot attempt %d failed: %v", attempt, err)
			if attempt < 2 {
				select {
				case <-time.After(autoScreenshotRetryDelay):
				case <-ctx.Done():
					return
				}
				continue
			}
			return
		}

		// Extract images from the screenshot result.
		if strings.Contains(result, "[MCP_IMAGE:") {
			_, images := imageutil.ParseImages(result)
			if len(images) > 0 && e.OnImage != nil {
				e.OnImage(ctx, "auto-screenshot", images)
			}
		} else {
			log.Printf("[engine] auto-screenshot: no image content in result (len=%d)", len(result))
		}
		return // done on first success
	}
}
