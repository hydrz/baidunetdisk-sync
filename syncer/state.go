package syncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileRecord struct {
	LocalSize   int64 `json:"local_size"`
	LocalMtime  int64 `json:"local_mtime"`
	RemoteSize  int64 `json:"remote_size"`
	RemoteMtime int64 `json:"remote_mtime"`
	LastSync    int64 `json:"last_sync"`
}

type StateManager struct {
	mu       sync.RWMutex
	filePath string
	records  map[string]FileRecord
}

func NewStateManager(localSyncDir string) (*StateManager, error) {
	statePath := filepath.Join(localSyncDir, ".sync_state.json")
	sm := &StateManager{
		filePath: statePath,
		records:  make(map[string]FileRecord),
	}

	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &sm.records)
	}

	return sm, nil
}

func (s *StateManager) Get(relPath string) (FileRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[relPath]
	return rec, ok
}

func (s *StateManager) Update(relPath string, localSize, localMtime, remoteSize, remoteMtime int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[relPath] = FileRecord{
		LocalSize:   localSize,
		LocalMtime:  localMtime,
		RemoteSize:  remoteSize,
		RemoteMtime: remoteMtime,
		LastSync:    time.Now().Unix(),
	}
}

func (s *StateManager) Delete(relPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, relPath)
}

func (s *StateManager) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.records, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}

	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err == nil {
		if err := os.Rename(tmpPath, s.filePath); err == nil {
			return nil
		}
		_ = os.Remove(tmpPath)
	}
	return os.WriteFile(s.filePath, data, 0644)
}
