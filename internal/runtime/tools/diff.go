package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UnifiedDiff 生成带 3 行上下文的 unified diff（LCS 行级）。
func UnifiedDiff(path, oldContent, newContent string) string {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)
	ops := lcsOps(oldLines, newLines)

	var b strings.Builder
	fmt.Fprintf(&b, "--- a%s\n+++ b%s\n", path, path)

	const ctxLines = 3
	type hunk struct{ start, end int }
	hunks := []hunk{}
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i - ctxLines
		if start < 0 {
			start = 0
		}
		end := i
		for end < len(ops) {
			if ops[end].kind == ' ' {
				run := 0
				for end+run < len(ops) && ops[end+run].kind == ' ' {
					run++
				}
				if end+run >= len(ops) || run > 2*ctxLines {
					end += ctxLines
					if end > len(ops) {
						end = len(ops)
					}
					break
				}
				end += run
				continue
			}
			end++
		}
		hunks = append(hunks, hunk{start, end})
		i = end
	}

	for _, h := range hunks {
		oldStart, newStart, oldCount, newCount := 0, 0, 0, 0
		for _, op := range ops[:h.start] {
			if op.kind != '+' {
				oldStart++
			}
			if op.kind != '-' {
				newStart++
			}
		}
		for _, op := range ops[h.start:h.end] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart+1, oldCount, newStart+1, newCount)
		for _, op := range ops[h.start:h.end] {
			fmt.Fprintf(&b, "%c%s\n", op.kind, op.line)
		}
	}
	return b.String()
}

type diffOp struct {
	kind byte // ' ' keep, '-' delete, '+' insert
	line string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

func skillDir(dataDir, orgID, skillID string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("skill storage not configured")
	}
	return filepath.Join(dataDir, "skills", orgID, skillID), nil
}

func readSkillMD(dir string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", false, err
	}
	if len(data) > skillMaxBytes {
		return string(data[:skillMaxBytes]), true, nil
	}
	return string(data), false, nil
}

func listSkillFiles(dir string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}
