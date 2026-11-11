package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/gomail.v2"
	"gopkg.in/natefinch/lumberjack.v2"
)

type EmailAccount struct {
	User string
	Pass string
}

type Config struct {
	SmtpHost    string
	SmtpPort    int
	Accounts    []EmailAccount // 支持多个账号
	ValidAppKey string
}
type RoundRobin struct {
	mu       sync.Mutex
	accounts []EmailAccount
	index    int
}

// NewRoundRobin 创建轮询器
func NewRoundRobin(accounts []EmailAccount) *RoundRobin {
	if len(accounts) == 0 {
		return nil
	}
	return &RoundRobin{
		accounts: accounts,
		index:    0,
	}
}

// Next 返回下一个账号（线程安全）
func (r *RoundRobin) Next() EmailAccount {
	r.mu.Lock()
	defer r.mu.Unlock()
	acc := r.accounts[r.index]
	r.index = (r.index + 1) % len(r.accounts)
	return acc
}

func loadAccountsFromEnv(envKey string) []EmailAccount {
	accountsStr := getEnv(envKey, "")
	if accountsStr == "" {
		return nil
	}
	parts := strings.Split(accountsStr, ",")
	var accounts []EmailAccount
	for _, part := range parts {
		cred := strings.SplitN(part, ":", 2)
		if len(cred) == 2 {
			accounts = append(accounts, EmailAccount{
				User: strings.TrimSpace(cred[0]),
				Pass: strings.TrimSpace(cred[1]),
			})
		}
	}
	return accounts
}

func loadConfig() Config {
	port, _ := strconv.Atoi(getEnv("SMTP_PORT", "465"))
	// 加载账号数组（这里以方式A为例）
	accounts := loadAccountsFromEnv("SMTP_ACCOUNTS")
	if len(accounts) == 0 {
		log.Println("警告：未配置 SMTP_ACCOUNTS，请至少设置一个账号")
	}
	return Config{
		SmtpHost:    getEnv("SMTP_HOST", "smtp.qq.com"),
		SmtpPort:    port,
		Accounts:    accounts,
		ValidAppKey: getEnv("APP_KEY", ""),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

type EmailRequest struct {
	To      string `json:"to" binding:"required,email"`
	Subject string `json:"subject" binding:"required"`
	Body    string `json:"body" binding:"required"`
}

func main() {
	// 1. 创建日志目录（容器中可通过挂载 /app/logs 持久化）
	if err := os.MkdirAll("logs", 0755); err != nil {
		log.Fatalf("无法创建日志目录: %v", err)
	}

	// 2. 初始化日志轮转，防止日志文件无限增长
	logFile := &lumberjack.Logger{
		Filename:   "logs/mail.log",
		MaxSize:    10,   // 单个日志文件最大 10MB，超过后自动切割
		MaxBackups: 3,    // 最多保留 3 个历史日志文件
		MaxAge:     7,    // 历史日志文件最多保留 7 天
		Compress:   true, // 压缩历史日志文件
	}

	// 3. 将日志同时输出到控制台和文件，并设置给 log 包与 gin 框架
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multiWriter)
	gin.DefaultWriter = multiWriter

	config := loadConfig()

	if len(config.Accounts) == 0 || config.ValidAppKey == "" {
		log.Fatal("错误：必须配置 SMTP_ACCOUNTS 和 APP_KEY")
	}

	rr := NewRoundRobin(config.Accounts)

	r := gin.New()
	r.Use(requestLogger(), gin.Recovery())

	r.POST("/api/send", func(c *gin.Context) {

		if c.GetHeader("appKey") != config.ValidAppKey {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权：AppKey 无效"})
			return
		}

		var req EmailRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
			return
		}

		// 尝试发送邮件（带重试机制，最多尝试所有账号）
		var lastErr error
		for retry := 0; retry < len(config.Accounts); retry++ {
			acc := rr.Next() // 轮询获取账号
			err := sendEmail(config.SmtpHost, config.SmtpPort, acc.User, acc.Pass, req.To, req.Subject, req.Body)
			if err == nil {
				c.JSON(http.StatusOK, gin.H{"message": "邮件发送成功！"})
				return
			}
			lastErr = err
			log.Printf("使用账号 %s 发送失败: %v，尝试下一个账号", acc.User, err)
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "所有账号均发送失败: " + lastErr.Error()})
	})

	if err := r.Run(":5010"); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

func sendEmail(host string, port int, user, pass, to, subject, body string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", user)
	m.SetHeader("To", to)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", body)

	d := gomail.NewDialer(host, port, user, pass)
	return d.DialAndSend(m)
}

// requestLogger 自定义访问日志中间件：过滤 Cloudflare 后的安全扫描/探测请求
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		if shouldSkipLog(c.Writer.Status(), path) {
			return
		}

		log.Printf("[GIN] %s | %3d | %13v | %-15s | %s",
			c.Request.Method,
			c.Writer.Status(),
			time.Since(start),
			c.ClientIP(),
			path,
		)
	}
}

// shouldSkipLog 判断请求日志是否应被忽略
func shouldSkipLog(statusCode int, path string) bool {
	// 本服务仅提供 /api/send 一个合法端点，所有 404 均为扫描/探测请求
	if statusCode == http.StatusNotFound {
		return true
	}

	// 过滤常见安全扫描路径（无论返回状态码）
	lower := strings.ToLower(path)
	for _, kw := range []string{
		"/.env", "/.git", "/.aws", "/.ssh", "/wp-", "/php",
		"/admin", "/actuator", "/config", "/backup", "/sql",
		"/debug", "/trace", "/shell", "/.well-known", "/robots.txt",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
