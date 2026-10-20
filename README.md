# AI Git Developer

> 自托管、手机操作优先的「远程 AI 开发控制台」。在手机上与 AI 沟通，让 AI 直接操作服务器上的前端项目工作区，手机网页查看可视化 git diff、预览页面，一键 commit / push 草稿分支——全程不需要电脑，随时随地改代码。

## 关于本项目

**这个项目能解决什么问题？**

你可以随时随地在手机上和 AI（豆包 / 或其他任意对话模型）聊出一个前端改动，但代码在服务器上、电脑不在身边。本工具把「工作区改动 → 代码级 diff 审查 → 页面预览 → 提交 / 推送」整条链路搬到手机浏览器：

- **AI 负责写代码**：对话 AI 通过本工具的 API（或直接在服务器工作区）改动文件；
- **你负责拍板**：手机上看行级 + 行内字符级高亮的 diff，打开页面预览确认；
- **一键落盘**：满意就 commit、push 到自动草稿分支做云备份；回家后，在电脑上 pull 下来在 IDE 复审，再合并到正式分支。

对话 AI 与工具解耦、模型无关：你用哪个聊天助手都行，工具只守好工作区、diff、预览和提交。

## 工作流

```
手机对话 AI  ──改代码──▶  服务器工作区（自动草稿分支 ai-mobile-dev）
                              │
              手机网页 ◀──查看── 可视化 git diff（行级 + 字符级高亮）
              手机网页 ◀──打开── 页面预览
                              │
                     满意 → commit → push 草稿分支（云备份）
                              │
        电脑 pull 草稿 → IDE 复审 → 合并到目标分支
```

> - AI 改动代码有两条路：SSH 直连服务器工作区，或只产出统一 diff 补丁、由你在网页【应用补丁】粘贴落盘（无需 SSH，更安全）。
> - 在电脑上预览改动、合并：`git fetch && git checkout <草稿分支> && git pull`，IDE 复审后合并到目标分支。

## 特性

- **零依赖单二进制**：Go 编译，内嵌前端与静态资源，上传即可运行；
- **可视化 git diff**：解析统一 diff，行级增删改 + 行内字符级 LCS 高亮（中文按字、英文按词），新增/删除/重命名/二进制均可识别；
- **自动草稿分支**：从目标分支自动创建并切换到 `<draft>`（默认 `ai-mobile-dev`），所有提交只落在草稿分支；
- **应用补丁**：对话 AI 只产出统一 diff 文本，在网页粘贴即可落到草稿分支工作区（先 `git apply --check` 校验、不自动提交），AI 无需任何服务器 SSH 权限；
- **内置预览服务**：直接通过工具安全地浏览工作区静态页面，无需额外配静态站点；
- **多工作区**：一个控制台管理多个前端项目，支持注册已有仓库或按地址克隆；
- **安全约束**：可设访问口令；写操作只作用于草稿分支，目标分支与 `main` 不自动推；预览接口防目录穿越；
- **手机优先界面**：Bootstrap + Vue，本地资源加载，针对小屏和触控优化。

## 快速开始

```bash
# 克隆（Gitee）
git clone https://gitee.com/rxbook/ai-git-developer.git
# 或 GitHub 镜像
# git clone https://github.com/renxingcode/ai-git-developer.git
cd ai-git-developer

# 手动配置 config.json（正式配置不入库，注意保持文件权限 600）
cd config && cp config_demo.json config.json
chmod 600 config.json
vim config.json
cd ..

# 编译（需 Go 1.23+）
go build -o ai-git-developer .

# 直接运行
./ai-git-developer
```

打开浏览器访问，在【添加 / 克隆工作区】填入项目路径（或仓库地址）即可开始。

生产环境（公网、systemd、nginx、口令）部署见 [部署运维文档](docs/DEPLOY.md)；部署后的 `config.json` 与《AI 接入指南》既可手工维护，也可直接交给 AI 代劳，见文档中「让 AI 接手维护配置与接入指南」。

## 文档

- [设计文档](docs/DESIGN.md)：项目背景、核心概念（草稿分支模型、AI 到达服务器的两条路径）、功能规格、关键设计决策与完整安全模型；
- [部署运维](docs/DEPLOY.md)：编译、配置参考、systemd / nginx 部署、口令、升级与排查；
- [路线图](docs/ROADMAP.md)：后续版本规划；
- [变更记录](CHANGELOG.md)：各版本功能与变更历史。

## 仓库地址

- Gitee：https://gitee.com/rxbook/ai-git-developer
- GitHub：https://github.com/renxingcode/ai-git-developer

欢迎 Issue / Pull Request，也欢迎 Fork、Star。

## 安全要点

- 公网部署务必设置访问口令，并置于 nginx（HTTPS）反向代理之后；
- 工具不内置任何云端调用，所有数据（配置、日志）都保存在服务器本地 `config/` 与 `logs/` 目录。

## 技术栈

Go（标准库为主，`os/exec` 调用 git）+ 原生 `net/http` + 内嵌 Bootstrap / Vue 单页界面。

## 免责声明

本工具是个人开源的小项目，不内置任何 AI 模型，也不绑定、不代理任何 AI 服务，所有代码改动均由你所使用的对话 AI 或你本人发起；工具只提供工作区状态查看、可视化 diff、页面预览，以及受限的补丁应用与 git 提交能力。

请自行评估所用 AI 工具的安全性与可信度，谨慎授予其访问服务器的权限。

凡因阅读本文、使用本工具或依照本工具引导操作，导致所使用的 AI 或其他第三方窃取、篡改、损坏服务器数据，或造成任何直接、间接损失，作者概不承担任何责任。

如果你无法接受这些风险，请不要使用本工具。

## License

MIT
