package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var indexHTML []byte

//go:embed web/assets
var assetsFS embed.FS

// Server HTTP 服务，聚合存储与 git 能力
type Server struct {
	store *Store
	git   *Git
	mu    sync.Mutex
}

// WsStatus 工作区运行时状态
type WsStatus struct {
	Workspace
	CurrentBranch      string       `json:"currentBranch"`
	Clean              bool         `json:"clean"`
	WorkingFiles       []FileChange `json:"workingFiles"`
	Untracked          []string     `json:"untracked"`
	Ahead              int          `json:"ahead"`
	Behind             int          `json:"behind"`
	BranchFiles        []FileChange `json:"branchFiles"`
	Commits            []Commit     `json:"commits"`
	InternalPreviewURL string       `json:"internalPreviewUrl"`
	Errors             []string     `json:"errors"`
}

// StatusResponse 全局状态
type StatusResponse struct {
	Phase      string     `json:"phase"`
	BasePath   string     `json:"basePath"`
	Workspaces []WsStatus `json:"workspaces"`
	Current    *WsStatus  `json:"current"`
}

func init() {
	// 强制静态资源 MIME，不依赖服务器 /etc/mime.types（否则 .css/.js 可能被判 text/plain）
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".html", "text/html; charset=utf-8")
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	baseDir := filepath.Dir(exe)
	configDir := filepath.Join(baseDir, "config")
	logsDir := filepath.Join(baseDir, "logs")
	store, err := NewStore(configDir, logsDir)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{store: store, git: NewGit()}
	cfg := store.Config()
	prefix := cfg.BasePath

	mux := http.NewServeMux()
	// 静态资源与首页
	mux.HandleFunc(prefix+"/", s.handleRoot)
	// 鉴权 / 会话
	mux.HandleFunc(prefix+"/api/login", s.handleLogin)
	mux.HandleFunc(prefix+"/api/logout", s.handleLogout)
	// 状态与 diff
	mux.HandleFunc(prefix+"/api/status", s.auth(s.handleStatus))
	mux.HandleFunc(prefix+"/api/diff", s.auth(s.handleDiff))
	// 工作区管理
	mux.HandleFunc(prefix+"/api/workspace/add", s.auth(s.handleWorkspaceAdd))
	mux.HandleFunc(prefix+"/api/workspace/remove", s.auth(s.handleWorkspaceRemove))
	// 提交 / 推送 / 丢弃 / 拉取
	mux.HandleFunc(prefix+"/api/commit", s.auth(s.handleCommit))
	mux.HandleFunc(prefix+"/api/push", s.auth(s.handlePush))
	mux.HandleFunc(prefix+"/api/discard", s.auth(s.handleDiscard))
	mux.HandleFunc(prefix+"/api/patch", s.auth(s.handlePatch))
	mux.HandleFunc(prefix+"/api/fetch", s.auth(s.handleFetch))
	// 内置预览
	mux.HandleFunc(prefix+"/api/preview", s.auth(s.handlePreview))
	// 日志
	mux.HandleFunc(prefix+"/api/log", s.auth(s.handleLog))

	addr := cfg.ListenAddr
	log.Printf("AI Git Developer 已启动: %s（配置目录 %s，日志目录 %s）", addr, configDir, logsDir)
	log.Printf("工作区根目录: %s", cfg.WorkspaceRoot)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// ---------- 首页与静态资源 ----------

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, s.store.Config().BasePath)
	if p == "" || p == "/" || p == "/index.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
		return
	}
	if strings.HasPrefix(p, "/assets/") {
		sub, err := fs.Sub(assetsFS, "web/assets")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// 重写为相对 assets 根的路径，避免依赖外层是否已剥前缀
		rel := strings.TrimPrefix(p, "/assets/")
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + rel
		http.FileServer(http.FS(sub)).ServeHTTP(w, r2)
		return
	}
	http.NotFound(w, r)
}

// ---------- 鉴权 ----------

