# 迭代 1 设计：Vault 与新领域模型

本文定义迭代 1 的技术设计：Vault 文件格式、密钥生命周期、备份包格式、新领域模型与引用规则、存储接口抽象。决策依据为 `CONTEXT.md`、[ADR 0001](../adr/0001-separate-local-vault-and-portable-backups.md) 与 [MVP 计划](mvp-modules-and-delivery-plan.md)，不重复论证已定边界。

## 1. Vault 文件格式

文件 `vault.dat`，位于应用数据目录（沿用现有目录解析，含 `config.root` 重定向）。

```
| magic 8B "TBVAULT1" | nonce 12B | ciphertext ... |
```

- 加密：AES-256-GCM（标准库 `crypto/aes` + `crypto/cipher`，零新增依赖）。
- nonce：每次写入取密码学随机 12 字节；magic 作为 AAD 绑定，防止文件被替换为其他版本/格式的合法密文。
- payload：JSON（标准库 `encoding/json`），结构见第 3 节。选 JSON 而非沿用 TOML 的理由：payload 永不落明文盘，人类可读性无收益；JSON 标准库成熟且无前向兼容陷阱。
- payload 内含 `version` 整数字段（当前 1），作为未来格式迁移的唯一钩子；magic 标识大版本，payload version 标识结构演进。
- 写入：与现有 `Storage.saveLocked` 相同的临时文件 + rename 原子替换，文件权限 0600（现为 0644，需收紧）。

## 2. 密钥生命周期

- 密钥：32 字节密码学随机，首次启动（vault.dat 与 vault.key 均不存在）时生成。
- 存储：同目录 `vault.key`，原始字节，权限 0600。这是 ADR 0001 的“设备本地随机密钥、零交互”决定：密钥本身不加密落盘，防护依赖文件权限与用户账户边界。OS 密钥库/DPAPI 加固列为后续增强，不在 MVP。
- 密钥遗失（vault.dat 存在而 vault.key 缺失或损坏）：**不自动覆盖数据**。Vault Module 返回明确的 `ErrKeyUnavailable`，由应用层引导用户“导入备份包”或“初始化空 Vault”（初始化前二次确认，旧 vault.dat 重命名保留为 `vault.dat.orphaned`，不删除）。
- vault.key 存在而 vault.dat 缺失/为空白：视为全新安装，基于现有密钥初始化空 Vault。
- 备份包不携带该密钥（见第 3 节），换机唯一途径是备份包导入。

## 3. 备份包格式（仅定义，实现属于迭代 4）

文件 `*.tbbak`：

```
| magic 8B "TBBACKUP" | salt 16B | m 4B | t 4B | p 4B | nonce 12B | ciphertext ... |
```

- KDF：Argon2id（`golang.org/x/crypto/argon2`，go.mod 已有 x/crypto），默认参数 m=64MiB、t=3、p=4（RFC 9106 推荐档），参数写入文件头以便未来调整。
- 加密：AES-256-GCM，密钥 = Argon2id(用户密码, salt, m, t, p)，magic+salt+参数整体作为 AAD。
- 内容：Vault payload 的完整快照（含密码与私钥口令），默认不含外部私钥文件本体；用户显式选择包含时私钥文件以条目形式附加进 payload（迭代 4 细化）。
- 密码不可找回：文件不含任何密码提示或恢复材料。

## 4. 新领域模型

`internal/model` 演进为（`→` 表示与现有模型的对应关系）：

```go
Folder   { ID, Name, ParentID /* 0=顶层 */, Sort }
SSHHost  { ID, Name, Host, Port, User, AuthType /* password|ssh_key|ssh_agent */,
           KeyPath, AgentSocketPath, Password /* 密码或私钥口令，仅存于 Vault */,
           KeepAliveIntervalMs, TimeoutMs, HostKeyAlgorithms, Notes }        // → Jumper；删除 BypassHostVerification
Forward  { ID, FolderID, Name, Mode /* local|remote|dynamic */, ChainHostIDs []int,
           LocalHost, LocalPort, RemoteHost, RemotePort, AutoStart, Description } // → Tunnel；删除 Status/LastError/LatencyMs
WebRoute { ID, ForwardID, Domain, HostsEnabled, CaddyEnabled,
           UpstreamScheme /* http|https */, TLSSNI }                        // 新增；迭代 1 仅建模型
HostKey  { ID, Host, Port, KeyType, FingerprintSHA256, FirstSeenAt, LastSeenAt } // 新增 TOFU 指纹库
Prefs    { AutoRun, UpdateCheckEnabled, UILocale }                          // ui.locale 伴随文件并入
```

