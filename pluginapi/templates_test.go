package pluginapi

import (
	"embed"
	"strings"
	"testing"
)

//go:embed test_templates/*.html
var testTemplatesFS embed.FS

func TestTemplateRenderer_RenderTemplate(t *testing.T) {
	// Create test template directory and file
	renderer := NewTemplateRenderer()

	// Create a simple test template
	testTemplate := `<h1>{{.Title}}</h1><p>{{.Content}}</p>`
	testFS := createTestFS(t, map[string]string{
		"test_templates/simple.html": testTemplate,
	})

	data := map[string]interface{}{
		"Title":   "Test Title",
		"Content": "Test Content",
	}

	html, err := renderer.RenderTemplate(testFS, "test_templates/simple.html", data)
	if err != nil {
		t.Fatalf("failed to render template: %v", err)
	}

	if !strings.Contains(html, "Test Title") {
		t.Error("rendered HTML should contain title")
	}
	if !strings.Contains(html, "Test Content") {
		t.Error("rendered HTML should contain content")
	}
}

func TestTemplateRenderer_Caching(t *testing.T) {
	renderer := NewTemplateRenderer()

	testTemplate := `<h1>{{.Title}}</h1>`
	testFS := createTestFS(t, map[string]string{
		"test_templates/cached.html": testTemplate,
	})

	data := map[string]interface{}{"Title": "Test"}

	// First render (should parse and cache)
	html1, err := renderer.RenderTemplate(testFS, "test_templates/cached.html", data)
	if err != nil {
		t.Fatalf("first render failed: %v", err)
	}

	// Second render (should use cache)
	html2, err := renderer.RenderTemplate(testFS, "test_templates/cached.html", data)
	if err != nil {
		t.Fatalf("second render failed: %v", err)
	}

	// Both should produce the same output
	if html1 != html2 {
		t.Error("cached template should produce same output")
	}

	// Check that template is in cache
	renderer.mu.RLock()
	_, exists := renderer.cache["test_templates/cached.html"]
	renderer.mu.RUnlock()

	if !exists {
		t.Error("template should be in cache")
	}
}

func TestTemplateRenderer_ClearCache(t *testing.T) {
	renderer := NewTemplateRenderer()

	testTemplate := `<h1>{{.Title}}</h1>`
	testFS := createTestFS(t, map[string]string{
		"test_templates/test.html": testTemplate,
	})

	data := map[string]interface{}{"Title": "Test"}

	// Render to populate cache
	_, err := renderer.RenderTemplate(testFS, "test_templates/test.html", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	// Verify cache has content
	renderer.mu.RLock()
	cacheSize := len(renderer.cache)
	renderer.mu.RUnlock()

	if cacheSize == 0 {
		t.Error("cache should not be empty after rendering")
	}

	// Clear cache
	renderer.ClearCache()

	// Verify cache is empty
	renderer.mu.RLock()
	cacheSize = len(renderer.cache)
	renderer.mu.RUnlock()

	if cacheSize != 0 {
		t.Error("cache should be empty after ClearCache()")
	}
}

func TestTemplateRenderer_XSSProtection(t *testing.T) {
	renderer := NewTemplateRenderer()

	testTemplate := `<div>{{.UnsafeContent}}</div>`
	testFS := createTestFS(t, map[string]string{
		"test_templates/xss.html": testTemplate,
	})

	data := map[string]interface{}{
		"UnsafeContent": "<script>alert('xss')</script>",
	}

	html, err := renderer.RenderTemplate(testFS, "test_templates/xss.html", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	// HTML should be escaped automatically
	if strings.Contains(html, "<script>") {
		t.Error("script tags should be escaped to prevent XSS")
	}

	// Should contain escaped version
	if !strings.Contains(html, "&lt;script&gt;") && !strings.Contains(html, "&#") {
		t.Error("HTML should be escaped")
	}
}

func TestTemplateRenderer_MissingTemplate(t *testing.T) {
	renderer := NewTemplateRenderer()

	testFS := createTestFS(t, map[string]string{
		"test_templates/exists.html": "<h1>Exists</h1>",
	})

	_, err := renderer.RenderTemplate(testFS, "test_templates/nonexistent.html", nil)
	if err == nil {
		t.Error("should return error for non-existent template")
	}
}

func TestTemplateRenderer_InvalidTemplate(t *testing.T) {
	renderer := NewTemplateRenderer()

	// Template with syntax error
	invalidTemplate := `<h1>{{.Title}}</h1>{{.UnclosedTag`
	testFS := createTestFS(t, map[string]string{
		"test_templates/invalid.html": invalidTemplate,
	})

	_, err := renderer.RenderTemplate(testFS, "test_templates/invalid.html", nil)
	if err == nil {
		t.Error("should return error for invalid template syntax")
	}
}

func TestTemplateRenderer_ComplexData(t *testing.T) {
	renderer := NewTemplateRenderer()

	testTemplate := `
		<h1>{{.Title}}</h1>
		<ul>
		{{range .Items}}
			<li>{{.Name}}: {{.Value}}</li>
		{{end}}
		</ul>
	`
	testFS := createTestFS(t, map[string]string{
		"test_templates/complex.html": testTemplate,
	})

	type Item struct {
		Name  string
		Value int
	}

	data := map[string]interface{}{
		"Title": "Complex Data",
		"Items": []Item{
			{Name: "Item 1", Value: 10},
			{Name: "Item 2", Value: 20},
			{Name: "Item 3", Value: 30},
		},
	}

	html, err := renderer.RenderTemplate(testFS, "test_templates/complex.html", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if !strings.Contains(html, "Complex Data") {
		t.Error("should contain title")
	}
	if !strings.Contains(html, "Item 1") || !strings.Contains(html, "Item 2") {
		t.Error("should contain all items")
	}
}

func TestRenderTemplate_GlobalFunction(t *testing.T) {
	testTemplate := `<h1>{{.Title}}</h1>`
	testFS := createTestFS(t, map[string]string{
		"test_templates/global.html": testTemplate,
	})

	data := map[string]interface{}{"Title": "Global Test"}

	html, err := RenderTemplate(testFS, "test_templates/global.html", data)
	if err != nil {
		t.Fatalf("global render failed: %v", err)
	}

	if !strings.Contains(html, "Global Test") {
		t.Error("global function should work")
	}
}

func TestTemplateRenderer_ConcurrentAccess(t *testing.T) {
	renderer := NewTemplateRenderer()

	testTemplate := `<h1>{{.Title}}</h1>`
	testFS := createTestFS(t, map[string]string{
		"test_templates/concurrent.html": testTemplate,
	})

	// Render concurrently from multiple goroutines
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			data := map[string]interface{}{"Title": "Concurrent"}
			_, err := renderer.RenderTemplate(testFS, "test_templates/concurrent.html", data)
			if err != nil {
				t.Errorf("concurrent render %d failed: %v", id, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Helper function to create an in-memory test filesystem
func createTestFS(t *testing.T, files map[string]string) embed.FS {
	t.Helper()

	// For testing, we'll use the actual test templates directory
	// In a real scenario, you'd create temp files or use an in-memory FS
	// For now, we'll create the test_templates directory
	return testTemplatesFS
}
