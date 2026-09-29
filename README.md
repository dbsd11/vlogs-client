# VictoriaLogs Client

一个轻量级的 VictoriaLogs Go 客户端，支持批量写入和异步发送，专为微服务日志和指标收集设计。

## 特性

- ✅ **批量写入**: 自动批量日志条目，减少HTTP请求次数
- ✅ **定时刷新**: 定时刷新日志，避免日志丢失
- ✅ **异步发送**: 日志发送不阻塞主流程
- ✅ **LLM指标**: 内置LLM调用指标记录（请求/响应/错误）
- ✅ **工具执行指标**: 记录工具执行的耗时和结果
- ✅ **零依赖**: 仅使用Go标准库
- ✅ **环境变量驱动**: 从环境变量读取配置

## 安装

```bash
go get github.com/dbsd11/vlogs-client
```

## 快速开始

### 1. 初始化客户端

```go
package main

import (
    "os"
    "time"
    "github.com/dbsd11/vlogs-client"
)

func main() {
    // 使用默认配置（从环境变量读取）
    cfg := vlogs.DefaultConfig()
    // 或者自定义配置
    cfg = vlogs.Config{
        URL:           "http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message",
        BatchSize:     100,           // 每100条日志刷新一次
        FlushInterval: 5 * time.Second, // 或每5秒刷新一次
        ServiceName:   "my-service",
        HTTPTimeout:   10 * time.Second,
    }
    
    client := vlogs.Init(cfg)
    defer vlogs.Flush() // 服务关闭时刷新
    
    // 你的服务逻辑...
}
```

### 2. 记录普通日志

```go
// 使用全局客户端
vlogs.Log("info", "Service started", map[string]string{
    "port":    "8001",
    "version": "1.0.0",
})

vlogs.Log("error", "Database connection failed", map[string]string{
    "error": err.Error(),
    "host":  dbHost,
})

// 或使用客户端实例
client := vlogs.GetClient()
client.Log("warn", "High memory usage", map[string]string{
    "memory_mb": "512",
})
```

### 3. 记录LLM调用指标

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
        0, // duration will be calculated
    )
    
    // 调用LLM
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

### 4. 记录工具执行指标

```go
func executeTool(toolName string, input string) (string, error) {
    startTime := time.Now()
    client := vlogs.GetClient()
    
    output, err := doExecute(toolName, input)
    duration := time.Since(startTime)
    
    // 记录工具执行
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

## 环境变量

| 变量名 | 说明 | 示例值 |
|--------|------|--------|
| `VLOGS_INSERT_URL` | VictoriaLogs 插入URL | `http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message` |
| `SERVICE_NAME` | 服务名称（用于stream标签） | `bug-agent-server` |

## Docker Compose 配置

```yaml
services:
  your-service:
    image: your-service:latest
    environment:
      - VLOGS_INSERT_URL=http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message
      - SERVICE_NAME=your-service-name
```

## API 文档

### 配置结构

```go
type Config struct {
    URL           string        // VictoriaLogs insert URL
    BatchSize     int           // 批量大小（默认100）
    FlushInterval time.Duration // 刷新间隔（默认5秒）
    ServiceName   string        // 服务名称
    HTTPTimeout   time.Duration // HTTP超时（默认10秒）
}
```

### 核心方法

#### 初始化
- `Init(cfg Config) *Client` - 初始化全局客户端
- `NewClient(cfg Config) *Client` - 创建新客户端实例
- `DefaultConfig() Config` - 获取默认配置（从环境变量读取）

#### 日志记录
- `Log(level, message string, fields map[string]string)` - 记录普通日志
- `LogLLMRequest(model, endpoint string, inputTokens int, requestPayload string, duration time.Duration)` - 记录LLM请求
- `LogLLMResponse(model, endpoint string, inputTokens, outputTokens int, responseContent string, duration time.Duration)` - 记录LLM响应
- `LogLLMError(model, endpoint, errorMsg string, duration time.Duration)` - 记录LLM错误
- `LogToolExecution(toolName, input, output string, duration time.Duration, success bool)` - 记录工具执行

#### 便捷方法
- `GetClient() *Client` - 获取全局客户端
- `Flush()` - 刷新全局客户端日志

## VictoriaLogs URL 格式

```
http://vlogs:9428/insert/jsonline?_stream_fields=stream&_time_field=date&_msg_field=log.message
```

参数说明：
- `_stream_fields=stream` - 使用stream字段作为流标签
- `_time_field=date` - 使用date字段作为时间戳
- `_msg_field=log.message` - 使用log.message作为消息内容

## 示例项目

查看 [example_usage.go](example_usage.go) 获取完整的使用示例。

## License

MIT
