package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// FileChange 工作区一个变更文件（来自 git status --porcelain）
type FileChange struct {
	Path   string `json:"path"`
	Code   string `json:"code"`   // 原始 XY 状态码
	Status string `json:"status"` // 单字母状态：M/A/D/R/C/U/?
}

// Commit 一条提交记录
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
}

// Git 封装所有 git 操作；通过 os/exec 调用本机 git，不依赖 go-git 库
type Git struct{}

func NewGit() *Git { return &Git{} }

func (g *Git) run(repo string, args ...string) (string, error) {
	return g.runInput(repo, nil, args...)
}

func (g *Git) runInput(repo string, stdin []byte, args ...string) (string, error) {
	full := append([]string{"-C", repo}, args...)
	cmd := exec.Command("git", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return string(out), fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

// CurrentBranch 当前所在分支（detached 时返回 HEAD）
func (g *Git) CurrentBranch(repo string) (string, error) {
	out, err := g.run(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HeadSHA HEAD 完整 SHA
func (g *Git) HeadSHA(repo string) (string, error) {
	out, err := g.run(repo, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// RefExists 校验引用/提交存在
func (g *Git) RefExists(repo, ref string) error {
	_, err := g.run(repo, "rev-parse", "--verify", "--quiet", ref)
	return err
}

// IsClean 工作区是否干净（忽略 untracked？不，porcelain 含 ??）
func (g *Git) IsClean(repo string) (bool, []string, error) {
	out, err := g.run(repo, "status", "--porcelain")
	if err != nil {
		return false, nil, err
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, ln)
		}
	}
	return len(lines) == 0, lines, nil
}

// Porcelain 返回工作区变更文件列表
func (g *Git) Porcelain(repo string) ([]FileChange, error) {
	out, err := g.run(repo, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	res := []FileChange{}
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(ln) < 3 {
			continue
		}
		xy := ln[:2]
		path := strings.TrimSpace(ln[3:])
		// 重命名格式：old -> new，取新路径
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		code := strings.TrimSpace(xy)
		st := string(code[0])
		if code == "??" {
			st = "?"
		}
		res = append(res, FileChange{Path: path, Code: xy, Status: st})
	}
	return res, nil
}

// markIntentToAdd 对未跟踪文件打 intent-to-add 标记，使其出现在 git diff 中（整文件视为新增）
func (g *Git) markIntentToAdd(repo string) ([]string, error) {
	fcs, err := g.Porcelain(repo)
	if err != nil {
		return nil, err
	}
	var untracked []string
	for _, fc := range fcs {
		if fc.Code == "??" {
			untracked = append(untracked, fc.Path)
		}
	}
	if len(untracked) > 0 {
		args := append([]string{"add", "-N", "--"}, untracked...)
		if _, err := g.run(repo, args...); err != nil {
			return nil, err
		}
	}
	return untracked, nil
}

// WorkingDiff 未提交改动（含 staged/unstaged/未跟踪新文件）相对 HEAD 的完整 diff
func (g *Git) WorkingDiff(repo string) (string, []string, error) {
	untracked, err := g.markIntentToAdd(repo)
	if err != nil {
		return "", nil, err
	}
	out, err := g.run(repo, "diff", "HEAD", "--")
	if err != nil {
		return "", nil, err
	}
	return out, untracked, nil
}

// BranchDiff 两个分支/提交之间的树差异（草稿相对目标的累积改动）
func (g *Git) BranchDiff(repo, oldRef, newRef string) (string, error) {
	return g.run(repo, "diff", oldRef, newRef, "--")
}

// DiffFile 单个文件在工作区相对 HEAD 的 diff（文件级查看用）
func (g *Git) DiffFile(repo, path string) (string, error) {
	if _, err := g.markIntentToAdd(repo); err != nil {
		return "", err
	}
	return g.run(repo, "diff", "HEAD", "--", path)
}

// ApplyPatch 在工作区应用统一 diff 补丁（经 stdin 传入）。先 --check 校验，再实际应用。
// 用于"对话 AI 只产出 diff 文本、由工具受控落盘"的模式，使 AI 无需直接持有服务器 SSH 权限。
func (g *Git) ApplyPatch(repo, patch string) (string, error) {
	b := []byte(patch)
	if _, err := g.runInput(repo, b, "apply", "--check", "--whitespace=warn", "-"); err != nil {
		return "", fmt.Errorf("补丁无法应用（--check 未通过）: %s", err.Error())
	}
	return g.runInput(repo, b, "apply", "--whitespace=warn", "-")
}

// Commit 暂存全部改动并提交，返回新提交 SHA 与输出
func (g *Git) Commit(repo, message string) (string, string, error) {
	if _, err := g.run(repo, "add", "-A"); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp("", "aigit-commit-*.txt")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(message); err != nil {
		tmp.Close()
		return "", "", err
	}
	tmp.Close()
	out, err := g.run(repo, "commit", "-F", tmp.Name())
	if err != nil {
		return "", out, err
	}
	sha, err := g.HeadSHA(repo)
	if err != nil {
		return "", out, err
	}
	return sha, out, nil
}

// PushDraft 推送草稿分支（首次自动建立跟踪）
func (g *Git) PushDraft(repo, branch string) (string, error) {
	// 已存在上游则普通 push，否则 -u
	up := "origin/" + branch
	if err := g.RefExists(repo, up); err == nil {
		return g.run(repo, "push", "origin", branch)
	}
	return g.run(repo, "push", "-u", "origin", branch)
}

// Fetch 从 origin 拉取引用（不合并）
func (g *Git) Fetch(repo string) (string, error) {
	return g.run(repo, "fetch", "origin", "--prune")
}

// Clone 克隆仓库到 path，并检出指定分支
func (g *Git) Clone(url, path, branch string) (string, error) {
	args := []string{"clone"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	args = append(args, url, path)
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return fmt.Sprintf("已克隆 %s 到 %s", url, path), nil
}

// EnsureDraftBranch 确保草稿分支存在并切换过去：远端/本地已有则检出，否则从目标分支新建
func (g *Git) EnsureDraftBranch(repo, target, draft string) error {
	// 本地已有
	if err := g.RefExists(repo, "refs/heads/"+draft); err == nil {
		_, err := g.run(repo, "checkout", draft)
		return err
	}
	// 远端有则基于远端创建跟踪分支
	if err := g.RefExists(repo, "refs/remotes/origin/"+draft); err == nil {
		_, err := g.run(repo, "checkout", "-b", draft, "origin/"+draft)
		return err
	}
	from := target
	if target == "" {
		from = "HEAD"
	}
	_, err := g.run(repo, "checkout", "-b", draft, from)
	return err
}

// Checkout 切换到已存在分支
func (g *Git) Checkout(repo, branch string) error {
	_, err := g.run(repo, "checkout", branch)
	return err
}

// Discard 丢弃已跟踪文件的未提交改动（git reset 清掉 intent-to-add/暂存，再还原工作区）。
// 未跟踪的新文件默认保留在磁盘（避免误删），由调用方在提示中说明。
func (g *Git) Discard(repo string) (string, error) {
	if _, err := g.run(repo, "reset", "-q"); err != nil {
		return "", err
	}
	out, err := g.run(repo, "checkout", "--", ".")
	if err != nil {
		return "", err
	}
	return out, nil
}

// AheadBehind 相对 ref 的领先/落后提交数
func (g *Git) AheadBehind(repo, ref string) (int, int, error) {
	out, err := g.run(repo, "rev-list", "--left-right", "--count", "HEAD..."+ref)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("无法解析 ahead/behind: %q", out)
	}
	a, _ := strconv.Atoi(fields[0])
	b, _ := strconv.Atoi(fields[1])
	return a, b, nil
}

// Log 列出 ref 最近 n 条提交
func (g *Git) Log(repo, ref string, n int) ([]Commit, error) {
	if ref == "" {
		ref = "HEAD"
	}
	out, err := g.run(repo, "log", ref, "-n", strconv.Itoa(n),
		"--pretty=format:%H%x09%ad%x09%s", "--date=format:%Y-%m-%d %H:%M")
	if err != nil {
		return nil, err
	}
	res := []Commit{}
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		parts := strings.SplitN(ln, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		short := parts[0]
		if len(short) > 7 {
			short = short[:7]
		}
		res = append(res, Commit{SHA: parts[0], Short: short, Date: parts[1], Subject: parts[2]})
	}
	return res, nil
}
