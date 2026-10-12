package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	const name = "disk-explorer-linux-amd64"
	content := []byte("fake binary contents")
	sum := sha256.Sum256(content)
	goodHash := hex.EncodeToString(sum[:])
	wrongHash := hex.EncodeToString(make([]byte, sha256.Size))

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		sums    string
		wantErr bool
	}{
		{"matching entry", goodHash + "  " + name + "\n", false},
		{"mismatched hash", wrongHash + "  " + name + "\n", true},
		{"missing entry", goodHash + "  other-binary\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyChecksum(path, name, tt.sums)
			if (err != nil) != tt.wantErr {
				t.Fatalf("verifyChecksum() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSelfUpdate(t *testing.T) {
	name := fmt.Sprintf("disk-explorer-%s-%s", runtime.GOOS, runtime.GOARCH)
	binary := []byte("fake binary")
	sum := sha256.Sum256(binary)
	goodHash := hex.EncodeToString(sum[:])

	tests := []struct {
		name     string
		tag      string
		sumsCode int
		sumsBody string
		wantErr  bool
	}{
		{"valid checksum", "v1.2.3", http.StatusOK, goodHash + "  " + name + "\n", false},
		{"sums missing", "v1.2.3", http.StatusNotFound, "", true},
		{"no entry for binary", "v1.2.3", http.StatusOK, goodHash + "  other-binary\n", true},
		{"tag with path traversal", "../../etc", http.StatusOK, goodHash + "  " + name + "\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
					w.WriteHeader(tt.sumsCode)
					io.WriteString(w, tt.sumsBody)
				case strings.HasSuffix(r.URL.Path, "/"+name):
					w.Write(binary)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			orig := repoURL
			repoURL = srv.URL
			defer func() { repoURL = orig }()

			err := SelfUpdate(tt.tag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SelfUpdate(%q) error = %v, wantErr %v", tt.tag, err, tt.wantErr)
			}

			target := filepath.Join(home, ".cache", "disk-explorer", "bin", tt.tag, "disk-explorer")
			_, statErr := os.Stat(target)
			if tt.wantErr && statErr == nil {
				t.Errorf("target %s written despite failure", target)
			}
			if !tt.wantErr && statErr != nil {
				t.Errorf("target %s not written: %v", target, statErr)
			}
		})
	}
}