func sessionToken(pw string) string {
	h := sha256.Sum256([]byte("ai-git-developer::" + pw))
	return hex.EncodeToString(h[:])
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pw := s.store.Config().Password
		if pw == "" {
			next(w, r)
			return
		}
		c, err := r.Cookie("aigd_session")
		if err == nil && c.Value == sessionToken(pw) {
			next(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "未登录或登录已过期，请重新输入口令")
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	pw := s.store.Config().Password
	if pw != "" && body.Password != pw {
		writeErr(w, http.StatusUnauthorized, "口令错误")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "aigd_session",
		Value:    sessionToken(pw),
		Path:     s.store.Config().BasePath + "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "aigd_session", Value: "", Path: s.store.Config().BasePath + "/", MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- 状态 ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	resp := StatusResponse{Phase: "idle", BasePath: cfg.BasePath}
	for _, ws := range cfg.Workspaces {
		resp.Workspaces = append(resp.Workspaces, s.buildWs(ws, r))
	}
	if name := r.URL.Query().Get("ws"); name != "" {
		if ws, ok := s.store.GetWorkspace(name); ok {
			cur := s.buildWs(ws, r)
			resp.Current = &cur
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) buildWs(ws Workspace, r *http.Request) WsStatus {
	st := WsStatus{Workspace: ws, WorkingFiles: []FileChange{}, BranchFiles: []FileChange{}, Commits: []Commit{}, Untracked: []string{}, Errors: []string{}}
	info, err := os.Stat(ws.Path)
	if err != nil || !info.IsDir() {
		st.Errors = append(st.Errors, "工作区路径不存在或不是目录: "+ws.Path)
		return st
	}
	if err := s.git.RefExists(ws.Path, "HEAD"); err != nil {
		st.Errors = append(st.Errors, "该路径不是 git 仓库（缺少 HEAD）")
		return st
	}
	cb, err := s.git.CurrentBranch(ws.Path)
	if err != nil {
		st.Errors = append(st.Errors, "读取当前分支失败: "+err.Error())
		return st
	}
	st.CurrentBranch = cb

	fcs, err := s.git.Porcelain(ws.Path)
	if err != nil {
		st.Errors = append(st.Errors, err.Error())
		return st
	}
	st.WorkingFiles = fcs
	st.Clean = len(fcs) == 0

	// ahead/behind 相对 origin/<draft>
	up := "origin/" + ws.DraftBranch
	if err := s.git.RefExists(ws.Path, up); err == nil {
		if a, b, e := s.git.AheadBehind(ws.Path, up); e == nil {
			st.Ahead, st.Behind = a, b
		}
	}

	// 草稿分支相对目标分支的文件差异
	target := ws.TargetBranch
	if target == "" {
		target = "main"
	}
	if err := s.git.RefExists(ws.Path, target); err == nil {
		draft := ws.DraftBranch
		if err := s.git.RefExists(ws.Path, draft); err == nil {
			if out, e := s.git.run(ws.Path, "diff", "--name-status", target, draft); e == nil {
				st.BranchFiles = parseNameStatus(out)
			}
			if cs, e := s.git.Log(ws.Path, draft, 20); e == nil {
				st.Commits = cs
			}
		}
	} else {
		st.Errors = append(st.Errors, "目标分支 "+target+" 不存在于本地，可先点【拉取】")
	}

	// 记录未跟踪文件（用于提示）
	for _, fc := range fcs {
		if fc.Code == "??" {
			st.Untracked = append(st.Untracked, fc.Path)
		}
	}

	// 内置预览地址
	pf := ws.PreviewFile
	if pf == "" {
		pf = "index.html"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	st.InternalPreviewURL = fmt.Sprintf("%s://%s%s/api/preview?ws=%s&p=%s",
		scheme, r.Host, s.store.Config().BasePath, ws.Name, pf)
	return st
}

// parseNameStatus 解析 git diff --name-status 输出
func parseNameStatus(out string) []FileChange {
	res := []FileChange{}
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		code := f[0]
		path := f[len(f)-1] // 重命名时取最后一列（新路径）
		st := string(code[0])
		if len(code) > 1 && code[0] == 'R' {
			st = "R"
		}
		res = append(res, FileChange{Path: path, Code: code, Status: st})
	}
	return res
}

// ---------- Diff ----------

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("ws")
	ws, ok := s.store.GetWorkspace(name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区: "+name)
		return
	}
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "working"
	}
	var text string
	switch view {
	case "branch":
		target := ws.TargetBranch
		if target == "" {
			target = "main"
		}
		out, err := s.git.BranchDiff(ws.Path, target, ws.DraftBranch)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		text = out
	default:
		out, _, err := s.git.WorkingDiff(ws.Path)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		text = out
	}
	files := ParseDiff(text)
	if f := r.URL.Query().Get("file"); f != "" {
		var one []FileDiff
		for _, fd := range files {
			if fd.Path == f || fd.OldPath == f {
				one = append(one, fd)
			}
		}
		files = one
	}
	if files == nil {
		files = []FileDiff{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "view": view})
}

// ---------- 工作区添加 / 克隆 ----------

func (s *Server) handleWorkspaceAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		RepoURL     string `json:"repoUrl"`
		Target      string `json:"target"`
		Draft       string `json:"draft"`
		PreviewFile string `json:"previewFile"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "请填写工作区名称")
		return
	}
	target := strings.TrimSpace(body.Target)
	if target == "" {
		target = "main"
	}
	draft := strings.TrimSpace(body.Draft)
	if draft == "" {
		draft = "ai-mobile-dev"
	}
	preview := strings.TrimSpace(body.PreviewFile)
	if preview == "" {
		preview = "index.html"
	}
	path := strings.TrimSpace(body.Path)
	if path == "" {
		path = filepath.Join(s.store.Config().WorkspaceRoot, name)
	}

	// 路径不存在则克隆
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		if strings.TrimSpace(body.RepoURL) == "" {
			writeErr(w, http.StatusBadRequest, "路径不存在且未提供仓库地址，无法克隆")
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		msg, err := s.git.Clone(strings.TrimSpace(body.RepoURL), path, target)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.store.Log(fmt.Sprintf("%s [clone] %s → %s", nowStr(), body.RepoURL, path))
		_ = msg
	}

	if err := s.git.EnsureDraftBranch(path, target, draft); err != nil {
		writeErr(w, http.StatusInternalServerError, "创建/切换草稿分支失败: "+err.Error())
		return
	}
	ws := Workspace{
		Name: name, Path: path, RepoURL: strings.TrimSpace(body.RepoURL),
		TargetBranch: target, DraftBranch: draft, PreviewFile: preview,
	}
	if err := s.store.UpsertWorkspace(ws); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.Log(fmt.Sprintf("%s [workspace-add] %s（path=%s, draft=%s）", nowStr(), name, path, draft))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace": ws})
}

func (s *Server) handleWorkspaceRemove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if err := s.store.RemoveWorkspace(body.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.Log(fmt.Sprintf("%s [workspace-remove] %s（仅移除注册，磁盘文件保留）", nowStr(), body.Name))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- Commit / Push / Discard / Fetch ----------

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WS      string `json:"ws"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	ws, ok := s.store.GetWorkspace(body.WS)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区")
		return
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		writeErr(w, http.StatusBadRequest, "提交备注不能为空")
		return
	}
	cb, _ := s.git.CurrentBranch(ws.Path)
	if cb != ws.DraftBranch {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("当前在 %s 分支，请先切换到草稿分支 %s 再提交", cb, ws.DraftBranch))
		return
	}
	clean, _, _ := s.git.IsClean(ws.Path)
	if clean {
		writeErr(w, http.StatusBadRequest, "没有可提交的改动")
		return
	}
	sha, out, err := s.git.Commit(ws.Path, body.Message)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "commit 失败: "+err.Error()+out)
		return
	}
	s.store.Log(fmt.Sprintf("%s [commit] %s %s", nowStr(), ws.Name, sha[:min(7, len(sha))]))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sha": sha, "output": out})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WS string `json:"ws"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	ws, ok := s.store.GetWorkspace(body.WS)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区")
		return
	}
	cb, _ := s.git.CurrentBranch(ws.Path)
	if cb != ws.DraftBranch {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("只允许推送草稿分支 %s（当前 %s）", ws.DraftBranch, cb))
		return
	}
	out, err := s.git.PushDraft(ws.Path, ws.DraftBranch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "push 失败: "+err.Error())
		return
	}
	s.store.Log(fmt.Sprintf("%s [push] %s → origin/%s", nowStr(), ws.Name, ws.DraftBranch))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (s *Server) handleDiscard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WS string `json:"ws"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	ws, ok := s.store.GetWorkspace(body.WS)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区")
		return
	}
	out, err := s.git.Discard(ws.Path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "丢弃失败: "+err.Error())
		return
	}
	s.store.Log(fmt.Sprintf("%s [discard] %s（未跟踪新文件保留）", nowStr(), ws.Name))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out, "message": "已丢弃已跟踪文件的改动；未跟踪的新文件仍保留在磁盘"})
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WS    string `json:"ws"`
		Patch string `json:"patch"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body)
	ws, ok := s.store.GetWorkspace(body.WS)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区")
		return
	}
	if strings.TrimSpace(body.Patch) == "" {
		writeErr(w, http.StatusBadRequest, "补丁内容为空")
		return
	}
	cb, _ := s.git.CurrentBranch(ws.Path)
	if cb != ws.DraftBranch {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("只允许在草稿分支 %s 应用补丁（当前 %s）", ws.DraftBranch, cb))
		return
	}
	out, err := s.git.ApplyPatch(ws.Path, body.Patch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.Log(fmt.Sprintf("%s [patch] %s（应用补丁到工作区）", nowStr(), ws.Name))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out, "message": "补丁已应用到工作区，可在【未提交改动】查看、预览并 commit"})
}

func (s *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WS string `json:"ws"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	ws, ok := s.store.GetWorkspace(body.WS)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区")
		return
	}
	out, err := s.git.Fetch(ws.Path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch 失败: "+err.Error())
		return
	}
	s.store.Log(fmt.Sprintf("%s [fetch] %s", nowStr(), ws.Name))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

// ---------- 内置预览（静态文件） ----------

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.store.GetWorkspace(r.URL.Query().Get("ws"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知工作区")
		return
	}
	rel := r.URL.Query().Get("p")
	if rel == "" {
		rel = ws.PreviewFile
	}
	if rel == "" {
		rel = "index.html"
	}
	// 防目录穿越
	base := filepath.Clean(ws.Path)
	full := filepath.Join(base, filepath.Clean("/"+rel))
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) && full != base {
		writeErr(w, http.StatusBadRequest, "非法路径")
		return
	}
	b, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(full))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(b)
}

// ---------- 日志 ----------

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	lines, err := s.store.ReadLog(300)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// ---------- 辅助 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
