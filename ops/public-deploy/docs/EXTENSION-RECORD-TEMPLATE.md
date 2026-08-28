# 二开登记模板

每一项新增或修改的站点二开，都必须在进入生产候选前复制一份记录并填写完整。文件名建议使用
`YYYYMMDD-简短名称.md`，放在本目录的 `extensions/` 下；登记文件和代码必须在同一个提交中进入发布分支。

## 登记内容

```text
Capability ID:
Business purpose:
Backend/frontend files:
Routes or jobs:
Database migration/data dependency:
Environment-variable/config dependency:
Billing impact: none | internal cost | user charge
Scheduling impact: none | score | account selection
Client protocol impact: none | OpenAI Responses | Chat Completions | Anthropic | other
Upstream version/base commit:
Tests/fixtures:
Image smoke evidence:
First release manifest:
Rollback note:
Owner/status:
```

## Rules

1. 不把密钥、Cookie、Bearer token、密码、原始请求体或用户内容写入登记。
2. “文件已修改”不等于“功能已进入镜像”；必须记录测试和镜像级 smoke 证据。
3. 改动路由、计费、调度、探针、协议转换或数据库时，至少增加一个回归测试。
4. 上游更新时先把登记项逐项标为 `保留`、`冲突待处理` 或 `退休`，未决项不能发布。
5. 登记文件缺失、测试状态为 unknown、镜像源码标签不匹配，都视为发布阻断。
