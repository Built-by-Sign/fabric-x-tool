---
description: "Docker 镜像发版：分析 commit 生成发版说明，打 tag，构建推送镜像到 ghcr.io/built-by-sign/fabric-x-tool。触发方式：/release 或 /release v0.1.0"
user_invocable: true
---

# Release - fabric-x-tool Docker 镜像发版

## 输入

用户可能提供版本号作为参数（如 `/release v0.1.0`），也可能不提供（`/release`）。

镜像仓库：`ghcr.io/built-by-sign/fabric-x-tool`
Tag 格式：`vX.Y.Z`（裸版本号，无前缀）

**版本号唯一来源是 git tag**——先打 tag，构建步骤从 `git describe --tags --abbrev=0` 读取版本号传给 `make build-ghcr`。

## 步骤

### 1. 检查前置条件

- 运行 `git status --porcelain` 确认工作目录干净，如有未提交修改则**警告用户**并等待确认
- 运行 `git describe --tags --abbrev=0` 获取上次 tag
- 运行 `git log {last_tag}..HEAD --oneline` 获取变更列表
- 如果没有新 commit，提示用户无需发版并退出

### 2. 确定新版本号

- 如果用户 `/release vX.Y.Z` 指定了版本号 → 使用该版本号
- 如果用户未指定 → 根据变更类型建议：
  - 有 breaking change 或大功能 → minor 升级（如 v0.0.8 → v0.1.0）
  - 仅 fix/chore/refactor → patch 升级（如 v0.0.8 → v0.0.9）
- 展示建议版本号，等待用户确认或修改

### 3. 生成发版说明

分析所有 commit，按类型分类生成结构化发版说明，格式：

```
## {version}

### 新功能 (Features)
- 描述...

### 修复 (Fixes)
- 描述...

### 重构 (Refactors)
- 描述...

### 其他
- 描述...
```

同步更新根目录的 `CHANGELOG.md`（若存在）：在文件顶部插入新版本条目，保留历史。

### 4. 展示并确认

向用户展示：
- 上次版本 → 新版本
- 完整发版说明
- CHANGELOG.md 的 diff（若有更新）
- 将要执行的命令列表

**等待用户明确确认后才能继续。**

### 5. 执行发版

用户确认后，按顺序执行：

```bash
# 1. 如果更新了 CHANGELOG.md，先 commit
git add CHANGELOG.md
git commit -m "chore: release {version}"

# 2. 打 tag（版本号的唯一来源）
git tag -a {version} -m "{release_notes}"

# 3. 构建并推送镜像到 ghcr.io（从 tag 读取版本号，会同时 push {version} 和 latest）
make build-ghcr VERSION=$(git describe --tags --abbrev=0)

# 4. 推送 commit 和 tag 到远程 git 仓库（--follow-tags 一并推送指向 HEAD 的 annotated tag）
git push --follow-tags origin HEAD
```

发版完成的判定：**远程 git 仓库有 tag，且 ghcr.io 有对应镜像，两者缺一不可。**

**重要：如果 `make build-ghcr` 失败，立即停止，删除本地 tag（`git tag -d {version}`），reset 掉 CHANGELOG 的 commit（若有），报告错误。不要推送 tag。**

如果 `git push` 失败（网络/权限问题）：镜像已推送到 ghcr.io 但远程 git 无 tag，告知用户手动重试 `git push --follow-tags origin HEAD`，不要回滚镜像。

## 注意事项

- tag message 使用中文
- 发版说明基于 commit message 分析，不要编造不存在的变更
- 构建失败时必须回滚本地 tag 和 commit，绝不推送失败的版本
- `make build-ghcr` 会同时构建 linux/amd64 和 linux/arm64，约需 15 分钟，使用 `run_in_background` 并定期检查进度
- 构建前提示用户确认已 `docker login ghcr.io`
