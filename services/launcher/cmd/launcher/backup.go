package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"launcher/internal/objstore"
)

// Subcomandos para o "basestack backup" (corre-os com docker compose exec
// no container do launcher, que é quem chega ao storage da plataforma).

const (
	configBucket = "config"
	configKeep   = 30
	maxConfig    = 64 << 20
)

var configNameRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z\.tar\.age$`)

// backupConfig ("launcher backup-config <nome>") guarda o que vem no stdin
// -- a configuração da instalação, já cifrada pelo basestack -- no bucket
// config, e apaga as cópias além das configKeep mais recentes.
func backupConfig(cfg config, args []string) int {
	if len(args) != 1 || !configNameRe.MatchString(args[0]) {
		fmt.Fprintln(os.Stderr, "uso: launcher backup-config <AAAAMMDDTHHMMSSZ.tar.age> < ficheiro")
		return 2
	}
	store, ok := backupStoreOrExplain(cfg)
	if !ok {
		return 1
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxConfig+1))
	if err != nil || len(data) == 0 || len(data) > maxConfig {
		fmt.Fprintln(os.Stderr, "a configuração não chegou inteira pelo stdin")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := store.Put(ctx, configBucket, args[0], bytes.NewReader(data), int64(len(data))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if objs, err := store.List(ctx, configBucket, ""); err == nil && len(objs) > configKeep {
		for _, o := range objs[:len(objs)-configKeep] {
			_ = store.Delete(ctx, configBucket, o.Key)
		}
	}
	return 0
}

// backupStatus ("launcher backup-status") resume o que está no storage:
// o tamanho de cada bucket, a última configuração e o último dump de cada
// addon.
func backupStatus(cfg config) int {
	store, ok := backupStoreOrExplain(cfg)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	failed := false
	for _, bucket := range []string{"database", configBucket, "addon-backups"} {
		objs, err := store.List(ctx, bucket, "")
		if err != nil {
			fmt.Printf("%-14s erro: %v\n", bucket, err)
			failed = true
			continue
		}
		var total int64
		for _, o := range objs {
			total += o.Size
		}
		fmt.Printf("%-14s %d objetos, %s\n", bucket, len(objs), humanBytes(total))
		switch bucket {
		case configBucket:
			if len(objs) == 0 {
				fmt.Println("               nenhuma cópia (basestack backup init, depois basestack up)")
			} else {
				last := objs[len(objs)-1]
				fmt.Printf("               última: %s (%s)\n", last.Key, humanBytes(last.Size))
			}
		case "addon-backups":
			printLatestPerAddon(objs)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// printLatestPerAddon: uma linha por <projeto>/<ambiente>/<addon>, com o
// dump mais recente.
func printLatestPerAddon(objs []objstore.Object) {
	latest := map[string]objstore.Object{}
	count := map[string]int{}
	for _, o := range objs {
		dir := path.Dir(o.Key)
		count[dir]++
		if cur, ok := latest[dir]; !ok || o.Key > cur.Key {
			latest[dir] = o
		}
	}
	dirs := make([]string, 0, len(latest))
	for d := range latest {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		o := latest[d]
		fmt.Printf("               %s: %d cópias, última %s (%s)\n", d, count[d], strings.TrimSuffix(path.Base(o.Key), ".dump"), humanBytes(o.Size))
	}
}

func backupStoreOrExplain(cfg config) (*objstore.Client, bool) {
	store, err := cfg.backupStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, false
	}
	if store == nil {
		fmt.Fprintln(os.Stderr, "o storage da plataforma não está configurado neste launcher (falta LAUNCHER_BACKUP_STORAGE_URL)")
		return nil, false
	}
	return store, true
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
