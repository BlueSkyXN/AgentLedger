# AgentLedger Docs

AgentLedger v2 是本地 usage 统计分析器，不再是多表账本 / 审计系统。

推荐阅读顺序：

1. [Quickstart](quickstart.md): 构建、初始化、导入、报表、Web 面板和 v2 数据库合并的最小流程。
2. [User Guide](user-guide.md): 日常使用方式和指标语义。
3. [CLI Reference](cli-reference.md): 当前命令、flags 和只读 API。
4. [Data Model](data-model.md): schema v2 的三张表、字段和 upsert 规则。
5. [Architecture](architecture.md): 当前实现分层、数据流和边界。
6. [Reports and Merge](reports-and-merge.md): 报表字段、slow report 和 `.aldb` 合并行为。
7. [Configuration](configuration.md): 数据目录、配置文件和 agent path 配置。
8. [Source Adapters](source-adapters.md): 各 agent adapter 的解析边界。
9. [Privacy and Operations](privacy-and-operations.md): 本地数据、导出文件和面板截图的隐私边界。
10. [Development](development.md): 本地开发、测试和贡献注意事项。
11. [Roadmap](roadmap.md): 后续可能做的能力。

这些文档描述的是当前 Go CLI 已实现能力，而不是早期长期完整账本设计。当前 CLI 没有 cleanup/restore/pricing/workspace 命令；成本估算、timezone/currency 报表转换、export redaction、加密 raw archive 和完整 source file tracking 都属于后续能力。
