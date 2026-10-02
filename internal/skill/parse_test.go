package skill

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	data []byte
	mode os.FileMode
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create entry %s: %v", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatalf("write entry %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func skillMD(fm string) []byte {
	return []byte("---\n" + fm + "\n---\n\n# Usage\n")
}

func TestParseFrontmatterMinimal(t *testing.T) {
	fm, raw, err := ParseFrontmatter(skillMD("name: pdf-tools\ndescription: Handle PDF files"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "pdf-tools" || fm.Description != "Handle PDF files" {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
	if raw["name"] != "pdf-tools" {
		t.Fatalf("raw meta missing name: %v", raw)
	}
}

func TestParseFrontmatterFull(t *testing.T) {
	doc := skillMD(`name: data-crunch
description: Crunch data sets
license: MIT
compatibility: "Requires python 3.11"
metadata:
  author: team-a
  version: "1.2"
allowed-tools: exec read write`)
	fm, raw, err := ParseFrontmatter(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.License != "MIT" || fm.Compatibility != "Requires python 3.11" || fm.AllowedTools != "exec read write" {
		t.Fatalf("unexpected optional fields: %+v", fm)
	}
	if fm.Metadata["author"] != "team-a" || fm.Metadata["version"] != "1.2" {
		t.Fatalf("unexpected metadata: %+v", fm.Metadata)
	}
	if _, ok := raw["metadata"].(map[string]any); !ok {
		t.Fatalf("raw metadata not a map: %v", raw["metadata"])
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"a", "pdf-tools", "x1-y2", strings.Repeat("a", 64)}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Fatalf("name %q should be valid: %v", n, err)
		}
	}
	invalid := []string{
		"",
		"PDF-Tools",
		"-pdf",
		"pdf-",
		"pdf--tools",
		"pdf_tools",
		"pdf tools",
		strings.Repeat("a", 65),
	}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Fatalf("name %q should be invalid", n)
		}
	}
}

func TestParseFrontmatterMissingDescription(t *testing.T) {
	if _, _, err := ParseFrontmatter(skillMD("name: pdf-tools")); err == nil {
		t.Fatal("missing description should fail")
	}
	if _, _, err := ParseFrontmatter(skillMD("name: pdf-tools\ndescription: ''")); err == nil {
		t.Fatal("empty description should fail")
	}
}

func TestParseFrontmatterDescriptionTooLong(t *testing.T) {
	long := strings.Repeat("x", 1025)
	if _, _, err := ParseFrontmatter(skillMD("name: pdf-tools\ndescription: " + long)); err == nil {
		t.Fatal("description > 1024 should fail")
	}
}

func TestParseFrontmatterNoDelimiter(t *testing.T) {
	if _, _, err := ParseFrontmatter([]byte("# just markdown\n")); err == nil {
		t.Fatal("missing frontmatter should fail")
	}
	if _, _, err := ParseFrontmatter([]byte("---\nname: a\n")); err == nil {
		t.Fatal("unclosed frontmatter should fail")
	}
}

func extractToTemp(t *testing.T, data []byte) (*Package, error) {
	t.Helper()
	dir := t.TempDir()
	return ExtractZip(data, dir)
}

func TestExtractZipRootSkillMD(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")},
		{name: "scripts/run.py", data: []byte("print('hi')\n")},
		{name: "docs/", data: nil},
		{name: "docs/guide.md", data: []byte("# guide\n")},
	})
	pkg, err := extractToTemp(t, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.RelRoot != "" {
		t.Fatalf("expected root package, got rel root %q", pkg.RelRoot)
	}
	want := []string{"SKILL.md", "docs/guide.md", "scripts/run.py"}
	if strings.Join(pkg.Files, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", pkg.Files, want)
	}
	if pkg.Deviation != "" {
		t.Fatalf("unexpected deviation: %s", pkg.Deviation)
	}
}

func TestExtractZipUniqueSubdir(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "random-dir/SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")},
		{name: "random-dir/a.txt", data: []byte("a")},
	})
	pkg, err := extractToTemp(t, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.RelRoot != "random-dir" {
		t.Fatalf("rel root = %q", pkg.RelRoot)
	}
	if pkg.Deviation == "" {
		t.Fatal("expected dir/name deviation to be recorded")
	}
}

func TestExtractZipSubdirMatchingName(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "pdf-tools/SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")},
	})
	pkg, err := extractToTemp(t, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.Deviation != "" {
		t.Fatalf("no deviation expected, got %q", pkg.Deviation)
	}
}

func TestExtractZipNoSkillMD(t *testing.T) {
	data := buildZip(t, []zipEntry{{name: "readme.txt", data: []byte("hi")}})
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("missing SKILL.md should fail")
	}
}

func TestExtractZipAmbiguousSubdirs(t *testing.T) {
	fm := skillMD("name: pdf-tools\ndescription: Handle PDFs")
	data := buildZip(t, []zipEntry{
		{name: "a/SKILL.md", data: fm},
		{name: "b/SKILL.md", data: fm},
	})
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("ambiguous subdirs should fail")
	}
}

func TestExtractZipSlip(t *testing.T) {
	fm := skillMD("name: pdf-tools\ndescription: Handle PDFs")
	cases := map[string]string{
		"dotdot":     "../evil.txt",
		"nested":     "a/../../evil.txt",
		"absolute":   "/abs.txt",
		"drive":      "C:/win.txt",
		"backslash":  "a\\b.txt",
		"backslash2": "..\\evil.txt",
	}
	for label, evil := range cases {
		t.Run(label, func(t *testing.T) {
			data := buildZip(t, []zipEntry{
				{name: "SKILL.md", data: fm},
				{name: evil, data: []byte("x")},
			})
			_, err := extractToTemp(t, data)
			var ie *InvalidError
			if !errors.As(err, &ie) {
				t.Fatalf("expected InvalidError, got %v", err)
			}
		})
	}
}

func TestExtractZipSymlinkRejected(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")},
		{name: "link", data: []byte("/etc/passwd"), mode: os.ModeSymlink},
	})
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("symlink entry should fail")
	}
}

func TestExtractZipTooManyFiles(t *testing.T) {
	entries := []zipEntry{{name: "SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")}}
	for i := 0; i < MaxFiles; i++ {
		entries = append(entries, zipEntry{name: fmt.Sprintf("f%03d.txt", i), data: []byte("x")})
	}
	data := buildZip(t, entries)
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("file count over limit should fail")
	}
}

func TestExtractZipSingleFileTooLarge(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")},
		{name: "big.bin.txt", data: bytes.Repeat([]byte("x"), MaxFileBytes+1)},
	})
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("file over 5MB should fail")
	}
}

func TestExtractZipTotalTooLarge(t *testing.T) {
	entries := []zipEntry{{name: "SKILL.md", data: skillMD("name: pdf-tools\ndescription: Handle PDFs")}}
	chunk := bytes.Repeat([]byte("y"), 4*1024*1024)
	for i := 0; i < 13; i++ {
		entries = append(entries, zipEntry{name: fmt.Sprintf("blob%d.txt", i), data: chunk})
	}
	data := buildZip(t, entries)
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("extracted total over 50MB should fail")
	}
}

func TestExtractZipOversizedZip(t *testing.T) {
	data := make([]byte, MaxZipBytes+1)
	if _, err := extractToTemp(t, data); err == nil {
		t.Fatal("zip over 20MB should fail")
	}
}

func TestExtractZipNotAZip(t *testing.T) {
	if _, err := extractToTemp(t, []byte("not a zip")); err == nil {
		t.Fatal("garbage should fail")
	}
}
