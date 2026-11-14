package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildSelf compiles disk-explorer for the remote side of the tests.
func buildSelf(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}
	bin := filepath.Join(t.TempDir(), "disk-explorer")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func withFakeSSH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	src, _ := filepath.Abs("../../internal/remote/testdata/fake-ssh")
	if err := os.Symlink(src, filepath.Join(dir, "ssh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSSHEndToEnd(t *testing.T) {
	bin := buildSelf(t)
	withFakeSSH(t)
	dir := testTree(t)

	code, out, errOut := runCLI(t, "--ssh", "box:"+dir, "--ssh-binary", bin, "--summary")
	if code != exitOK || !strings.Contains(out, "logs/app.log") || !strings.Contains(out, "Dossier : "+dir) {
		t.Fatalf("remote summary: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "envoi du binaire") {
		t.Errorf("progress messages missing on stderr: %s", errOut)
	}

	// A second run shows growth (remote scans are recorded per host:path).
	os.WriteFile(filepath.Join(dir, "logs", "app.log"), make([]byte, 6<<20), 0o644)
	_, out, _ = runCLI(t, "--ssh", "box:"+dir, "--ssh-binary", bin, "--summary")
	if !strings.Contains(out, "CROISSANCE DEPUIS") {
		t.Errorf("growth missing for the remote host:\n%s", out)
	}

	// Fleet: two hosts, one unreachable.
	code, out, _ = runCLI(t, "--ssh", "box:"+dir+",badhost:/", "--ssh-binary", bin)
	if code != exitFatal || !strings.Contains(out, "HÔTE") || !strings.Contains(out, "box:"+dir) || !strings.Contains(out, "ÉCHEC") {
		t.Fatalf("fleet: exit %d\n%s", code, out)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	dir := testTree(t)
	file := filepath.Join(t.TempDir(), "scan.json")
	if code, _, errOut := runCLI(t, "--export", file, dir); code != exitOK {
		t.Fatalf("export: %d %s", code, errOut)
	}
	code, out, _ := runCLI(t, "--import", file, "--summary")
	if code != exitOK || !strings.Contains(out, "logs/app.log") {
		t.Fatalf("import: exit %d\n%s", code, out)
	}
	prom := filepath.Join(t.TempDir(), "disk.prom")
	if code, _, _ := runCLI(t, "--import", file, "--prom", prom); code != exitOK {
		t.Fatal("prom from import failed")
	}
	b, _ := os.ReadFile(prom)
	if !strings.Contains(string(b), "disk_explorer_directory_bytes{") {
		t.Fatalf("prom output:\n%s", b)
	}
}
