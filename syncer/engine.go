package syncer

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hydrz/baidunetdisk-sync/client"
	"github.com/hydrz/baidunetdisk-sync/config"
)

type LocalFile struct {
	RelPath string
	AbsPath string
	Size    int64
	Mtime   int64
}

type SyncActionType string

const (
	ActionDownload SyncActionType = "DOWNLOAD"
	ActionUpload   SyncActionType = "UPLOAD"
	ActionDeleteL  SyncActionType = "DELETE_LOCAL"
	ActionDeleteR  SyncActionType = "DELETE_REMOTE"
	ActionConflict SyncActionType = "CONFLICT"
)

type SyncTask struct {
	Type     SyncActionType
	RelPath  string
	Local    *LocalFile
	Remote   *client.FileItem
	Reason   string
}

type Engine struct {
	cfg    *config.Config
	client *client.BaiduClient
	state  *StateManager
	filter *Filter
}

func NewEngine(cfg *config.Config, bClient *client.BaiduClient, state *StateManager) *Engine {
	return &Engine{
		cfg:    cfg,
		client: bClient,
		state:  state,
		filter: NewFilter(),
	}
}

// ScanLocal 扫描本地同步根目录
func (e *Engine) ScanLocal() (map[string]LocalFile, error) {
	result := make(map[string]LocalFile)

	if err := os.MkdirAll(e.cfg.LocalSyncDir, 0755); err != nil {
		return nil, fmt.Errorf("创建或打开本地同步目录失败: %w", err)
	}

	err := filepath.WalkDir(e.cfg.LocalSyncDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == e.cfg.LocalSyncDir {
			return nil
		}

		relPath, err := filepath.Rel(e.cfg.LocalSyncDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if e.filter.ShouldIgnore(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		result[relPath] = LocalFile{
			RelPath: relPath,
			AbsPath: path,
			Size:    info.Size(),
			Mtime:   info.ModTime().Unix(),
		}
		return nil
	})

	return result, err
}

// Plan 对比本地、远端与历史状态，计算同步动作
func (e *Engine) Plan(localMap map[string]LocalFile, remoteMap map[string]client.FileItem) []SyncTask {
	var tasks []SyncTask
	allKeys := make(map[string]struct{})
	for k := range localMap {
		allKeys[k] = struct{}{}
	}
	for k := range remoteMap {
		allKeys[k] = struct{}{}
	}

	for relPath := range allKeys {
		loc, hasLoc := localMap[relPath]
		rem, hasRem := remoteMap[relPath]
		rec, hasRec := e.state.Get(relPath)

		var locPtr *LocalFile
		if hasLoc {
			locPtr = &loc
		}
		var remPtr *client.FileItem
		if hasRem {
			remPtr = &rem
		}

		if hasRem && !hasLoc {
			// 远端存在，本地不存在
			if !hasRec {
				tasks = append(tasks, SyncTask{
					Type:    ActionDownload,
					RelPath: relPath,
					Remote:  remPtr,
					Reason:  "远端新文件 -> 下载到本地",
				})
			} else {
				// 历史上有同步记录
				if rem.Size == rec.RemoteSize && rem.ServerMtime == rec.RemoteMtime {
					// 远端无变动，说明本地被用户主动删除 -> 同步删除远端
					tasks = append(tasks, SyncTask{
						Type:    ActionDeleteR,
						RelPath: relPath,
						Remote:  remPtr,
						Reason:  "本地已删除且远端未修改 -> 删除远端文件",
					})
				} else {
					// 远端又有更新 -> 重新下载到本地
					tasks = append(tasks, SyncTask{
						Type:    ActionDownload,
						RelPath: relPath,
						Remote:  remPtr,
						Reason:  "远端更新 -> 下载到本地",
					})
				}
			}
		} else if hasLoc && !hasRem {
			// 本地存在，远端不存在
			if !hasRec {
				tasks = append(tasks, SyncTask{
					Type:    ActionUpload,
					RelPath: relPath,
					Local:   locPtr,
					Reason:  "本地新文件 -> 上传到远端",
				})
			} else {
				// 历史上同步过
				if loc.Size == rec.LocalSize && loc.Mtime == rec.LocalMtime {
					// 本地未修改，远端被删 -> 同步删除本地
					tasks = append(tasks, SyncTask{
						Type:    ActionDeleteL,
						RelPath: relPath,
						Local:   locPtr,
						Reason:  "远端已删除且本地未修改 -> 删除本地文件",
					})
				} else {
					// 本地被修改过 -> 重新上传到远端
					tasks = append(tasks, SyncTask{
						Type:    ActionUpload,
						RelPath: relPath,
						Local:   locPtr,
						Reason:  "本地修改 -> 上传到远端",
					})
				}
			}
		} else if hasLoc && hasRem {
			// 两端都存在
			if !hasRec {
				// 初始同步时双方都有此文件
				if loc.Size == rem.Size {
					// 大小一致，直接纳入同步状态管理
					e.state.Update(relPath, loc.Size, loc.Mtime, rem.Size, rem.ServerMtime)
				} else {
					// 大小不一致，根据修改时间决定
					if rem.ServerMtime > loc.Mtime {
						tasks = append(tasks, SyncTask{
							Type:    ActionConflict,
							RelPath: relPath,
							Local:   locPtr,
							Remote:  remPtr,
							Reason:  "初始冲突（远端较新）",
						})
					} else {
						tasks = append(tasks, SyncTask{
							Type:    ActionUpload,
							RelPath: relPath,
							Local:   locPtr,
							Remote:  remPtr,
							Reason:  "初始同步（本地较新） -> 上传覆写",
						})
					}
				}
			} else {
				locChanged := loc.Size != rec.LocalSize || loc.Mtime != rec.LocalMtime
				remChanged := rem.Size != rec.RemoteSize || rem.ServerMtime != rec.RemoteMtime

				if locChanged && !remChanged {
					tasks = append(tasks, SyncTask{
						Type:    ActionUpload,
						RelPath: relPath,
						Local:   locPtr,
						Remote:  remPtr,
						Reason:  "本地已修改 -> 上传到远端",
					})
				} else if !locChanged && remChanged {
					tasks = append(tasks, SyncTask{
						Type:    ActionDownload,
						RelPath: relPath,
						Local:   locPtr,
						Remote:  remPtr,
						Reason:  "远端已修改 -> 下载到本地",
					})
				} else if locChanged && remChanged {
					tasks = append(tasks, SyncTask{
						Type:    ActionConflict,
						RelPath: relPath,
						Local:   locPtr,
						Remote:  remPtr,
						Reason:  "两端均发生修改（冲突）",
					})
				}
			}
		}
	}

	return tasks
}

// Sync 执行一次完整的双向同步循环
func (e *Engine) Sync() error {
	log.Printf("[扫描] 正在扫描本地目录: %s ...", e.cfg.LocalSyncDir)
	localFiles, err := e.ScanLocal()
	if err != nil {
		return fmt.Errorf("扫描本地目录失败: %w", err)
	}
	log.Printf("[本地] 发现 %d 个有效文件", len(localFiles))

	log.Printf("[扫描] 正在扫描百度网盘同步空间: %s ...", e.cfg.RemoteRoot)
	remoteFiles, err := e.client.ListRecursive(e.cfg.RemoteRoot, func(dirsScanned, filesFound int) {
		if dirsScanned%10 == 0 || filesFound%100 == 0 {
			log.Printf("[远端扫描] 已扫描 %d 个目录，发现 %d 个文件...", dirsScanned, filesFound)
		}
	})
	if err != nil {
		return fmt.Errorf("扫描百度网盘失败: %w", err)
	}
	log.Printf("[远端] 发现 %d 个文件", len(remoteFiles))

	tasks := e.Plan(localFiles, remoteFiles)
	if len(tasks) == 0 {
		log.Printf("[完成] 两端数据已完全同步，无需任何传输。")
		return nil
	}

	log.Printf("[计划] 发现 %d 个待同步任务:", len(tasks))
	for i, task := range tasks {
		if i < 20 {
			log.Printf("  [%s] %s (%s)", task.Type, task.RelPath, task.Reason)
		}
	}
	if len(tasks) > 20 {
		log.Printf("  ... 以及其他 %d 项任务", len(tasks)-20)
	}

	if e.cfg.DryRun {
		log.Println("[DRY-RUN] 预览模式，未做实际修改。")
		return nil
	}

	// 并发执行任务队列
	concurrency := e.cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	taskChan := make(chan SyncTask, len(tasks))
	for _, t := range tasks {
		taskChan <- t
	}
	close(taskChan)

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errCount int

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskChan {
				if err := e.executeTask(task); err != nil {
					log.Printf("[ERROR] [%s] %s 失败: %v", task.Type, task.RelPath, err)
					errMu.Lock()
					errCount++
					errMu.Unlock()
				} else {
					log.Printf("[SUCCESS] [%s] %s 完成", task.Type, task.RelPath)
				}
			}
		}(i + 1)
	}

	wg.Wait()
	_ = e.state.Save()

	if errCount > 0 {
		log.Printf("[同步结束] %d 个任务失败，请查看日志", errCount)
		return fmt.Errorf("%d 个任务执行失败", errCount)
	}

	log.Println("[同步完成] 全部任务已成功执行完成！")
	return nil
}