关键决定：

- **运行时状态移出 Vault**：`Status`/`LastError`/`LatencyMs` 是运行时事实，持久化它们只会制造脏数据（现有 `Toggle` 需要 `Status == "running"` 兜底就是症状）。Vault 只存配置；运行状态由 Forward 运行时 Module 在内存维护并随快照下发。
- 密码/私钥口令仍为一字段两用（沿用现有语义），但只存在于加密 payload 内，不再出现在任何明文文件与导出物中。
- ID 保持 `int` 递增（max+1），降低前端与 biz 改动面。
- `HostKey` 存在 Vault 内而非独立 known_hosts 文件：指纹是完整性敏感数据，随 Vault 一起被 AEAD 认证，且摆脱对用户全局 `~/.ssh/known_hosts` 的依赖。

## 5. 引用完整性与校验规则

以下规则在 biz（目录与配置 Module）写入路径强制，纯业务可测：

- `Folder.ParentID` 必须为 0 或顶层文件夹（最多两层）；删除非空文件夹被拒绝，调用方须先移动内容，或以显式 `cascade` 参数二次确认级联删除（CONTEXT.md:66）。
- `Forward.FolderID`、`Forward.ChainHostIDs`（非空）、`WebRoute.ForwardID` 必须引用存在的实体；`WebRoute` 仅允许引用 `Mode=local` 的 Forward。
- 被任一 Forward 引用的 SSHHost 不可删除（CONTEXT.md:73）。
- 指纹规则（模型层）：同一 (Host, Port) 仅一条 HostKey；首次连接记录，后续指纹不一致即产生阻断错误，不存在“更新为最新”的隐式路径，变更只能由用户显式确认后替换。

## 6. 存储接口与模块落点

```
internal/vault/    加密文件格式、密钥生命周期、Store 实现（Vault Module 主体）
internal/model/    上述新模型 + 不变量校验函数
internal/biz/      演进为目录与配置 Module，依赖下述接口而非 *conf.Storage
```

```go
// biz 依赖的唯一存储接口（与计划文档 Vault Module 的 Load/Update 对齐）
type Store interface {
    Load() (model.VaultData, error)
    Update(mutate func(*model.VaultData) error) (model.VaultData, error)
}
```

- `Export(password)`/`StageImport`/`CommitImport` 属迭代 4，迭代 1 不实现，避免超前。
- `internal/conf` 在 biz 完成切换后整体删除，包括明文导出/导入（`app.go` 的 `ExportConfig`/`ImportConfig` 一并移除，导入入口按迭代 1 范围“暂不出现”）。
- 目录解析与 `config.root` 重定向逻辑从 `internal/conf` 平移到 `internal/vault`（该能力与加密无关，属数据目录管理）。
- 更新检查开关、开机自启开关并入 `Prefs` 持久化。

## 7. 验收对应与测试策略

迭代 1 验收：“磁盘上不再有可直接读取的密码与私钥口令；删除和引用规则可通过纯业务测试验证。”

- 格式测试：roundtrip、篡改任一字节即解密失败、错误密钥失败、AAD 替换 magic 失败。
- 密钥生命周期矩阵：双无→初始化；仅有 key→空 Vault；仅有 dat→`ErrKeyUnavailable` 且文件不被触碰；双有→正常加载。
- 无明文测试：写入包含秘密的 Vault 后，扫描数据目录全部文件，断言秘密字节序列不出现（含 `vault.dat`、`vault.key` 之外的伴随文件）。
- 规则测试：两层上限、非空文件夹删除、引用中 SSHHost 删除、WebRoute 非法引用、指纹 TOFU 记录/阻断/显式替换。
- 沿用 `privacy_contract_test.go` 的环境变量沙箱手法隔离数据目录。

## 8. 迭代 1 提交切片

1. 本设计文档（`docs:`）。
2. `internal/vault`：格式 + 密钥生命周期 + `Store`（TDD）。
3. `internal/model`：新模型 + 校验规则（TDD）。
4. `internal/biz` + `app.go`：切换新模型与 Store，删除 `internal/conf` 与明文导入导出（保证编译与现有测试通过为底线）。
5. 指纹信任库模型与校验（TDD；接入拨号属迭代 2）。
6. 最小管理界面（文件夹树、SSH 主机编辑、Forward 新建/编辑/批量删除）。

每片独立提交；编译红线：每片结束 `go build ./... && go vet ./... && go test ./...` 全绿。
