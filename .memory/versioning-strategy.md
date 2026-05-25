---
name: versioning-strategy
description: 版本号管理策略 — v{major}.{minor}.{patch}，patch由Claude管理，major.minor由用户决策
type: feedback
---

## 版本号管理策略

格式: `vX.Y.ZZ`（如 v0.7.00）

- **前两位 (vX.Y)**: 大版本号，由用户决策何时升级
- **后两位 (ZZ)**: 补丁号，每次 commit（bug修复或小功能）后自动 +1
- **示例**: v0.7.00 → v0.7.01 → v0.7.02 → ...

### 操作流程
1. 每次 commit 后，patch 号 +1
2. 更新 `internal/version/version.go` 中的版本号
3. git tag 打对应版本标签
