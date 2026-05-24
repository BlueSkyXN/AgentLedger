# AgentLedger Docs

本目录保存面向公开仓库的项目文档。`local/` 下的设计草稿、扫描实验数据、私有数据库和审计记录只作为本机工作材料，不直接发布。

## 推荐阅读顺序

1. [Quickstart](quickstart.md): 从源码构建、初始化、导入、报表和跨设备合并的最小流程。
2. [CLI Reference](cli-reference.md): 当前 Cobra CLI 的真实命令、flags 和未实现命令边界。
3. [Configuration](configuration.md): 默认 TOML 配置、当前生效键和预留键。
4. [Source Adapters](source-adapters.md): Claude Code、Codex、Gemini CLI、Qwen 的发现路径和解析口径。
5. [Data Model](data-model.md): SQLite schema、核心事件表和 fingerprint 去重策略。
6. [Reports and Merge](reports-and-merge.md): 7 类报表、JSON 输出和 `.aldb` merge 语义。
7. [Privacy and Operations](privacy-and-operations.md): 本地隐私边界、数据库敏感性和维护命令。
8. [Development](development.md): 本地开发、验证命令、代码结构和贡献前检查。
9. [Roadmap](roadmap.md): 来自设计稿但尚未落地的 planned/reserved 能力。

## 补充文档

- [Architecture](architecture.md): 当前 Go 实现的模块结构、数据流、schema、指纹去重和适配器边界。
- [User Guide](user-guide.md): 一体化用户指南，内容与 quickstart/config/report/operation 文档保持同一事实口径。

## 当前实现边界

这些文档描述的是当前 Go CLI 已实现能力，而不是 `local/AgentLedger_design.md` 中的长期完整设计。当前 CLI 没有 `cleanup`/`restore` 命令；成本估算、timezone/currency 报表转换、export redaction、加密 raw archive 和完整 source file tracking 都属于 roadmap 或 schema/config 预留能力。
