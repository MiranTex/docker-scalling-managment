package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

func cmdExec(args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	replica := fs.String("replica", "", "nome do container (por defeito, a primeira réplica a correr)")
	debug := fs.Bool("debug", false, "container busybox ao lado do alvo (para imagens sem shell)")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("uso: bs exec <projeto>/<ambiente>/<serviço ou addon> [--replica X] [--debug]")
	}
	t, err := parseTarget(fs.Arg(0))
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	c := newClient(cfg)

	// Serviço primeiro; se não houver um com esse nome, é um addon.
	var tk ticket
	body := map[string]any{"replica": *replica, "debug": *debug}
	err = c.call("POST", t.envPath("services", t.name, "terminal"), body, &tk)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && *replica == "" {
		err = c.call("POST", t.envPath("addons", t.name, "terminal"), body, &tk)
	}
	if errors.As(err, &apiErr) && apiErr.Code == "no_shell" {
		return fmt.Errorf("%s\n  tenta: bs exec %s --debug", apiErr.Message, fs.Arg(0))
	}
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, c.socketURL(tk), nil)
	if err != nil {
		return fmt.Errorf("abrindo o terminal: %w", err)
	}
	conn.SetReadLimit(1 << 20)
	defer conn.CloseNow()

	stdin := int(os.Stdin.Fd())
	if term.IsTerminal(stdin) {
		old, err := term.MakeRaw(stdin)
		if err != nil {
			return err
		}
		defer term.Restore(stdin, old)
	}
	fmt.Fprintf(os.Stderr, "Ligado a %s. Para sair: exit (ou Ctrl+D).\r\n", tk.Container)

	sendSize := func() {
		cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			return
		}
		msg, _ := json.Marshal(map[string]any{"type": "resize", "rows": rows, "cols": cols})
		_ = conn.Write(ctx, websocket.MessageText, msg)
	}
	sendSize()
	stopResize := onResize(sendSize)
	defer stopResize()

	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) && ce.Reason != "" && ce.Reason != "sessão terminada" {
				fmt.Fprintf(os.Stderr, "\r\n[%s]\r\n", ce.Reason)
			}
			return nil
		}
		_, _ = os.Stdout.Write(data)
	}
}

// reorder põe as flags antes dos argumentos, para "bs exec alvo --debug"
// funcionar como "bs exec --debug alvo" (o pacote flag pára no primeiro
// argumento).
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			// --replica X: o valor vem a seguir (a não ser --replica=X).
			if (a == "--replica" || a == "-replica" || a == "--port" || a == "-port") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}
