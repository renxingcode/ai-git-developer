package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// 行内片段类型：eq=未变化，del=删除，add=新增
type Segment struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// DiffLine diff 中的一行
type DiffLine struct {
	Type     string    `json:"type"` // ctx / add / del
	OldNo    int       `json:"oldNo"`
	NewNo    int       `json:"newNo"`
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"` // 行内字符级高亮（配对的增删行）
}

// Hunk 一个 diff 块
type Hunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

// FileDiff 一个文件的完整 diff
type FileDiff struct {
	Path     string `json:"path"`
	OldPath  string `json:"oldPath"`
	Status   string `json:"status"` // M / A / D / R
	IsBinary bool   `json:"isBinary"`
	Hunks    []Hunk `json:"hunks"`
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff 解析统一格式 diff 文本
func ParseDiff(text string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	oldNo, newNo := 0, 0

	flush := func() {
		if cur != nil {
			if hunk != nil {
				cur.Hunks = append(cur.Hunks, *hunk)
				hunk = nil
			}
			files = append(files, *cur)
		}
	}

	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			flush()
			cur = &FileDiff{Status: "M"}
			// diff --git a/path b/path，取 b/ 后路径
			if m := strings.LastIndex(ln, " b/"); m >= 0 {
				cur.Path = ln[m+3:]
				cur.OldPath = cur.Path
			}
		case strings.HasPrefix(ln, "new file mode"):
			if cur != nil {
				cur.Status = "A"
			}
		case strings.HasPrefix(ln, "deleted file mode"):
			if cur != nil {
				cur.Status = "D"
			}
		case strings.HasPrefix(ln, "rename from "):
			if cur != nil {
				cur.OldPath = strings.TrimPrefix(ln, "rename from ")
				cur.Status = "R"
			}
		case strings.HasPrefix(ln, "rename to "):
			if cur != nil {
				cur.Path = strings.TrimPrefix(ln, "rename to ")
			}
		case strings.HasPrefix(ln, "Binary files"):
			if cur != nil {
				cur.IsBinary = true
			}
		case strings.HasPrefix(ln, "--- "):
			if cur != nil {
				p := strings.TrimPrefix(ln, "--- ")
				p = strings.TrimPrefix(p, "a/")
				if p != "/dev/null" {
					cur.OldPath = p
				}
			}
		case strings.HasPrefix(ln, "+++ "):
			if cur != nil {
				p := strings.TrimPrefix(ln, "+++ ")
				if p == "/dev/null" {
					cur.Path = cur.OldPath
				} else {
					p = strings.TrimPrefix(p, "b/")
					cur.Path = p
				}
			}
		case strings.HasPrefix(ln, "@@"):
			if cur == nil {
				continue
			}
			if hunk != nil {
				cur.Hunks = append(cur.Hunks, *hunk)
			}
			hunk = &Hunk{Header: ln}
			m := hunkRe.FindStringSubmatch(ln)
			if m != nil {
				oldNo, _ = strconv.Atoi(m[1])
				newNo, _ = strconv.Atoi(m[3])
			}
		case hunk != nil && strings.HasPrefix(ln, "+"):
			hunk.Lines = append(hunk.Lines, DiffLine{Type: "add", OldNo: 0, NewNo: newNo, Text: ln[1:]})
			newNo++
		case hunk != nil && strings.HasPrefix(ln, "-"):
			hunk.Lines = append(hunk.Lines, DiffLine{Type: "del", OldNo: oldNo, NewNo: 0, Text: ln[1:]})
			oldNo++
		case hunk != nil && strings.HasPrefix(ln, " "):
			hunk.Lines = append(hunk.Lines, DiffLine{Type: "ctx", OldNo: oldNo, NewNo: newNo, Text: ln[1:]})
			oldNo++
			newNo++
		case strings.HasPrefix(ln, `\ No newline`):
			// 忽略“文件末尾无换行”提示
		}
	}
	flush()

	for i := range files {
		for j := range files[i].Hunks {
			annotateHunk(&files[i].Hunks[j])
		}
	}
	return files
}

