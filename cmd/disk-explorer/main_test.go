package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func testTree(t *testing.T) string {
	t.Helper()
	t.Setenv("DISK_EXPLORER_STATE_DIR", t.TempDir())
	t.Setenv("DISK_EXPLORER_NO_UPDATE", "1")
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	os.WriteFile(filepath.Join(dir, "logs", "app.log"), make([]byte, 2<<20), 0o644)
	os.WriteFile(filepath.Join(dir, "small"), make([]byte, 4096), 0o644)
	return dir
}

func TestUsageErrorsExit64(t *testing.T) {
	for _, args := range [][]string{
		{"--mode", "partiton", "."},
		{"--scan-workers", "99"},
		{"--top", "0"},
		{"--no-such-flag"},
		{"a", "b"},
	} {
		if code, _, _ := runCLI(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestMissingPathIsFatal(t *testing.T) {
	t.Setenv("DISK_EXPLORER_STATE_DIR", t.TempDir())
	for _, mode := range []string{"--summary", "--tree", "--json"} {
		code, out, errOut := runCLI(t, mode, "/does/not/exist")
		if code != exitFatal || !strings.Contains(errOut, "introuvable") {
			t.Errorf("%s: exit %d stderr %q stdout %q", mode, code, errOut, out)
		}
	}
}

func TestSummaryTreeAndJSON(t *testing.T) {
	dir := testTree(t)
	code, out, _ := runCLI(t, "--summary", dir)
	if code != exitOK || !strings.Contains(out, "logs/") || !strings.Contains(out, "logs/app.log") {
		t.Fatalf("summary: exit %d\n%s", code, out)
	}
	code, out, _ = runCLI(t, "--tree", dir)
	if code != exitOK || !strings.Contains(out, "└── ") {
		t.Fatalf("tree: exit %d\n%s", code, out)
	}
	code, out, _ = runCLI(t, "--json", dir)
	var js map[string]any
	if code != exitOK || json.Unmarshal([]byte(out), &js) != nil || js["path"] != dir {
		t.Fatalf("json: exit %d\n%s", code, out)
	}
}

func TestPipedOutputFallsBackToSummary(t *testing.T) {
	dir := testTree(t)
	code, out, _ := runCLI(t, dir) // stdout is a buffer, not a terminal
	if code != exitOK || !strings.Contains(out, "RAPPORT DISQUE") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestGrowthAppearsOnSecondRun(t *testing.T) {
	dir := testTree(t)
	if code, _, _ := runCLI(t, "--summary", dir); code != exitOK {
		t.Fatal("first run failed")
	}
	os.WriteFile(filepath.Join(dir, "logs", "app.log"), make([]byte, 6<<20), 0o644)
	_, out, _ := runCLI(t, "--summary", dir)
	if !strings.Contains(out, "CROISSANCE DEPUIS") || !strings.Contains(out, "+4.0 MiB  logs/app.log") {
		t.Fatalf("growth missing on second run:\n%s", out)
	}
	t.Setenv("DISK_EXPLORER_NO_SNAPSHOT", "1")
	_, out, _ = runCLI(t, "--summary", dir)
	if strings.Contains(out, "CROISSANCE") {
		t.Fatal("DISK_EXPLORER_NO_SNAPSHOT must disable history")
	}
}

func TestPartialScanExit2(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	dir := testTree(t)
	secret := filepath.Join(dir, "secret")
	os.Mkdir(secret, 0o755)
	os.Chmod(secret, 0)
	t.Cleanup(func() { os.Chmod(secret, 0o755) })
	code, out, _ := runCLI(t, "--summary", dir)
	if code != exitPartial || !strings.Contains(out, "SCAN INCOMPLET") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestReportWritesFile(t *testing.T) {
	dir := testTree(t)
	outDir := t.TempDir()
	code, out, _ := runCLI(t, "--report", "--report-dir", outDir, dir)
	if code != exitOK || !strings.Contains(out, "Rapport créé") {
		t.Fatalf("exit %d %s", code, out)
	}
	files, _ := filepath.Glob(filepath.Join(outDir, "Report_*.txt"))
	if len(files) != 1 {
		t.Fatalf("report files: %v", files)
	}
}
