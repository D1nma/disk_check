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

func TestPrefetchAndInstall(t *testing.T) {
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
		noBinary bool
	}{
		{"valid checksum", "v1.2.3", http.StatusOK, goodHash + "  " + name + "\n", false, false},
		{"sums missing", "v1.2.3", http.StatusNotFound, "", true, false},
		{"no entry for binary", "v1.2.3", http.StatusOK, goodHash + "  other-binary\n", true, false},
		{"binary download fails", "v1.2.3", http.StatusOK, goodHash + "  " + name + "\n", true, true},
		{"tag with path traversal", "../../etc", http.StatusOK, goodHash + "  " + name + "\n", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
					w.WriteHeader(tt.sumsCode)
					io.WriteString(w, tt.sumsBody)
				case strings.HasSuffix(r.URL.Path, "/"+name):
					if tt.noBinary {
						http.NotFound(w, r)
						return
					}
					w.Write(binary)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			orig := repoURL
			repoURL = srv.URL
			defer func() { repoURL = orig }()

			file, err := Prefetch(tt.tag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Prefetch(%q) error = %v, wantErr %v", tt.tag, err, tt.wantErr)
			}

			exe, _ := os.Executable()
			exeDir := filepath.Dir(exe)
			if tt.wantErr {
				if file != "" {
					t.Errorf("Prefetch returned %q on failure", file)
				}
				leftovers, _ := filepath.Glob(filepath.Join(exeDir, "disk-explorer-*.tmp"))
				if len(leftovers) > 0 {
					t.Errorf("temp files left behind after failure: %v", leftovers)
					for _, l := range leftovers {
						os.Remove(l)
					}
				}
				return
			}

			got, err := os.ReadFile(file)
			if err != nil || string(got) != string(binary) {
				t.Fatalf("prefetched content mismatch: %q %v", got, err)
			}
			info, err := os.Stat(file)
			if err != nil || info.Mode()&0111 == 0 {
				t.Fatalf("prefetched file not executable: %v %v", info, err)
			}

			// installTo renames the verified file into place atomically.
			dst := filepath.Join(t.TempDir(), "disk-explorer")
			if err := installTo(file, dst); err != nil {
				t.Fatalf("installTo: %v", err)
			}
			gotDst, err := os.ReadFile(dst)
			if err != nil || string(gotDst) != string(binary) {
				t.Fatalf("installed content mismatch: %q %v", gotDst, err)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Errorf("source file not consumed by the rename")
			}
			leftovers, _ := filepath.Glob(filepath.Join(exeDir, "disk-explorer-*.tmp"))
			if len(leftovers) > 0 {
				t.Errorf("temp files left behind: %v", leftovers)
				for _, l := range leftovers {
					os.Remove(l)
				}
			}
		})
	}
}
