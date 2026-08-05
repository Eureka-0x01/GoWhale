package tests

import (
	"testing"

	"gowhale/internal/tools"
)

func TestIsTextFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"main.go", true},
		{"index.js", true},
		{"style.css", true},
		{"data.json", true},
		{"readme.md", true},
		{"Dockerfile", true},
		{"Makefile", true},
		{"dockerfile.prod", true},
		{"app.py", true},
		{"Cargo.toml", true},
		{"go.mod", true},
		{"go.sum", true},
		{"src/main.rs", true},
		{"image.png", false},
		{"data.bin", false},
		{"archive.tar.gz", false},
		{"binary.exe", false},
		{"photo.jpg", false},
		{"video.mp4", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := tools.IsTextFile(tt.path); got != tt.expected {
				t.Errorf("IsTextFile(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestIsLiteral(t *testing.T) {
	tests := []struct {
		s        string
		expected bool
	}{
		{"hello", true},
		{"TODO", true},
		{"func main", true},
		{"a+b", false},
		{"foo.bar", false},
		{"test*", false},
		{"[abc]", false},
		{"a(b)", false},
		{"x|y", false},
		{"^start", false},
		{"end$", false},
		{"\\escape", false},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			if got := tools.IsLiteral(tt.s); got != tt.expected {
				t.Errorf("IsLiteral(%q) = %v, want %v", tt.s, got, tt.expected)
			}
		})
	}
}
