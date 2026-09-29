# VictoriaLogs Client 使用指南

## 📦 独立仓库

本客户端是一个独立的Go模块，仓库地址：`github.com/dbsd11/vlogs-client`

## 🚀 快速集成

### 1. 添加依赖

在你的服务 `go.mod` 中添加：

```go
require github.com/dbsd11/vlogs-client v0.0.0

# 本地开发时使用本地路径
replace github.com/dbsd11/vlogs-client => ../vlogs-client
```

### 2. 初始化（3行代码）

```go
import "github.com/dbsd11/vlogs-client"

func main() {
    // 自动检测服务名，从环境变量或调用者模块路径
    vlogs.Init(vlogs.DefaultConfig())
    defer vlogs.Flush()
    
    // 你的服务逻辑...
}
```

### 3. 配置环境变量

在 `docker-compose.yml` 中：

```yaml
services:
  your-service:
    environment:
      - VLOGS_INSERT_URL=http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message
      # SERVICE_NAME 可选，不设置会自动检测
```

## 🎯 核心功能

### 自动服务名检测

服务名称按以下优先级获取：
1. `SERVICE_NAME` 环境变量
2. 调用者的Go模块路径（自动提取）
3. 回退值：`unknown-service`

**示例**：
- 模块 `bug-agent-server/main` → 服务名 `bug-agent-server`
- 模块 `github.com/user/my-service/handler` → 服务名 `my-service`

### LLM调用监控

```go
import (
    "time"
    "github.com/dbsd11/vlogs-client"
)

func callLLM(model, prompt string) (string, error) {
    startTime := time.Now()
    client := vlogs.GetClient()
    
    // 记录请求
    client.LogLLMRequest(
        model,
        "http://llm-server/v1/chat/completions",
        len(prompt),
        prompt,
        0,
    )
    
    response, err := doLLMCall(model, prompt)
    duration := time.Since(startTime)
    
    if err != nil {
        // 记录错误
        client.LogLLMError(model, "http://llm-server/v1/chat/completions", err.Error(), duration)
        return "", err
    }
    
    // 记录响应
    client.LogLLMResponse(
        model,
        "http://llm-server/v1/chat/completions",
        len(prompt),
        len(response),
        response,
        duration,
    )
    
    return response, nil
}
```

**记录的指标**：
- ✅ 模型名称
- ✅ 端点URL
- ✅ 输入/输出Token数
- ✅ 请求/响应内容（<10KB）
- ✅ 耗时（毫秒）
- ✅ 错误信息

### 工具执行监控

```go
func executeTool(toolName string, input string) (string, error) {
    startTime := time.Now()
    client := vlogs.GetClient()
    
    output, err := doExecute(toolName, input)
    duration := time.Since(startTime)
    
    client.LogToolExecution(
        toolName,
        input,
        output,
        duration,
        err == nil,
    )
    
    return output, err
}
```

**记录的指标**：
- ✅ 工具名称
- ✅ 输入/输出（<5KB）
- ✅ 执行耗时
- ✅ 成功/失败状态

### 普通日志

```go
// 使用全局客户端
vlogs.Log("info", "Service started", map[string]string{
    "port":    "8001",
    "version": "1.0.0",
})

// 或使用实例
client := vlogs.GetClient()
client.Log("error", "Database error", map[string]string{
    "error": err.Error(),
    "host":  dbHost,
})
```

## 📊 批量写入机制

### 工作原理

1. **批量累积**：日志先写入内存批次
2. **触发条件**：
   - 批次达到 `BatchSize`（默认1000条）
   - 或超过 `FlushInterval`（默认5秒）
3. **异步发送**：不阻塞主流程

### 性能优势

- ✅ 减少HTTP请求次数（1000条/次）
- ✅ 异步发送，零阻塞
- ✅ 自动重试（失败时记录警告）

## 🔧 配置选项

```go
type Config struct {
    URL           string        // VictoriaLogs插入URL（必填）
    BatchSize     int           // 批量大小（默认1000）
    FlushInterval time.Duration // 刷新间隔（默认5秒）
    ServiceName   string        // 服务名（可选，自动检测）
    HTTPTimeout   time.Duration // HTTP超时（默认10秒）
}
```

### 自定义配置示例

```go
cfg := vlogs.Config{
    URL:           "http://vlogs:9428/insert/jsonline?...",
    BatchSize:     500,           // 更频繁的刷新
    FlushInterval: 2 * time.Second,
    ServiceName:   "my-custom-service",
    HTTPTimeout:   15 * time.Second,
}
vlogs.Init(cfg)
```

## 🐳 Docker Compose 完整示例

```yaml
services:
  bug-agent-server:
    build: ./bug-agent-server
    environment:
      - VLOGS_INSERT_URL=http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message
      # SERVICE_NAME 可选，会自动从模块路径检测
    volumes:
      - ./data:/app/data
```

## 📈 VictoriaLogs URL 格式

```
http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message
```

**参数说明**：
- `_stream_fields=stream` - 使用stream字段作为流标签
- `_time_field=date` - 使用date字段作为时间戳
- `_msg_field=log.message` - 使用log.message作为消息内容

## 🎓 最佳实践

1. **服务启动时初始化**
   ```go
   func main() {
       vlogs.Init(vlogs.DefaultConfig())
       defer vlogs.Flush()
   }
   ```

2. **关键操作记录指标**
   - LLM调用（请求/响应/错误）
   - 工具执行
   - 数据库操作
   - 外部API调用

3. **服务关闭时刷新**
   ```go
   defer vlogs.Flush()
   ```

4. **不要阻塞主流程**
   - 客户端内部异步发送
   - 无需手动Flush（除非关闭时）

## 🔍 查询示例

在VictoriaLogs/Grafana中查询：

```
# 查询特定服务的日志
stream="bug-agent-server"

# 查询LLM错误日志
stream="bug-agent-server" type="llm_error"

# 查询慢LLM调用（>5秒）
stream="bug-agent-server" type="llm_response" duration_ms > 5000

# 查询失败的工具执行
stream="bug-agent-server" type="tool_execution" success="false"
```

## 📝 License

MIT License - See LICENSE file for details
