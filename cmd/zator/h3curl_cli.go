package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func cmdH3Curl(args []string, stdout, stderr io.Writer) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "status":
		res, err := s.CLISetting(ctx, "h3_curl")
		return menuResult(stdout, stderr, res, err)
	case "install", "update", "remove":
		res, err := s.CLISetSetting(ctx, "h3_curl", map[string]string{"action": sub})
		return menuResult(stdout, stderr, res, err)
	default:
		fmt.Fprintln(stderr, "использование: zator h3-curl status|install|update|remove")
		return 2
	}
}

func menuH3Curl(r *bufio.Reader, out, errw io.Writer) int {
	for {
		s, err := menuServer()
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		stRaw, err := s.CLISetting(ctx, "h3_curl")
		cancel()
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		b, _ := json.Marshal(stRaw)
		var st struct {
			Found   bool   `json:"found"`
			Path    string `json:"path"`
			Arch    string `json:"arch"`
			Managed bool   `json:"managed"`
		}
		_ = json.Unmarshal(b, &st)

		fmt.Fprintf(out, "\n%s\n", menuColor("96;1", "H3-CURL (HTTP/3 ДЛЯ ПРОФИЛЯ 5)"))
		if st.Found {
			origin := "системный/внешний"
			if st.Managed {
				origin = "управляемый Zator"
			}
			fmt.Fprintf(out, "Статус: %s (%s, арх. %s, %s)\n", menuColor("32", "найден"), st.Path, st.Arch, origin)
		} else {
			fmt.Fprintf(out, "Статус: %s (арх. %s)\n", menuColor("33", "не установлен"), st.Arch)
		}
		fmt.Fprintln(out, "\n 1. Установить h3-curl\n 2. Обновить / переустановить\n 3. Удалить управляемый h3-curl\n 0. Назад")
		c, e := menuRead(r, out, "Выбор: ")
		if e != nil || c == "0" {
			return menuBack
		}
		switch c {
		case "1":
			_ = cmdH3Curl([]string{"install"}, out, errw)
		case "2":
			_ = cmdH3Curl([]string{"update"}, out, errw)
		case "3":
			_ = cmdH3Curl([]string{"remove"}, out, errw)
		default:
			fmt.Fprintln(out, "Некорректный выбор.")
		}
	}
}

func cmdCustom(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		block := ""
		if len(args) > 1 {
			block = args[1]
		}
		res, err := s.CLICustom(ctx, "list", map[string]string{"block": block})
		return menuResult(stdout, stderr, res, err)
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator custom add <tpl|p4|p5|p6|p7> <spec> [comment]")
			return 2
		}
		comment := ""
		if len(args) > 3 {
			comment = strings.Join(args[3:], " ")
		}
		res, err := s.CLICustom(ctx, "add", map[string]string{"block": args[1], "value": args[2], "comment": comment})
		return menuResult(stdout, stderr, res, err)
	case "edit":
		if len(args) < 4 {
			fmt.Fprintln(stderr, "использование: zator custom edit <id> <block> <spec> [comment]")
			return 2
		}
		vals := map[string]string{"id": args[1], "block": args[2], "value": args[3]}
		if len(args) > 4 {
			vals["comment"] = strings.Join(args[4:], " ")
		}
		res, err := s.CLICustom(ctx, "edit", vals)
		return menuResult(stdout, stderr, res, err)
	case "enable", "disable":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "использование: zator custom %s <id>\n", args[0])
			return 2
		}
		val := "1"
		if args[0] == "disable" {
			val = "0"
		}
		res, err := s.CLICustom(ctx, "enable", map[string]string{"id": args[1], "value": val})
		return menuResult(stdout, stderr, res, err)
	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "использование: zator custom delete <id>")
			return 2
		}
		res, err := s.CLICustom(ctx, "delete", map[string]string{"id": args[1]})
		return menuResult(stdout, stderr, res, err)
	case "import":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator custom import <tpl|p4|p5|p6|p7> <file|->")
			return 2
		}
		var raw []byte
		if args[2] == "-" {
			raw, err = io.ReadAll(os.Stdin)
		} else {
			raw, err = os.ReadFile(args[2])
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		res, err := s.CLICustom(ctx, "import", map[string]string{"block": args[1], "text": string(raw)})
		return menuResult(stdout, stderr, res, err)
	case "apply":
		restart := "1"
		if len(args) > 1 && (args[1] == "--no-restart" || args[1] == "0") {
			restart = "0"
		}
		res, err := s.CLICustom(ctx, "apply", map[string]string{"restart": restart})
		return menuResult(stdout, stderr, res, err)
	default:
		fmt.Fprintln(stderr, "использование: zator custom list|add|edit|enable|disable|delete|import|apply")
		return 2
	}
}