// annotateHunk 对 hunk 内连续的删/增行块做配对与字符级高亮
func annotateHunk(h *Hunk) {
	for i := 0; i < len(h.Lines); {
		if h.Lines[i].Type != "del" {
			i++
			continue
		}
		j := i
		var dels, adds []int
		for j < len(h.Lines) && (h.Lines[j].Type == "del" || h.Lines[j].Type == "add") {
			if h.Lines[j].Type == "del" {
				dels = append(dels, j)
			} else {
				adds = append(adds, j)
			}
			j++
		}
		k := 0
		for ; k < len(dels) && k < len(adds); k++ {
			os, ns := inlinePair(h.Lines[dels[k]].Text, h.Lines[adds[k]].Text)
			h.Lines[dels[k]].Segments = os
			h.Lines[adds[k]].Segments = ns
		}
		for ; k < len(dels); k++ {
			h.Lines[dels[k]].Segments = mergeSegs([]Segment{{Type: "del", Text: h.Lines[dels[k]].Text}})
		}
		for ; k < len(adds); k++ {
			h.Lines[adds[k]].Segments = mergeSegs([]Segment{{Type: "add", Text: h.Lines[adds[k]].Text}})
		}
		i = j
	}
}

// tokenize 把一行切成适合 LCS 的 token：英文/数字连续成词，中文逐字，连续空白与标点各自成块
func tokenize(s string) []string {
	var toks []string
	var buf strings.Builder
	flushWord := func() {
		if buf.Len() > 0 {
			toks = append(toks, buf.String())
			buf.Reset()
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			buf.WriteRune(r)
		case unicode.IsSpace(r):
			flushWord()
			if len(toks) == 0 || toks[len(toks)-1] != " " {
				toks = append(toks, " ")
			}
		default:
			flushWord()
			toks = append(toks, string(r))
		}
	}
	flushWord()
	return toks
}

// inlinePair 对一对旧行/新行做 token 级 LCS，返回各自的高亮片段
func inlinePair(oldLine, newLine string) ([]Segment, []Segment) {
	a := tokenize(oldLine)
	b := tokenize(newLine)
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	type step struct {
		tag  int // 0 eq,1 del,2 add
		text string
	}
	var steps []step
	i, j := n, m
	for i > 0 && j > 0 {
		switch {
		case a[i-1] == b[j-1]:
			steps = append(steps, step{0, a[i-1]})
			i--
			j--
		case dp[i-1][j] >= dp[i][j-1]:
			steps = append(steps, step{1, a[i-1]})
			i--
		default:
			steps = append(steps, step{2, b[j-1]})
			j--
		}
	}
	for i > 0 {
		steps = append(steps, step{1, a[i-1]})
		i--
	}
	for j > 0 {
		steps = append(steps, step{2, b[j-1]})
		j--
	}
	// 反转回正序
	for x, y := 0, len(steps)-1; x < y; x, y = x+1, y-1 {
		steps[x], steps[y] = steps[y], steps[x]
	}

	var oldSegs, newSegs []Segment
	for _, st := range steps {
		switch st.tag {
		case 0:
			oldSegs = append(oldSegs, Segment{Type: "eq", Text: st.text})
			newSegs = append(newSegs, Segment{Type: "eq", Text: st.text})
		case 1:
			oldSegs = append(oldSegs, Segment{Type: "del", Text: st.text})
		case 2:
			newSegs = append(newSegs, Segment{Type: "add", Text: st.text})
		}
	}
	return mergeSegs(oldSegs), mergeSegs(newSegs)
}

// mergeSegs 合并相邻同类型片段
func mergeSegs(segs []Segment) []Segment {
	var out []Segment
	for _, s := range segs {
		if len(out) > 0 && out[len(out)-1].Type == s.Type {
			out[len(out)-1].Text += s.Text
		} else {
			out = append(out, s)
		}
	}
	return out
}
