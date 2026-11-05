package syncer

import (
	"path/filepath"
	"strings"
)

var defaultIgnorePatterns = []string{
	".sync_state.json",
	".sync_state.json.tmp",
	"*.bdsync.tmp",
	"*.tmp",
	"*.swp",
	"*.swo",
	".DS_Store",
	"Thumbs.db",
	"desktop.ini",
	".git/**",
	".git",
	"~$*",
}

// Filter 判断路径是否应该被忽略
type Filter struct {
	patterns []string
}

func NewFilter(patterns ...string) *Filter {
	if len(patterns) == 0 {
		patterns = defaultIgnorePatterns
	}
	return &Filter{patterns: patterns}
}

func (f *Filter) ShouldIgnore(relPath string) bool {
	// 规范化路径分隔符
	cleanPath := filepath.ToSlash(relPath)
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	parts := strings.Split(cleanPath, "/")
	baseName := filepath.Base(cleanPath)

	for _, pattern := range f.patterns {
		// 忽略隐藏的特定目录如 .git
		if pattern == ".git" || pattern == ".git/**" {
			for _, part := range parts {
				if part == ".git" {
					return true
				}
			}
		}

		// 检查基本文件名匹配（例如 *.tmp, Thumbs.db, ~$*）
		if matched, _ := filepath.Match(pattern, baseName); matched {
			return true
		}

		// 检查相对路径匹配
		if matched, _ := filepath.Match(pattern, cleanPath); matched {
			return true
		}
	}

	return false
}
