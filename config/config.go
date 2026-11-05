package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Config struct {
	mu           sync.Mutex
	EnvPath      string
	AccessToken  string
	RefreshToken string
	LocalSyncDir string
	RemoteRoot   string
	SyncInterval int
	Concurrency  int
	DryRun       bool
}

func LoadConfig() (*Config, error) {
	envMap := make(map[string]string)
	selectedEnvPath := ".env"

	// 读取当前目录或家目录的 .env
	candidates := []string{".env", filepath.Join(os.Getenv("HOME"), ".baidunetdisk.env")}
	for _, p := range candidates {
		if f, err := os.Open(p); err == nil {
			selectedEnvPath = p
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
					envMap[key] = val
				}
			}
			f.Close()
			break
		}
	}

	getVal := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		if v, ok := envMap[key]; ok && v != "" {
			return v
		}
		return def
	}

	homeDir, _ := os.UserHomeDir()
	localDir := getVal("LOCAL_SYNC_DIR", filepath.Join(homeDir, ".baidunetdisk"))
	if strings.HasPrefix(localDir, "~/") {
		localDir = filepath.Join(homeDir, localDir[2:])
	}

	interval, _ := strconv.Atoi(getVal("SYNC_INTERVAL", "300"))
	if interval <= 0 {
		interval = 300
	}

	concurrency, _ := strconv.Atoi(getVal("CONCURRENCY", "16"))
	if concurrency <= 0 {
		concurrency = 16
	}

	return &Config{
		EnvPath:      selectedEnvPath,
		AccessToken:  getVal("BAIDU_ACCESS_TOKEN", ""),
		RefreshToken: getVal("BAIDU_REFRESH_TOKEN", ""),
		LocalSyncDir: localDir,
		RemoteRoot:   getVal("REMOTE_ROOT", "/_pcs_.workspace"),
		SyncInterval: interval,
		Concurrency:  concurrency,
	}, nil
}

// SaveTokens 将最新获得的 AccessToken 和 RefreshToken 持久化写回 .env 文件
func (c *Config) SaveTokens(accessToken, refreshToken string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.AccessToken = accessToken
	c.RefreshToken = refreshToken

	filePath := c.EnvPath
	if filePath == "" {
		filePath = ".env"
	}

	var lines []string
	hasAccess := false
	hasRefresh := false

	if data, err := os.ReadFile(filePath); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "BAIDU_ACCESS_TOKEN=") {
				lines = append(lines, fmt.Sprintf("BAIDU_ACCESS_TOKEN=%q", accessToken))
				hasAccess = true
			} else if strings.HasPrefix(trimmed, "BAIDU_REFRESH_TOKEN=") {
				lines = append(lines, fmt.Sprintf("BAIDU_REFRESH_TOKEN=%q", refreshToken))
				hasRefresh = true
			} else {
				lines = append(lines, line)
			}
		}
	}

	if !hasAccess {
		lines = append(lines, fmt.Sprintf("BAIDU_ACCESS_TOKEN=%q", accessToken))
	}
	if !hasRefresh {
		lines = append(lines, fmt.Sprintf("BAIDU_REFRESH_TOKEN=%q", refreshToken))
	}

	content := strings.Join(lines, "\n") + "\n"
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err == nil {
		if err := os.Rename(tmpFile, filePath); err == nil {
			return nil
		}
		_ = os.Remove(tmpFile)
	}
	// 兼容 Docker 单文件挂载 (EBUSY) 场景下的直接覆盖写入
	return os.WriteFile(filePath, []byte(content), 0644)
}
