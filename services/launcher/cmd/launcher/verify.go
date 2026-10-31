package main

import (
	"archive/tar"
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"filippo.io/age"

	"launcher/internal/dockerclient"
	"launcher/internal/objstore"
	"launcher/internal/offsite"
)

// backupVerify ("launcher backup-verify", a chave privada no stdin) é o
// ensaio de restauro a partir do destino fora desta máquina: decifra a
// última configuração, o último dump de cada addon (e testa-o com
// pg_restore --list) e confirma que os backups da base que o backup.info
// lista estão lá. A chave só vive na memória deste processo.
func backupVerify(cfg config) int {
	m, err := cfg.offsiteMirror(quietLogger())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if m == nil {
		fmt.Fprintln(os.Stderr, "sem destino fora desta máquina -- não há de onde restaurar (basestack backup target set ...)")
		return 1
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(string(raw)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "a chave privada não é válida (esperava AGE-SECRET-KEY-...)")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	v := verifier{m: m, id: id, docker: dockerclient.New(cfg.dockerSocket), pgImage: cfg.postgresImage}

	ok := true
	for _, step := range []struct {
		name string
		run  func(context.Context) (string, error)
	}{
		{"configuração", v.config},
		{"base da plataforma", v.database},
		{"dumps dos addons", v.addons},
	} {
		msg, err := step.run(ctx)
		if err != nil {
			fmt.Printf("FALHOU  %s: %v\n", step.name, err)
			ok = false
			continue
		}
		fmt.Printf("ok      %s: %s\n", step.name, msg)
	}
	if !ok {
		return 1
	}
	return 0
}

var backupInfoRe = regexp.MustCompile(`(^|/)backup/[^/]+/backup\.info$`)

type verifier struct {
	m       *offsite.Mirror
	id      *age.X25519Identity
	docker  *dockerclient.Client
	pgImage string
}

// list devolve os objetos do destino de um bucket, pela chave local.
func (v verifier) list(ctx context.Context, bucket string) ([]objstore.Object, error) {
	prefix := v.m.Target.Prefix + bucket + "/"
	objs, err := v.m.Target.Store.List(ctx, v.m.Target.Bucket, prefix)
	if err != nil {
		return nil, err
	}
	out := objs[:0]
	for _, o := range objs {
		if key, ok := strings.CutSuffix(strings.TrimPrefix(o.Key, prefix), offsite.Suffix); ok {
			o.Key = key
			out = append(out, o)
		}
	}
	return out, nil
}

// open descarrega e decifra um objeto do destino.
func (v verifier) open(ctx context.Context, bucket, key string) (io.Reader, func(), error) {
	body, err := v.m.Target.Store.Get(ctx, v.m.Target.Bucket, v.m.Target.Key(bucket, key))
	if err != nil {
		return nil, nil, err
	}
	r, err := age.Decrypt(body, v.id)
	if err != nil {
		body.Close()
		return nil, nil, fmt.Errorf("%s não se decifra com esta chave: %w", key, err)
	}
	return r, func() { body.Close() }, nil
}

func (v verifier) config(ctx context.Context) (string, error) {
	objs, err := v.list(ctx, "config")
	if err != nil {
		return "", err
	}
	if len(objs) == 0 {
		return "", fmt.Errorf("nenhuma cópia no destino")
	}
	last := objs[len(objs)-1].Key // os nomes são datas: a última é a mais recente
	r, done, err := v.open(ctx, "config", last)
	if err != nil {
		return "", err
	}
	defer done()
	// A cópia da configuração está cifrada duas vezes: pelo basestack (a
	// mesma chave) e pelo espelho.
	inner, err := age.Decrypt(r, v.id)
	if err != nil {
		return "", fmt.Errorf("%s: a cópia interna não se decifra: %w", last, err)
	}
	found := map[string]bool{}
	tr := tar.NewReader(inner)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%s: tar ilegível: %w", last, err)
		}
		found[h.Name] = true
	}
	for _, need := range []string{"basestack/settings.json", "basestack/tls/ca.key", "volumes/auth-signing-key/signing-key.json"} {
		if !found[need] {
			return "", fmt.Errorf("%s não tem %s", last, need)
		}
	}
	return fmt.Sprintf("%s abre-se e tem settings.json, as chaves e a do auth (%d ficheiros)", strings.TrimSuffix(last, ".tar.age"), len(found)), nil
}