func cmdBlobs(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		res, err := s.CLIBlobs(ctx, "list", nil)
		return menuResult(stdout, stderr, res, err)
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator blobs add <name> <file> [comment]")
			return 2
		}
		comment := ""
		if len(args) > 3 {
			comment = strings.Join(args[3:], " ")
		}
		res, err := s.CLIBlobs(ctx, "add", map[string]string{"value": args[1], "file": args[2], "comment": comment})
		return menuResult(stdout, stderr, res, err)
	case "edit":
		if len(args) < 4 {
			fmt.Fprintln(stderr, "использование: zator blobs edit <id> <name> <file> [comment]")
			return 2
		}
		vals := map[string]string{"id": args[1], "value": args[2], "file": args[3]}
		if len(args) > 4 {
			vals["comment"] = strings.Join(args[4:], " ")
		}
		res, err := s.CLIBlobs(ctx, "edit", vals)
		return menuResult(stdout, stderr, res, err)
	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "использование: zator blobs delete <id>")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "delete", map[string]string{"id": args[1]})
		return menuResult(stdout, stderr, res, err)
	case "apply":
		restart := "1"
		if len(args) > 1 && (args[1] == "--no-restart" || args[1] == "0") {
			restart = "0"
		}
		res, err := s.CLIBlobs(ctx, "apply", map[string]string{"restart": restart})
		return menuResult(stdout, stderr, res, err)
	default:
		fmt.Fprintln(stderr, "использование: zator blobs list|add|edit|delete|apply")
		return 2
	}
}

func cmdSNI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"status"}
	}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	switch args[0] {
	case "status":
		res, err := s.CLIBlobs(ctx, "list", nil)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return menuResult(stdout, stderr, res["sni"], nil)
	case "pin":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "использование: zator sni pin <profile> [domain]")
			return 2
		}
		val := ""
		if len(args) > 2 {
			val = args[2]
		}
		res, err := s.CLIBlobs(ctx, "sni_pin", map[string]string{"profile": args[1], "value": val})
		return menuResult(stdout, stderr, res, err)
	case "pool-set":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator sni pool-set <profile> <@set>")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "sni_pool_assign", map[string]string{"profile": args[1], "value": args[2]})
		return menuResult(stdout, stderr, res, err)
	case "pool-custom":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator sni pool-custom <profile> <d1> [d2 ...]")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "sni_pool_custom", map[string]string{"profile": args[1], "list": strings.Join(args[2:], " ")})
		return menuResult(stdout, stderr, res, err)
	case "pool-clear":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "использование: zator sni pool-clear <profile>")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "sni_pool_clear", map[string]string{"profile": args[1]})
		return menuResult(stdout, stderr, res, err)
	case "policy":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator sni policy <profile> <rr|rand>")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "sni_pool_policy", map[string]string{"profile": args[1], "value": args[2]})
		return menuResult(stdout, stderr, res, err)
	case "set-write":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "использование: zator sni set-write <name> <d1> [d2 ...]")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "sni_set_write", map[string]string{"name": args[1], "list": strings.Join(args[2:], " ")})
		return menuResult(stdout, stderr, res, err)
	case "set-delete":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "использование: zator sni set-delete <name>")
			return 2
		}
		res, err := s.CLIBlobs(ctx, "sni_set_delete", map[string]string{"name": args[1]})
		return menuResult(stdout, stderr, res, err)
	default:
		fmt.Fprintln(stderr, "использование: zator sni status|pin|pool-set|pool-custom|pool-clear|policy|set-write|set-delete")
		return 2
	}
}
