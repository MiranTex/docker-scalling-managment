package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

// Backups da plataforma. A base (pgBackRest) e os dumps dos addons (o
// launcher) vão sozinhos para o storage da plataforma; aqui fica o que só
// esta máquina tem -- a configuração e as chaves -- e os comandos para ver
// o estado e forçar um backup.

const backupUsage = `uso:
  basestack backup init [--force]  gera a chave que cifra a cópia da configuração
  basestack backup status          o que está no storage da plataforma
  basestack backup now [--full]    backup da base e da configuração agora`

func cmdBackup(args []string) error {
	if len(args) == 0 {
		return errors.New(backupUsage)
	}
	switch args[0] {
	case "init":
		return cmdBackupInit(args[1:])
	case "status":
		return cmdBackupStatus(args[1:])
	case "now":
		return cmdBackupNow(args[1:])
	}
	return fmt.Errorf("backup %q desconhecido\n%s", args[0], backupUsage)
}

// cmdBackupInit gera um par de chaves age: a pública fica em
// settings.json e cifra as cópias; a privada só se mostra uma vez. Quem
// roubar o storage (ou esta máquina) não abre as cópias sem ela.
func cmdBackupInit(args []string) error {
	fs := flag.NewFlagSet("backup init", flag.ContinueOnError)
	force := fs.Bool("force", false, "gerar outra chave (as cópias antigas continuam a precisar da antiga)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, dir, err := loadSettings()
	if err != nil {
		return err
	}
	if s.BackupRecipient != "" && !*force {
		return errors.New("esta instalação já tem chave de backup -- uma nova (--force) não abre as cópias antigas, guarda a antiga também")
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	s.BackupRecipient = id.Recipient().String()
	if err := saveSettings(dir, s); err != nil {
		return err
	}
	fmt.Printf(`Chave privada da cópia da configuração -- guarda-a FORA deste servidor
(gestor de passwords, por exemplo). Não fica guardada em lado nenhum:
sem ela, a cópia da configuração não se abre.

  %s

`, id.String())
	if err := uploadConfig(dir, s, true); err != nil {
		fmt.Printf("Aviso: a primeira cópia não foi feita (%v).\n  Faz-se no próximo \"basestack up\" ou com \"basestack backup now\".\n", err)
		return nil
	}
	fmt.Println("Primeira cópia da configuração guardada no storage da plataforma.")
	return nil
}

func cmdBackupStatus(args []string) error {
	s, dir, err := loadSettings()
	if err != nil {
		return err
	}
	if s.BackupRecipient == "" {
		fmt.Print("Configuração:  sem cópia -- falta a chave (basestack backup init)\n\n")
	}
	fmt.Println("Storage da plataforma:")
	if err := compose(dir, "exec", "-T", "launcher", "launcher", "backup-status").Run(); err != nil {
		return fmt.Errorf("não consegui ler o storage (a plataforma está a correr?): %w", err)
	}
	fmt.Println("\nBase da plataforma (pgBackRest):")
	return compose(dir, "exec", "-T", "-u", "postgres", "database", "pgbackrest", "--stanza=basestack", "info").Run()
}

func cmdBackupNow(args []string) error {
	fs := flag.NewFlagSet("backup now", flag.ContinueOnError)
	full := fs.Bool("full", false, "backup completo da base (por defeito incremental; o primeiro é sempre completo)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, dir, err := loadSettings()
	if err != nil {
		return err
	}
	kind := "incr"
	if *full {
		kind = "full"
	}
	fmt.Println("Base da plataforma:")
	if err := compose(dir, "exec", "-T", "-u", "postgres", "database", "run-backup.sh", kind).Run(); err != nil {
		return fmt.Errorf("o backup da base falhou: %w", err)
	}
	if s.BackupRecipient == "" {
		fmt.Println("\nConfiguração: sem cópia -- falta a chave (basestack backup init).")
	} else if err := uploadConfig(dir, s, true); err != nil {
		return fmt.Errorf("a cópia da configuração falhou: %w", err)
	} else {
		fmt.Println("\nConfiguração: cópia guardada.")
	}
	fmt.Println("\nOs addons Postgres dos projetos têm um dump por dia, com cópia no storage\n(no portal: Addons > Backup agora).")
	return nil
}

// lastConfigFile guarda o hash da última configuração enviada: o up só
// manda uma cópia nova quando ela mudou.
const lastConfigFile = ".last-config-backup"

// uploadConfig junta a configuração (settings.json e tls/) e a chave de
// assinatura do auth num tar, cifra-o com a chave pública e entrega-o ao
// launcher, que o guarda no bucket config. Sem force, não faz nada se
// nada mudou desde a última cópia.
func uploadConfig(dir string, s Settings, force bool) error {
	if s.BackupRecipient == "" {
		return errors.New("falta a chave (basestack backup init)")
	}
	recipient, err := age.ParseX25519Recipient(s.BackupRecipient)
	if err != nil {
		return fmt.Errorf("backupRecipient inválido em settings.json: %w", err)
	}
	plain, err := configArchive(dir)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(plain)
	hash := hex.EncodeToString(sum[:])
	if last, err := os.ReadFile(filepath.Join(dir, lastConfigFile)); err == nil && !force && strings.TrimSpace(string(last)) == hash {
		return nil
	}

	var sealed bytes.Buffer
	w, err := age.Encrypt(&sealed, recipient)
	if err != nil {
		return err
	}
	if _, err := w.Write(plain); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	name := time.Now().UTC().Format("20060102T150405Z") + ".tar.age"
	cmd := compose(dir, "exec", "-T", "launcher", "launcher", "backup-config", name)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &sealed, io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return os.WriteFile(filepath.Join(dir, lastConfigFile), []byte(hash+"\n"), 0o600)
}

// configArchive: settings.json, tls/ e o volume do auth (a chave que
// assina os tokens), em config/... e auth/... -- o compose.yml e config/
// regeneram-se do settings.json. As datas ficam a zero: o mesmo conteúdo
// dá o mesmo tar (e o mesmo hash).
func configArchive(dir string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, mode int64, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0)}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	settings, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return nil, err
	}
	if err := add("basestack/settings.json", 0o600, settings); err != nil {
		return nil, err
	}
	err = filepath.WalkDir(filepath.Join(dir, "tls"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return add("basestack/"+filepath.ToSlash(rel), 0o600, data)
	})
	if err != nil {
		return nil, err
	}

	// O volume do auth, pelo docker cp do container (tar no stdout).
	id, err := composeOutput(dir, "ps", "-q", "auth")
	if err != nil || id == "" {
		return nil, fmt.Errorf("o auth não está a correr -- a chave dele vai na cópia: %v", err)
	}
	var authTar, stderr bytes.Buffer
	cp := exec.Command("docker", "cp", id+":/var/lib/auth", "-")
	cp.Stdout, cp.Stderr = &authTar, &stderr
	if err := cp.Run(); err != nil {
		return nil, fmt.Errorf("copiando a chave do auth: %s", strings.TrimSpace(stderr.String()))
	}
	tr := tar.NewReader(&authTar)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		if err := add("volumes/auth-signing-key/"+strings.TrimPrefix(h.Name, "auth/"), 0o600, data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func composeOutput(dir string, args ...string) (string, error) {
	cmd := compose(dir, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}
