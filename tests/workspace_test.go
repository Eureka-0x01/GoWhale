package tests

import (
	"os"
	"path/filepath"
	"testing"

	"gowhale/internal/tools"
)

func TestCheckPath(t *testing.T) {
	tmpDir := t.TempDir()
	tools.SetWorkspace(tmpDir)

	validSub := filepath.Join(tmpDir, "subdir")
	os.Mkdir(validSub, 0755)

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "workspace root", path: tmpDir, wantErr: false},
		{name: "subdir", path: "subdir", wantErr: false},
		{name: "file", path: "test.go", wantErr: false},
		{name: "nested relative", path: "subdir/../test.go", wantErr: false},
		{name: "dot", path: ".", wantErr: false},
		{name: "parent dir", path: "..", wantErr: true},
		{name: "parent file", path: "../outside.txt", wantErr: true},
		{name: "deep escape", path: "../../etc/passwd", wantErr: true},
		{name: "sandwich escape", path: "subdir/../../etc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tools.CheckPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckPath(%q) error=%v, wantErr=%v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestCheckShell(t *testing.T) {
	tmpDir := t.TempDir()
	tools.SetWorkspace(tmpDir)

	tests := []struct {
		name    string
		cmd     string
		wantErr bool
	}{
		{name: "no cd", cmd: "go build ./...", wantErr: false},
		{name: "cd to valid sub", cmd: "cd subdir && ls", wantErr: false},
		{name: "cd parent slash", cmd: "cd ../", wantErr: true},
		{name: "cd deep escape", cmd: "cd ../../", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tools.CheckShell(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckShell(%q) error=%v, wantErr=%v", tt.cmd, err, tt.wantErr)
			}
		})
	}
}
