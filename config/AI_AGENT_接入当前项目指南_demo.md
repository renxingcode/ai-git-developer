# AI Git Developer 接入指南（面向 AI 助手）

> 本文件供 AI 助手（如豆包）阅读后，在用户服务器上接管前端项目开发使用。含内部信息，请勿公开。
>
> 本文档是示例文档，其中提及的 IP、网址和密码等敏感信息均为示例数据，请 AI 助手根据用户的描述替换为用户的真实信息。

## 0. 一句话说明
这是一个自托管的「手机远程开发控制台」：你（AI）负责改代码，用户用手机浏览器在工具页查看 diff、预览效果、决定提交；你不需要持有服务器 SSH 权限（推荐补丁模式），也可经 SSH 直接操作。

## 1. 环境速览（内部）
- 工具页：http://123.45.67.89/ai-git-developer/
- 访问口令：your_strong_password
- 服务器：root@123.45.67.89（仅密钥登录，PasswordAuthentication=no）
- 工具服务：systemd ai-git-developer.service，监听 127.0.0.1:8767，nginx 反向代理 /ai-git-developer/
- 工具仓库（源码）：~/code/ai-git-developer（远端 gitee，main）
- 工作区配置：~/code/ai-git-developer/config/config.json（改后 systemctl restart ai-git-developer）
- 操作日志：logs/年/月/日/ops.log（按日分文件）

## 2. 分支模型与安全红线（最高优先级）
- 每个项目一个工作区，配置项：path / targetBranch（正式分支）/ draftBranch（开发草稿分支）/ previewFile / previewUrl
- 只能操作、提交、推送 draftBranch（草稿分支），其他一切不动
- 严禁 push 到 targetBranch、main 或任何公开分支；严禁在正式分支 commit
- 拿不准先问用户，绝不擅自 push
- 若项目尚无草稿分支，询问用户创建（git checkout -b <draftBranch>）

## 3. 两种工作模式
A. SSH 直改（当前云电脑已配好）：直接编辑文件，用户工具页看 diff/预览，用户决定提交
B. 补丁模式（推荐、最安全）：只输出 unified diff，应用走工具接口
   - POST /api/patch  body {"ws":"<工作区名>","patch":"<统一diff>"}
   - 先本地校验：git apply --check（服务器上：git -C <path> apply --check <补丁文件>）
   - 仅草稿分支可应用；应用后可 discard 还原

## 4. 标准工作流（每轮开发）
1. 看状态：git -C <path> status --short && git -C <path> branch --show-current && git -C <path> log --oneline -5
2. 读相关文件（必须先读再改，别凭印象）
3. 小步改动，一次一个需求
4. 自检：HTML/JS 语法、引用路径、Vue 字段一致；curl -sI 预览 URL 确认 200
5. 汇报：改动文件、改动点、如何验证；提醒用户去工具页看 diff + 预览
6. 不自动 commit/push；用户说「提交」才操作（可在工具页点，也可执行 commit 到草稿分支）

## 5. 常用命令
- 查看改动：git -C <path> diff
- 查看状态：git -C <path> status --short
- 预览检查：curl -sI http://123.45.67.89/<项目>/...
- 服务器 Go 编译：/usr/local/go/bin/go build
- SSH 复用（避免限流）：ssh -o ControlMaster=yes -o ControlPath=/tmp/aigmux -o ControlPersist=600 -fN root@123.45.67.89

## 6. 用户偏好（务必遵守）
- 不写自动化测试，功能由用户手动点 + 真机验证
- 数据正确性问题必须当期解决；样式小尾巴可留迭代
- commit 备注极简（4-5 条短 bullet）
- 改完代码不自动 commit，用户自行 git diff 后提交，可能合并多次改动

## 7. 接入新项目示例
- 开发仓：/root/code/your_project
- 预览入口（nginx配置）：http://123.45.67.89/your_project/html/index.html

接入步骤：
1. 在 config.json workspaces 新增（分支名以用户确认为准；路径须写绝对路径，不支持 `~`）：
```
{
   "name": "your_project",
   "path": "/root/code/your_project",
   "repoUrl": "git@gitee:yourname/your_project.git",
   "targetBranch": "main",
   "draftBranch": "ai-mobile-dev",
   "previewFile": "html/index.html",
   "previewUrl": "http://123.45.67.89/your_project/html/index.html"
}
```
2. systemctl restart ai-git-developer
3. 之后按第 4 节工作流操作


## 8. 常见坑
- 短时间多次 SSH 建连会触发 TCP 临时禁闭（ping 通、22/80 无响应，几分钟自愈），用 ControlMaster 复用连接
- 若云电脑 sandbox 重建，公钥会变：请用户把新公钥发给工具负责人加入 authorized_keys
- 服务器 go 不在 PATH，用 /usr/local/go/bin/go
