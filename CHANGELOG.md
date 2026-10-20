# Changelog

本项目的所有重要变更都记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [V0.1] - 2026-10-20

首个版本：手机端远程 AI 开发审查控制台。

### 新增

- 多工作区注册 / 克隆；
- 自动草稿分支（默认 `ai-mobile-dev`），目标分支与 `main` 永不被自动推送；
- 工作区状态：当前分支、相对远端领先 / 落后、未提交改动、草稿 vs 目标差异、提交历史；
- 可视化 git diff：行级 + 行内字符级 LCS 高亮（中文按字、英文按词切分）；
- 内置静态预览（支持 HTML 相对资源、路径式地址，防目录穿越与软链逃逸）与外部预览 URL 配置；
- commit / push（仅草稿分支）/ discard / fetch；
- 所有写操作（含 diff 刷新）在服务端串行化（互斥锁），避免并发操作同一仓库；
- 应用补丁：粘贴统一 diff，经 `git apply --check` 校验后落到草稿分支工作区，AI 无需服务器 SSH 权限；
- 访问口令（HttpOnly Cookie 会话）与操作审计日志（`logs/年/月/日/ops.log` 按日分文件）；
- 配置与接入指南示例：`config/config_demo.json` 与 `AI_AGENT_接入当前项目指南_demo.md` 随仓库分发，实际配置不入库；
- 手机优先单页界面（Bootstrap + Vue），静态资源全部内嵌，单二进制分发。
