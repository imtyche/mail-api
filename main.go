package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"gopkg.in/gomail.v2"
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
	// 1. 初始化日志到当前目录的 mail.log 文件
	logFile, err := os.OpenFile("mail.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("无法创建或打开日志文件: %v", err)
	}
	defer logFile.Close()

	// 2. 将日志同时输出到控制台和文件，并设置给 log 包与 gin 框架
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multiWriter)
	gin.DefaultWriter = multiWriter // 让 Gin 的访问日志也写入该文件

	config := loadConfig()

	if len(config.Accounts) == 0 || config.ValidAppKey == "" {
		log.Fatal("错误：必须配置 SMTP_ACCOUNTS 和 APP_KEY")
	}

	rr := NewRoundRobin(config.Accounts)

	r := gin.Default()

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

	err = r.Run(":5010")
	if err != nil {
		return
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
