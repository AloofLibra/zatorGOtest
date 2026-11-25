package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func menuCustomStrategies(r *bufio.Reader, out, errw io.Writer) int {
	for {
		s, err := menuServer()
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		payload, err := s.CLICustom(context.Background(), "list", nil)
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		b, _ := json.Marshal(payload)
		var data struct {
			Strategies []struct {
				ID       int    `json:"id"`
				Block    string `json:"block"`
				Spec     string `json:"spec"`
				Enabled  bool   `json:"enabled"`
				Comment  string `json:"comment"`
				Assigned string `json:"assigned"`
			} `json:"strategies"`
		}
		_ = json.Unmarshal(b, &data)

		fmt.Fprintf(out, "\n%s\n", menuColor("96;1", "СВОИ СТРАТЕГИИ"))
		if len(data.Strategies) == 0 {
			fmt.Fprintln(out, "Записей пока нет.")
		} else {
			for _, st := range data.Strategies {
				en := "вкл"
				if !st.Enabled {
					en = "выкл"
				}
				asg := "—"
				if st.Assigned != "" {
					asg = "#" + st.Assigned
				}
				fmt.Fprintf(out, " %2d. [%-3s] %-4s %-5s %s (%s)\n", st.ID, st.Block, en, asg, st.Spec, st.Comment)
			}
		}
		fmt.Fprintln(out, "\n 1. Добавить стратегию (одна строка)\n 2. Массовый импорт (blockcheck2 / nfqws2)\n 3. Вкл/выкл стратегию\n 4. Удалить стратегию\n 5. Применить к конфигу\n 0. Назад")
		choice, e := menuRead(r, out, "Выбор: ")
		if e != nil || choice == "0" {
			return menuBack
		}
		ctx := context.Background()
		switch choice {
		case "1":
			blk, e := menuRead(r, out, "Блок (tpl/p4/p5/p6/p7): ")
			if e != nil || blk == "" {
				continue
			}
			spec, e := menuRead(r, out, "Строка стратегии (--lua-desync=...): ")
			if e != nil || spec == "" {
				continue
			}
			comment, _ := menuRead(r, out, "Комментарий (Enter — пусто): ")
			res, err := s.CLICustom(ctx, "add", map[string]string{"block": blk, "value": spec, "comment": comment})
			_ = menuResult(out, errw, res, err)
		case "2":
			blk, e := menuRead(r, out, "Блок импорта (tpl/p4/p5/p6/p7): ")
			if e != nil || blk == "" {
				continue
			}
			fmt.Fprintln(out, "Вставьте строки и завершите ввод строкой END:")
			var lines []string
			for {
				ln, re := r.ReadString('\n')
				trim := strings.TrimSpace(ln)
				if trim == "END" {
					break
				}
				if trim != "" {
					lines = append(lines, strings.TrimRight(ln, "\r\n"))
				}
				if re != nil {
					break
				}
			}
			if len(lines) == 0 {
				continue
			}
			res, err := s.CLICustom(ctx, "import", map[string]string{"block": blk, "text": strings.Join(lines, "\n")})
			_ = menuResult(out, errw, res, err)
		case "3":
			id, _ := menuRead(r, out, "ID стратегии: ")
			val, _ := menuRead(r, out, "Состояние (1=вкл, 0=выкл): ")
			res, err := s.CLICustom(ctx, "enable", map[string]string{"id": id, "value": val})
			_ = menuResult(out, errw, res, err)
		case "4":
			id, _ := menuRead(r, out, "ID стратегии для удаления: ")
			res, err := s.CLICustom(ctx, "delete", map[string]string{"id": id})
			_ = menuResult(out, errw, res, err)
		case "5":
			ok, _ := menuConfirm(r, out, "Переинжектить в config и перезапустить zapret2?")
			restart := "0"
			if ok {
				restart = "1"
			}
			res, err := s.CLICustom(ctx, "apply", map[string]string{"restart": restart})
			_ = menuResult(out, errw, res, err)
		default:
			fmt.Fprintln(out, "Некорректный выбор.")
		}
	}
}
