# AgentLedger v3 文档

AgentLedger v3 是本地 Session usage 统计器，不是审计账本、远程同步服务或设备管理系统。

1. [Quickstart](quickstart.md)：构建、初始化、导入、报表和面板。
2. [User Guide](user-guide.md)：日常使用路径和结果解释。
3. [Configuration](configuration.md)：当前有效配置字段。
4. [Source Adapters](source-adapters.md)：五个来源的 usage、Session、identity 与 accounting 口径。
5. [Data Model](data-model.md)：schema v3 三表、identity v2 与 reconcile。
6. [Architecture](architecture.md)：模块边界和数据流。
7. [CLI Reference](cli-reference.md)：全部命令、flags 和退出语义。
8. [Reports and Merge](reports-and-merge.md)：报表、即时价格与 `.aldb` merge。
9. [Privacy and Operations](privacy-and-operations.md)：隐私、只读 API、export、vacuum。
10. [Database Rebuild](database-rebuild.md)：v2 baseline、v3 candidate、差异报告、切换与回滚。
11. [Local Preview](local-preview.md)：API/Web 本机验收。
12. [Development](development.md)：开发、测试和 CI。
13. [Roadmap](roadmap.md)：明确 non-goals 与后续候选。

实现事实以 `internal/db/schema.go`、`internal/fingerprint`、`internal/adapters`、`internal/analytics`、`internal/report`、`internal/control` 和 CLI `--help` 为准。
