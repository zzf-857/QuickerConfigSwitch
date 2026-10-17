package safekeep

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
)

// diffContext is how many unchanged lines a hunk shows around a change.
const diffContext = 3

// maxLCS bounds the table the line diff fills; past it the changed middle
// is shown as removed and added whole.
const maxLCS = 4 << 20

type diffLine struct {
	op byte // ' ', '-', '+'
	s  string
}

func splitDiffLines(b []byte) []string {
	s := strings.ReplaceAll(string(edit.TrimBOM(b)), "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lineDiff is the edit script from a to b, by longest common subsequence
// of the lines between their common head and tail.
func lineDiff(a, b []string) []diffLine {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []diffLine
	for _, s := range a[:pre] {
		out = append(out, diffLine{' ', s})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	n, m := len(ma), len(mb)
	if n*m > maxLCS {
		for _, s := range ma {
			out = append(out, diffLine{'-', s})
		}
		for _, s := range mb {
			out = append(out, diffLine{'+', s})
		}
	} else {
		// lcs[i][j] is the LCS length of ma[i:] and mb[j:]
		lcs := make([][]int, n+1)
		for i := range lcs {
			lcs[i] = make([]int, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else {
					lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n || j < m {
			switch {
			case i < n && j < m && ma[i] == mb[j]:
				out = append(out, diffLine{' ', ma[i]})
				i++
				j++
			case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
				out = append(out, diffLine{'-', ma[i]})
				i++
			default:
				out = append(out, diffLine{'+', mb[j]})
				j++
			}
		}
	}
	for _, s := range a[len(a)-suf:] {
		out = append(out, diffLine{' ', s})
	}
	return out
}

// unifiedDiff is the unified diff from before to after of the file at
// path, "" when nothing changes. A file that is not there on one side is
// /dev/null.
func unifiedDiff(path string, before, after []byte, existedBefore, existsAfter bool) string {
	if existedBefore == existsAfter && string(before) == string(after) {
		return ""
	}
	name := strings.TrimLeft(filepath.ToSlash(path), "/")
	from, to := "a/"+name, "b/"+name
	if !existedBefore {
		from = "/dev/null"
	}
	if !existsAfter {
		to = "/dev/null"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", from, to)
	script := lineDiff(splitDiffLines(before), splitDiffLines(after))
	// line numbers before each script entry, on each side
	apos, bpos := make([]int, len(script)+1), make([]int, len(script)+1)
	for k, l := range script {
		apos[k+1], bpos[k+1] = apos[k], bpos[k]
		if l.op != '+' {
			apos[k+1]++
		}
		if l.op != '-' {
			bpos[k+1]++
		}
	}
	hunks := 0
	for i := 0; i < len(script); {
		for i < len(script) && script[i].op == ' ' {
			i++
		}
		if i == len(script) {
			break
		}
		start := max(0, i-diffContext)
		end := i
		for {
			for end < len(script) && script[end].op != ' ' {
				end++
			}
			j := end
			for j < len(script) && script[j].op == ' ' {
				j++
			}
			if j < len(script) && j-end <= 2*diffContext {
				end = j
				continue
			}
			end = min(end+diffContext, len(script))
			break
		}
		ac, bc := apos[end]-apos[start], bpos[end]-bpos[start]
		as, bs := apos[start], bpos[start]
		if ac > 0 {
			as++
		}
		if bc > 0 {
			bs++
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", as, ac, bs, bc)
		for _, l := range script[start:end] {
			sb.WriteByte(l.op)
			sb.WriteString(l.s)
			sb.WriteByte('\n')
		}
		hunks++
		i = end
	}
	if hunks == 0 { // only the line ends or the byte order mark differ
		sb.WriteString("@@ line endings or byte order mark change @@\n")
	}
	return sb.String()
}
