package skill

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxZipBytes       = 20 * 1024 * 1024
	MaxExtractedBytes = 50 * 1024 * 1024
	MaxFiles          = 200
	MaxFileBytes      = 5 * 1024 * 1024
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) *InvalidError {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

type Frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
}

type Package struct {
	RootDir     string
	RelRoot     string
	Frontmatter *Frontmatter
	RawMeta     map[string]any
	Files       []string
	Deviation   string
}

func ValidateName(name string) error {
	if len(name) < 1 || len(name) > 64 {
		return invalidf("name 长度必须是 1..64 字符（当前 %d）", len(name))
	}
	if !namePattern.MatchString(name) {
		return invalidf("name %q 非法：只能包含小写字母、数字、连字符，且不得以连字符开头/结尾或含连续连字符", name)
	}
	return nil
}

func ParseFrontmatter(doc []byte) (*Frontmatter, map[string]any, error) {
	s := strings.TrimPrefix(string(doc), "\ufeff")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, nil, invalidf("SKILL.md 缺少 YAML frontmatter（首行须为 ---）")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, nil, invalidf("SKILL.md frontmatter 未闭合（缺少 --- 结束行）")
	}
	yamlText := strings.Join(lines[1:end], "\n")
	fm := &Frontmatter{}
	if err := yaml.Unmarshal([]byte(yamlText), fm); err != nil {
		return nil, nil, invalidf("frontmatter YAML 解析失败: %v", err)
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal([]byte(yamlText), &raw); err != nil {
		return nil, nil, invalidf("frontmatter YAML 解析失败: %v", err)
	}
	fm.Name = strings.TrimSpace(fm.Name)
	fm.Description = strings.TrimSpace(fm.Description)
	if err := ValidateName(fm.Name); err != nil {
		return nil, nil, err
	}
	if fm.Description == "" {
		return nil, nil, invalidf("description 必填")
	}
	if len(fm.Description) > 1024 {
		return nil, nil, invalidf("description 长度不能超过 1024（当前 %d）", len(fm.Description))
	}
	return fm, raw, nil
}

func checkZipPath(name string) error {
	if name == "" {
		return invalidf("zip 内存在空路径条目")
	}
	if strings.ContainsRune(name, '\\') {
		return invalidf("非法路径（含反斜杠）: %s", name)
	}
	if strings.HasPrefix(name, "/") || path.IsAbs(name) {
		return invalidf("非法路径（绝对路径）: %s", name)
	}
	for i, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return invalidf("非法路径（路径穿越）: %s", name)
		}
		if i == 0 && len(seg) == 2 && seg[1] == ':' {
			return invalidf("非法路径（盘符绝对路径）: %s", name)
		}
	}
	return nil
}

func extractEntry(f *zip.File, destDir string) (int64, error) {
	rel := path.Clean(f.Name)
	base := filepath.Clean(destDir)
	dest := filepath.Join(base, filepath.FromSlash(rel))
	if dest != base && !strings.HasPrefix(dest, base+string(os.PathSeparator)) {
		return 0, invalidf("非法路径: %s", f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return 0, invalidf("读取 zip 条目 %s 失败: %v", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	w, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, io.LimitReader(rc, MaxFileBytes+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func ExtractZip(data []byte, destDir string) (*Package, error) {
	if len(data) == 0 {
		return nil, invalidf("zip 文件为空")
	}
	if len(data) > MaxZipBytes {
		return nil, invalidf("zip 大小 %d 字节超过 20MB 限制", len(data))
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalidf("zip 解析失败: %v", err)
	}
	fileCount := 0
	var total int64
	for _, f := range zr.File {
		if err := checkZipPath(f.Name); err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, invalidf("仅允许常规文件（拒绝符号链接等）: %s", f.Name)
		}
		fileCount++
		if fileCount > MaxFiles {
			return nil, invalidf("文件数超过 %d 限制", MaxFiles)
		}
		n, err := extractEntry(f, destDir)
		if err != nil {
			return nil, err
		}
		if n > MaxFileBytes {
			return nil, invalidf("文件 %s 超过 5MB 限制（%d 字节）", f.Name, n)
		}
		total += n
		if total > MaxExtractedBytes {
			return nil, invalidf("解压后总大小超过 50MB 限制")
		}
	}
	return locateAndParse(destDir)
}

func locateAndParse(destDir string) (*Package, error) {
	root := destDir
	relRoot := ""
	if fi, err := os.Stat(filepath.Join(destDir, "SKILL.md")); err != nil || !fi.Mode().IsRegular() {
		entries, err := os.ReadDir(destDir)
		if err != nil {
			return nil, err
		}
		matches := []string{}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if fi, err := os.Stat(filepath.Join(destDir, e.Name(), "SKILL.md")); err == nil && fi.Mode().IsRegular() {
				matches = append(matches, e.Name())
			}
		}
		if len(matches) == 0 {
			return nil, invalidf("未找到 SKILL.md（应在 zip 根目录或唯一一级子目录内）")
		}
		if len(matches) > 1 {
			return nil, invalidf("多个一级子目录包含 SKILL.md: %s", strings.Join(matches, ", "))
		}
		relRoot = matches[0]
		root = filepath.Join(destDir, relRoot)
	}
	doc, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	fm, raw, err := ParseFrontmatter(doc)
	if err != nil {
		return nil, err
	}
	files, err := listFiles(root)
	if err != nil {
		return nil, err
	}
	pkg := &Package{RootDir: root, RelRoot: relRoot, Frontmatter: fm, RawMeta: raw, Files: files}
	if relRoot != "" && relRoot != fm.Name {
		pkg.Deviation = fmt.Sprintf("zip 目录名 %q 与 frontmatter name %q 不一致，以 name 为准", relRoot, fm.Name)
	}
	return pkg, nil
}

func listFiles(root string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
