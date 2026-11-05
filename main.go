package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hydrz/baidunetdisk-sync/client"
	"github.com/hydrz/baidunetdisk-sync/config"
	"github.com/hydrz/baidunetdisk-sync/syncer"
)

var (
	flagSync     = flag.Bool("sync", false, "执行单次同步后退出")
	flagDaemon   = flag.Bool("daemon", false, "持续常驻后台周期性自动同步")
	flagInterval = flag.Int("interval", 0, "后台常驻模式下的同步间隔时间（秒）")
	flagWorkers  = flag.Int("workers", 0, "并发下载/上传工作线程数 (默认 4)")
	flagDryRun   = flag.Bool("dry-run", false, "演练模式（仅分析打印待执行的操作，不实际变动文件）")
	flagDir      = flag.String("dir", "", "自定义本地同步目录路径（默认 ~/.baidunetdisk）")
	flagRemote   = flag.String("remote", "", "自定义百度网盘同步根目录（默认 /_pcs_.workspace）")
	flagStatus   = flag.Bool("status", false, "查看当前同步状态与文件跟踪统计")
	flagToken    = flag.String("token", "", "手动设置并保存新的 BAIDU_REFRESH_TOKEN 到 .env")
)

func main() {
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime)
	log.Println("=== 百度网盘「同步空间」离线双向同步工具 (Go 原生版) ===")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("[FATAL] 读取配置失败: %v", err)
	}

	if *flagToken != "" {
		cfg.RefreshToken = *flagToken
		if err := cfg.SaveTokens(cfg.AccessToken, cfg.RefreshToken); err != nil {
			log.Fatalf("[FATAL] 保存 RefreshToken 失败: %v", err)
		}
		log.Printf("[配置] 成功将新的 BAIDU_REFRESH_TOKEN 保存到 %s", cfg.EnvPath)
	}

	// 命令行参数覆盖
	if *flagInterval > 0 {
		cfg.SyncInterval = *flagInterval
	}
	if *flagWorkers > 0 {
		cfg.Concurrency = *flagWorkers
	}
	if *flagDryRun {
		cfg.DryRun = true
	}
	if *flagDir != "" {
		cfg.LocalSyncDir = *flagDir
	}
	if *flagRemote != "" {
		cfg.RemoteRoot = *flagRemote
	}

	if cfg.RefreshToken == "" && cfg.AccessToken == "" {
		fmt.Println("\n[提示] 未检测到百度网盘登录凭证！")
		fmt.Println("请按以下步骤获取 BAIDU_REFRESH_TOKEN：")
		fmt.Println("1. 在浏览器打开：https://api.oplist.org/")
		fmt.Println("2. 选择「百度网盘」并完成扫码或授权登录")
		fmt.Println("3. 复制生成的 Refresh Token")
		fmt.Printf("4. 运行: ./baidunetdisk-sync -token <你的RefreshToken>\n\n")
		os.Exit(1)
	}

	log.Printf("[配置] 本地同步目录: %s", cfg.LocalSyncDir)
	log.Printf("[配置] 远端空间路径: %s", cfg.RemoteRoot)
	log.Printf("[配置] 并发工作线程: %d", cfg.Concurrency)

	// 初始化网盘客户端
	bClient := client.NewBaiduClient(cfg.AccessToken, cfg.RefreshToken, func(newAccess, newRefresh string) {
		log.Printf("[Token] 凭证已自动更新，正在持久化写回 %s ...", cfg.EnvPath)
		if err := cfg.SaveTokens(newAccess, newRefresh); err != nil {
			log.Printf("[WARN] 保存最新 Token 失败: %v", err)
		} else {
			log.Printf("[Token] 凭证持久化保存成功！")
		}
	})

	// 初始化本地状态管理器
	stateMgr, err := syncer.NewStateManager(cfg.LocalSyncDir)
	if err != nil {
		log.Fatalf("[FATAL] 加载本地同步状态失败: %v", err)
	}

	if *flagStatus {
		printStatus(cfg, stateMgr)
		return
	}

	// 如果 AccessToken 为空，立即刷新
	if bClient.GetAccessToken() == "" {
		log.Println("[Auth] AccessToken 为空，正在通过 oplist 接口自动兑换...")
		if err := bClient.RefreshToken(); err != nil {
			log.Fatalf("[FATAL] 获取 AccessToken 失败: %v", err)
		}
	}

	engine := syncer.NewEngine(cfg, bClient, stateMgr)

	if *flagDaemon {
		runDaemon(engine, cfg)
		return
	}

	// 默认执行单次同步
	log.Println("[运行] 开始执行同步...")
	startTime := time.Now()
	if err := engine.Sync(); err != nil {
		log.Printf("[WARN] 同步过程存在警告或错误: %v", err)
	} else {
		log.Printf("[完成] 同步成功结束，耗时: %v", time.Since(startTime).Round(time.Millisecond))
	}
}

func runDaemon(engine *syncer.Engine, cfg *config.Config) {
	log.Printf("[DAEMON] 常驻后台模式已启动，同步轮询间隔: %d 秒", cfg.SyncInterval)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(time.Duration(cfg.SyncInterval) * time.Second)
	defer ticker.Stop()

	// 启动时立即执行一次
	_ = engine.Sync()

	for {
		select {
		case <-sigChan:
			log.Println("\n[退出] 收到终止信号，正在安全退出...")
			return
		case <-ticker.C:
			log.Printf("[DAEMON] 周期轮询触发，开始同步...")
			_ = engine.Sync()
		}
	}
}

func printStatus(cfg *config.Config, sm *syncer.StateManager) {
	fmt.Printf("\n--- 同步状态概览 ---\n")
	fmt.Printf("本地同步目录: %s\n", cfg.LocalSyncDir)
	fmt.Printf("云端空间目录: %s\n", cfg.RemoteRoot)
	fmt.Println("--------------------")
}
