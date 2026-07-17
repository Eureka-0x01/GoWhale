# GoWhale —— Go 写的终端 AI 编程助手

参考 [CodeWhale](../CodeWhale)（Rust 版本地 AI 编程 Agent）的思路，用 Go 实现的一个
**命令行 AI 编程 Agent**：读取你的任务 → 大模型规划 → 调用「技能」直接操作本地文件与命令 → 汇报结果。

## 项目类型

属于 **CLI Coding Agent / AI 编程助手**。同类：Claude Code、Aider、Cursor、CodeWhale。
核心是一个「**工具调用循环**」：模型不只是回答，还能通过工具读写文件、执行命令来真正完成任务。

## 已实现的技能（工具）

| 技能 | 说明 | 审批 |
|------|------|------|
| `write_plan`    | 创建/更新任务计划（`.aicode/plan.md`） | 自动放行 |
| `batch_write`   | **批量写入多个文件**（一次搞定，省轮数） | ✅ 需审批 |
| `execute_shell` | 执行 shell 命令 | ✅ 需审批，危险命令额外警告 |
| `write_file`    | 写入/生成单个文件（自动建父目录） | ✅ 需审批 |
| `read_file`     | 读取文件内容 | 自动放行（只读） |
| `list_dir`      | 列出目录内容 | 自动放行（只读） |

### 输出格式（仿 CodeWhale 紧凑风格）

每个工具调用呈现为**一行**紧凑日志，细节自动收起，只有出错时才展开：

```
[1] 📁 list_dir       .                        → ✓ [目录] src/   [文件] main.go
[2] 📋 write_plan     6 步骤                    → ✓ 计划已更新（0/6 完成）
[3] 🔧 execute_shell  go build ./...            ▶ [y/a/N] → ✓ (命令执行成功，无输出)
[4] ✏️ batch_write    4 files (go.mod, …)       → ✓ 已批量写入 4 个文件
[5] 🔧 execute_shell  rm -rf tmp                → ✗ 执行出错：高危命令
     命令行包含 rm -rf 被拦截。请检查后重试。
```

每次工具调用都会在终端**实时报告**（`🔧 工具名 参数` + 结果摘要）。

## 审批门（approval gate）

参考 CodeWhale 的做法，**所有有副作用的操作在执行前都会停下来征求你的确认**：

```
🔧 execute_shell  {"command":"rm -rf tmp_test"}
   ⚠️  高危操作：命中危险命令拦截规则：\brm\s+...
   ▶ 是否允许执行？[y]本次 / [a]该范围始终允许 / [N]否
```

- 只读操作（`read_file` / `list_dir`）自动放行，不打扰你。
- 写文件 / 执行命令必须确认；**默认拒绝**（直接回车或输入非 `y`/`a` 即不执行）。
- **`a` = 该范围始终允许**（授权记忆，见下）：一次授权，后续同范围自动放行。
- 危险命令（`rm -rf`、`mkfs`、`format`、`dd of=/dev/`、关机重启、fork 炸弹、
  `curl ... | sh`、借解释器内联删除等）会额外标红警告，且**不提供 `a`**（每次都问）。
- 危险规则见 `internal/tools/guard.go`。

> 相比纯黑名单，审批门从机制上根治了「模型写脚本再执行」的绕过问题——
> 因为脚本的执行（`execute_shell`）本身也要过你这一关。

### 授权记忆（选 `a` 后不再重复询问）

- **写文件按目录记忆**：对某目录选 `a` 后，写入该目录**及其子目录**的后续文件自动放行。
- **执行命令按会话记忆**：选 `a` 后，本次会话内的**非危险**命令自动放行（危险命令仍每次询问）。
- 记忆只在**单次进程/会话**内有效，退出后重置。

## 工作日志（.aicode/journal.md）

Agent 会在**当前工作目录**下的 `.aicode/journal.md` 记录自己的工作：每条任务（带时间戳）、
每次工具调用、最终总结。下次在同一目录启动时，会**读回最近的记录注入上下文**，让模型「记得」
之前做过什么，方便接续。建议把 `.aicode/` 加入 `.gitignore`。

## 配置（环境变量）

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `AICODE_API_KEY`   | 访问密钥 | 代码内置测试 Key |
| `AICODE_BASE_URL`  | API 地址 | `https://api.deepseek.com/v1` |
| `AICODE_MODEL`     | 模型名 | `deepseek-chat` |
| `AICODE_MAX_TURNS` | 单次任务最大工具调用轮数 | `40` |

> 任务较大时可能触及轮数上限；届时程序会**保存进度并给出方案**（输入「继续」接续、
> 拆分任务、或调高 `AICODE_MAX_TURNS`）。

## 使用

```bash
cd /d/ronghuicode/other/aiCode

# 一次性任务
go run . "在 demo 目录下生成一个 Go 版 hello world 并编译运行"

# 交互式多轮（exit 退出）
go run .
```

## 目录结构

```
.gitignore                   忽略编译产物 + .aicode/
main.go                      入口：加载配置、注册技能、启动 Agent
internal/
  config/config.go           配置加载（provider/model/baseURL 分离）
  llm/client.go              大模型调用（OpenAI 兼容 + function calling）
  tools/
    registry.go              工具接口、注册表、Approvable 审批接口
    plan.go                  write_plan 任务拆解工具
    guard.go                 危险命令识别规则（供审批门警告）
    shell.go                 execute_shell 技能（含 GBK 解码）
    file.go                  read_file / write_file / batch_write / list_dir 技能
    workspace.go             工作区锁定 + 路径越界拦截
  agent/
    agent.go                 工具调用循环 + 执行报告 + 达上限方案
    approver.go              审批门（stdin 确认 + 授权记忆）
    ego.go                   工作区身份 + 宪法加载/渲染
    journal.go               工作日志（.aicode/journal.md）
    term.go                  ANSI 颜色 + 转圈动画
```

## 后续可扩展（对齐 CodeWhale 完整形态）

- 把授权记忆持久化到磁盘，跨会话保留
- 沙箱执行、side-git 快照与回滚
- 编辑后语言服务器诊断
- 并发子 Agent、会话持久化