// database confirma que cada backup que o backup.info do pgBackRest lista
// tem o seu manifesto no destino.
func (v verifier) database(ctx context.Context) (string, error) {
	objs, err := v.list(ctx, "database")
	if err != nil {
		return "", err
	}
	have := map[string]bool{}
	var info string
	for _, o := range objs {
		have[o.Key] = true
		// <repo-path>/backup/<stanza>/backup.info (o repo-path do
		// pgBackRest entra nas chaves: var/lib/pgbackrest/...).
		if backupInfoRe.MatchString(o.Key) {
			info = o.Key
		}
	}
	if info == "" {
		return "", fmt.Errorf("o destino não tem o backup.info do pgBackRest")
	}
	r, done, err := v.open(ctx, "database", info)
	if err != nil {
		return "", err
	}
	defer done()
	var labels []string
	section := ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if label, _, ok := strings.Cut(line, "="); ok && section == "[backup:current]" {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("o backup.info não lista nenhum backup")
	}
	dir := path.Dir(info)
	for _, l := range labels {
		if !have[dir+"/"+l+"/backup.manifest"] {
			return "", fmt.Errorf("o backup %s está no backup.info mas não no destino", l)
		}
	}
	return fmt.Sprintf("%d backups no destino, o último %s", len(labels), labels[len(labels)-1]), nil
}

// addons decifra o dump mais recente de cada addon e corre pg_restore
// --list num container temporário: um dump que o lista inteiro restaura.
func (v verifier) addons(ctx context.Context) (string, error) {
	objs, err := v.list(ctx, "addon-backups")
	if err != nil {
		return "", err
	}
	latest := map[string]string{}
	for _, o := range objs {
		if d := path.Dir(o.Key); o.Key > latest[d] {
			latest[d] = o.Key
		}
	}
	if len(latest) == 0 {
		return "nenhum dump no destino (ainda sem addons com backups)", nil
	}
	var done []string
	for dir, key := range latest {
		entries, err := v.restoreList(ctx, key)
		if err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
		if entries == 0 {
			done = append(done, dir+" (base vazia)")
		} else {
			done = append(done, fmt.Sprintf("%s (%d objetos)", dir, entries))
		}
	}
	return strings.Join(done, ", "), nil
}

func (v verifier) restoreList(ctx context.Context, key string) (int, error) {
	r, closeBody, err := v.open(ctx, "addon-backups", key)
	if err != nil {
		return 0, err
	}
	defer closeBody()
	tmp, err := os.CreateTemp("", "verify-*.dump")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := io.Copy(tmp, r)
	if err != nil {
		return 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	if _, err := v.docker.InspectImage(ctx, v.pgImage); err != nil {
		if err := v.docker.PullImage(ctx, v.pgImage); err != nil {
			return 0, err
		}
	}
	id, err := v.docker.CreateContainer(ctx, "bs-backup-verify-"+fmt.Sprint(time.Now().UnixNano()), dockerclient.CreateContainerRequest{
		Image:      v.pgImage,
		Cmd:        []string{"pg_restore", "--list", "/tmp/backup.dump"},
		HostConfig: &dockerclient.CreateHostConfig{NetworkMode: "none"},
	})
	if err != nil {
		return 0, err
	}
	defer func() { _ = v.docker.RemoveContainer(context.WithoutCancel(ctx), id, true) }()
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Name: "backup.dump", Mode: 0o644, Size: size})
		if err == nil {
			_, err = io.Copy(tw, tmp)
		}
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	if err := v.docker.CopyToContainer(ctx, id, "/tmp", pr); err != nil {
		return 0, err
	}
	if err := v.docker.StartContainer(ctx, id); err != nil {
		return 0, err
	}
	code, err := v.docker.WaitContainer(ctx, id)
	if err != nil {
		return 0, err
	}
	out, _ := v.docker.ContainerLogs(ctx, id, 0)
	if code != 0 {
		return 0, fmt.Errorf("pg_restore --list falhou (%d): %s", code, lastLines(out, 3))
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if line != "" && !strings.HasPrefix(line, ";") {
			n++
		}
	}
	return n, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}
