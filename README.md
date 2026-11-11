# mail-api

一个基于 Go 的轻量级 SMTP 邮件发送服务，提供 HTTP API 供其他应用集成调用。支持多账号轮询、安全认证与失败重试，可轻松通过 Docker 部署。

## 功能特性

- **多账号轮询发送**：支持配置多个 SMTP 邮箱账号，按轮询（Round-Robin）策略自动分发，分散单账号发送压力、降低封禁风险。
- **失败自动重试**：当某个账号发送失败时，自动切换到下一个账号继续尝试，直到所有账号尝试完毕。
- **安全认证**：通过 `appKey` 请求头校验调用方身份，防止接口被滥用。
- **HTML 邮件正文**：邮件正文以 HTML 格式发送，支持富文本内容。
- **日志切割**：访问日志与错误日志自动轮转（按大小/数量/时间），防止文件无限增长。
- **日志过滤**：自动忽略 404 与常见安全扫描请求，保持日志整洁。
- **环境变量配置**：SMTP 服务器、端口、账号、密钥等全部通过环境变量注入，简化部署。
- **Docker 化部署**：提供多阶段构建的 `Dockerfile`，最终镜像精简、体积小。

## 技术栈

| 组件 | 说明 |
| --- | --- |
| [Go](https://go.dev/) | 开发语言 |
| [Gin](https://github.com/gin-gonic/gin) | HTTP Web 框架 |
| [gomail](https://github.com/go-gomail/gomail) | SMTP 邮件发送库 |
| [lumberjack](https://github.com/natefinch/lumberjack) | 日志切割库 |
| [Alpine](https://www.alpinelinux.org/) | 运行环境基础镜像 |

## 环境变量

| 变量名 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `SMTP_ACCOUNTS` | 是 | 无 | 发件账号列表，多个账号用英文逗号 `,` 分隔，格式为 `邮箱:授权码` |
| `SMTP_HOST` | 否 | `smtp.qq.com` | SMTP 服务器地址 |
| `SMTP_PORT` | 否 | `465` | SMTP 服务器端口（SSL） |
| `APP_KEY` | 是 | 无 | 调用 API 时用于鉴权的密钥 |

> **授权码说明**：`SMTP_ACCOUNTS` 中的密码请使用邮箱服务商提供的「SMTP 授权码」，而非登录密码。
> - QQ 邮箱：设置 → 账户 → 开启 SMTP 服务后获取授权码
> - 163 邮箱：设置 → POP3/SMTP/IMAP → 开启 SMTP 服务后获取授权码

多账号配置示例：

```
SMTP_ACCOUNTS=sender1@qq.com:授权码1,sender2@163.com:授权码2
```

## 快速开始

### 本地运行

```bash
# 1. 设置环境变量
export SMTP_ACCOUNTS="sender1@qq.com:pass123,sender2@163.com:pass456"
export SMTP_HOST="smtp.qq.com"
export SMTP_PORT="465"
export APP_KEY="your-secret-key"

# 2. 运行
go run main.go
```

服务默认监听在 `5010` 端口。

### Docker 运行

创建 `docker-compose.yml`：

```yaml
version: "3.9"

services:
  mail:
    image: gzsoft/mail-api:latest
    container_name: mail
    ports:
      - "5010:5010"
    environment:
      - TZ=Asia/Shanghai
      - SMTP_ACCOUNTS=你的QQ@qq.com:QQ邮箱授权码
      - SMTP_HOST=smtp.qq.com
      - SMTP_PORT=465
      - APP_KEY=your-secret-key
    volumes:
      - /root/docker/mail/logs:/app/logs
    restart: unless-stopped
```

启动服务：

```bash
docker-compose up -d
```

## API 使用

### 发送邮件

发送邮件时，请在请求头中携带 `appKey`，其值需与 `APP_KEY` 环境变量一致。

**请求示例**

```http
POST /api/send
Content-Type: application/json
appKey: your-secret-key
```

```json
{
    "to": "recipient@gmail.com",
    "subject": "邮件标题",
    "body": "<p>邮件正文内容，支持 <strong>HTML</strong></p>"
}
```

**字段说明**

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `to` | string | 是 | 收件人邮箱地址（需为合法邮箱格式） |
| `subject` | string | 是 | 邮件标题 |
| `body` | string | 是 | 邮件正文，支持 HTML |

**响应示例**

| 场景 | HTTP 状态码 | 响应内容 |
| --- | --- | --- |
| 发送成功 | `200` | `{"message":"邮件发送成功！"}` |
| 鉴权失败 | `401` | `{"error":"未授权：AppKey 无效"}` |
| 参数错误 | `400` | `{"error":"参数错误: ..."}` |
| 全部账号发送失败 | `500` | `{"error":"所有账号均发送失败: ..."}` |

**curl 示例**

```bash
curl -X POST http://localhost:5010/api/send \
  -H "Content-Type: application/json" \
  -H "appKey: your-secret-key" \
  -d '{
    "to": "recipient@gmail.com",
    "subject": "Hello",
    "body": "<p>Hello from mail-api</p>"
  }'
```

## 多账号轮询机制

服务启动时会解析 `SMTP_ACCOUNTS` 中的所有账号，并维护一个线程安全的轮询器（Round-Robin）：

1. 每次请求依次轮询取用下一个账号；
2. 若当前账号发送失败，自动尝试下一个账号；
3. 最多重试次数等于账号总数；
4. 若所有账号均失败，返回 `500` 错误。

该机制可有效分散单账号发送压力，并在某个账号不可用时提供容错能力。

## 日志

程序运行后会在 `logs/` 目录下生成日志文件（容器中路径为 `/app/logs/mail.log`），同时将日志输出到控制台。日志通过 `lumberjack` 自动切割，避免文件无限增长：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| 单文件大小上限 | `10` MB | 超过后自动切割 |
| 历史文件保留数量 | `3` | 最多保留的历史日志文件数 |
| 历史文件保留时间 | `7` 天 | 超时自动删除 |
| 历史文件压缩 | 开启 | 压缩为 `.gz` |

记录的内容包括：

- 有意义的访问日志（方法、状态码、耗时、客户端 IP、路径）
- 发送失败时切换账号的提示日志
- 启动时的配置警告信息

> **日志过滤**：服务通常部署在 Cloudflare 之后，会收到大量安全扫描/探测请求。为保持日志整洁，以下请求不会写入日志（请求本身仍会正常处理）：
> - 所有返回 `404` 的请求（本服务仅有 `/api/send` 一个合法端点）
> - 命中常见扫描路径关键词的请求（如 `/.env`、`/.git`、`/wp-`、`/php`、`/actuator`、`/admin` 等）

## 构建与部署

### 本地编译

项目提供了跨平台编译脚本：

- `build-linux-amd64.bat`：编译 Linux（AMD64）版本，输出 `mail-api-linux-amd64`
- `build-windows-amd64.bat`：编译 Windows（AMD64）版本，输出 `mail-api-windows-amd64.exe`

### 手动构建 Docker 镜像

```bash
docker build -t mail-api .
docker run -d \
  -p 5010:5010 \
  -v /root/docker/mail/logs:/app/logs \
  -e SMTP_ACCOUNTS="sender@qq.com:授权码" \
  -e SMTP_HOST="smtp.qq.com" \
  -e SMTP_PORT="465" \
  -e APP_KEY="your-secret-key" \
  mail-api
```

### 持续集成

仓库内置 GitHub Actions 工作流（`.github/workflows/deploy.yml`），在推送到 `main` 分支或创建 `v*` 标签时，会自动构建并推送 Docker 镜像到 Docker Hub。使用前需在仓库 Secrets 中配置：

- `DOCKER_USERNAME`：Docker Hub 用户名
- `DOCKER_PASSWORD`：Docker Hub 访问令牌

## 目录结构

```
mail-api/
├── main.go                        # 主程序入口
├── go.mod / go.sum                # Go 模块依赖
├── Dockerfile                     # 多阶段构建镜像
├── build-linux-amd64.bat          # Linux 编译脚本
├── build-windows-amd64.bat        # Windows 编译脚本
└── .github/workflows/deploy.yml   # CI/CD 工作流
```

## 常见问题

**Q：提示「必须配置 SMTP_ACCOUNTS 和 APP_KEY」？**
服务启动时会对必填环境变量做校验，请确保 `SMTP_ACCOUNTS` 和 `APP_KEY` 均已正确设置。

**Q：邮件发送失败？**
请检查 SMTP 主机/端口是否与邮箱服务商一致，以及是否使用了正确的 SMTP 授权码（而非登录密码）。常见配置：QQ 邮箱 `smtp.qq.com:465`，163 邮箱 `smtp.163.com:465`。

**Q：如何发送纯文本邮件？**
`body` 字段按 HTML 解析，纯文本请直接传入文本内容，或使用 `<pre>` 等标签保持格式。
