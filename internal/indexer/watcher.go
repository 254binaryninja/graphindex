package indexer

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (idx *Indexer) Watch() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := addDirs(watcher, idx.root); err != nil {
		watcher.Close()
		return err
	}

	pending := make(map[string]*time.Timer)
	var mu sync.Mutex

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				ext := filepath.Ext(event.Name)
				if !supportedExts[ext] {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					mu.Lock()
					if t, ok := pending[event.Name]; ok {
						t.Stop()
					}
					pending[event.Name] = time.AfterFunc(300*time.Millisecond, func() {
						mu.Lock()
						delete(pending, event.Name)
						mu.Unlock()
						if err := idx.IndexFile(event.Name); err != nil {
							log.Printf("index error %s: %v", event.Name, err)
						}
					})
					mu.Unlock()
				} else if event.Op&fsnotify.Remove != 0 {
					_ = idx.RemoveFile(event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()

	return nil
}

func addDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			switch name {
			case ".git", "vendor", "node_modules", ".graphindex", "dist", "build", ".next", ".angular", ".turbo", "coverage", "__pycache__":
				return filepath.SkipDir
			}
			return watcher.Add(path)
		}
		return nil
	})
}