func (e *Engine) executeTask(task SyncTask) error {
	remoteFullPath := filepath.ToSlash(filepath.Join(e.cfg.RemoteRoot, task.RelPath))
	localFullPath := filepath.Join(e.cfg.LocalSyncDir, filepath.FromSlash(task.RelPath))

	switch task.Type {
	case ActionDownload:
		if err := e.client.DownloadFile(remoteFullPath, localFullPath); err != nil {
			return err
		}
		info, err := os.Stat(localFullPath)
		if err == nil {
			e.state.Update(task.RelPath, info.Size(), info.ModTime().Unix(), task.Remote.Size, task.Remote.ServerMtime)
		}

	case ActionUpload:
		if err := e.client.UploadFile(localFullPath, remoteFullPath); err != nil {
			return err
		}
		info, err := os.Stat(localFullPath)
		if err == nil {
			// 上传后使用当前本地时间记录远端时间戳
			e.state.Update(task.RelPath, info.Size(), info.ModTime().Unix(), info.Size(), time.Now().Unix())
		}

	case ActionDeleteL:
		if err := os.Remove(localFullPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		e.state.Delete(task.RelPath)

	case ActionDeleteR:
		if err := e.client.DeleteFile(remoteFullPath); err != nil {
			return err
		}
		e.state.Delete(task.RelPath)

	case ActionConflict:
		// 冲突解决：将本地文件重命名为 conflict 备份，再下载远端版本
		timestamp := time.Now().Format("20060102-150405")
		ext := filepath.Ext(localFullPath)
		base := strings.TrimSuffix(localFullPath, ext)
		conflictPath := fmt.Sprintf("%s.conflict-%s%s", base, timestamp, ext)

		_ = os.Rename(localFullPath, conflictPath)
		log.Printf("[冲突处理] 本地版本已备份为: %s", conflictPath)

		if err := e.client.DownloadFile(remoteFullPath, localFullPath); err != nil {
			return err
		}
		info, err := os.Stat(localFullPath)
		if err == nil {
			e.state.Update(task.RelPath, info.Size(), info.ModTime().Unix(), task.Remote.Size, task.Remote.ServerMtime)
		}
	}

	return nil
}
