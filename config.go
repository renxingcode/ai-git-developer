package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Workspace 一个被管理的前端项目工作区。工具不绑定任何具体项目，全部运行时注册。
type Workspace struct {
	Name         string `json:"name"`         // 显示名，同时作为唯一标识
	Path         string `json:"path"`         // 工作区绝对路径（git 仓库根）
	RepoURL      string `json:"repoUrl"`      // 克隆来源地址（注册已有目录时可空）
	TargetBranch string `json:"targetBranch"` // 目标分支：最终要合并进去的分支，如 main
	DraftBranch  string `json:"draftBranch"`  // 草稿分支：AI 在此开发，如 ai-mobile-dev
	PreviewFile  string `json:"previewFile"`  // 预览入口相对路径，如 index.html
	PreviewURL   string `json:"previewUrl"`   // 完整预览地址（可空，空则用工具内置预览）
}

// Config 工具全局配置
type Config struct {
	ListenAddr    string      `json:"listenAddr"`    // 监听地址，默认 127.0.0.1:8767
	Port          int         `json:"port"`          // 监听端口（兼容字段）
	Password      string      `json:"password"`      // 访问口令，空表示不鉴权
	BasePath      string      `json:"basePath"`      // 反代前缀，如 /ai-git-developer
	WorkspaceRoot string      `json:"workspaceRoot"` // 克隆工作区的默认根目录
	Workspaces    []Workspace `json:"workspaces"`    // 已注册工作区
}

// Store 负责配置持久化与操作日志（config/ 与 logs/ 分目录存放）
type Store struct {
	mu     sync.Mutex
	cfgDir string
	logDir string
	cfg    *Config
}

// NewStore 打开（或初始化）配置目录与日志目录
func NewStore(cfgDir, logDir string) (*Store, error) {
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{cfgDir: cfgDir, logDir: logDir, cfg: defaultConfig()}
	if b, err := os.ReadFile(filepath.Join(cfgDir, "config.json")); err == nil {
		_ = json.Unmarshal(b, s.cfg)
		if s.cfg.ListenAddr == "" {
			s.cfg.ListenAddr = defaultConfig().ListenAddr
		}
	}
	return s, nil
}

func defaultConfig() *Config {
	return &Config{
		ListenAddr:    "127.0.0.1:8767",
		Port:          8767,
		WorkspaceRoot: "/root/code/ai-workspaces",
		Workspaces:    []Workspace{},
	}
}

// Config 返回配置副本
func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.cfg
}

// SaveConfig 整体保存配置
func (s *Store) SaveConfig(c *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = c
	return s.writeConfigLocked()
}

func (s *Store) writeConfigLocked() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cfgDir, "config.json"), b, 0o600)
}

// UpsertWorkspace 添加或更新工作区（按 Name 去重）
func (s *Store) UpsertWorkspace(ws Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws.Name = strings.TrimSpace(ws.Name)
	found := false
	for i := range s.cfg.Workspaces {
		if s.cfg.Workspaces[i].Name == ws.Name {
			s.cfg.Workspaces[i] = ws
			found = true
			break
		}
	}
	if !found {
		s.cfg.Workspaces = append(s.cfg.Workspaces, ws)
	}
	return s.writeConfigLocked()
}

// RemoveWorkspace 从配置移除工作区（不删除磁盘文件）
func (s *Store) RemoveWorkspace(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cfg.Workspaces[:0]
	for _, w := range s.cfg.Workspaces {
		if w.Name != name {
			out = append(out, w)
		}
	}
	s.cfg.Workspaces = out
	return s.writeConfigLocked()
}

// GetWorkspace 按名称取工作区
func (s *Store) GetWorkspace(name string) (Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.cfg.Workspaces {
		if w.Name == name {
			return w, true
		}
	}
	return Workspace{}, false
}

// Log 追加一行操作日志到 logs/年/月/日/ops.log（按日分文件）
func (s *Store) Log(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.logDir, time.Now().Format("2006/01/02"), "ops.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// ReadLog 读取最近 n 行（返回顺序为最新在前）。日志按天分文件存放，
// 当天不足时自动向前跨天回溯，连续 7 天无日志文件则停止。
func (s *Store) ReadLog(n int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ordered []string // 时间正序（旧 → 新）
	miss := 0
	for day := time.Now(); miss < 7 && len(ordered) < n; day = day.AddDate(0, 0, -1) {
		p := filepath.Join(s.logDir, day.Format("2006/01/02"), "ops.log")
		b, err := os.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			miss++
			continue
		}
		miss = 0
		var lns []string
		for _, ln := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if ln != "" {
				lns = append(lns, ln)
			}
		}
		if len(lns) == 0 {
			continue
		}
		merged := make([]string, 0, len(lns)+len(ordered))
		merged = append(merged, lns...)
		merged = append(merged, ordered...)
		ordered = merged
	}
	if len(ordered) > n {
		ordered = ordered[len(ordered)-n:]
	}
	// 反转，最新在前
	for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	}
	if ordered == nil {
		ordered = []string{}
	}
	return ordered, nil
}
