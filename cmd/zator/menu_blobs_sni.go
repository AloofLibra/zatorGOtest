package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func menuBlobsAndSNI(r *bufio.Reader, out, errw io.Writer) int {
	for {
		s, err := menuServer()
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		ctx := context.Background()
		tlsRaw, err := s.CLISetting(ctx, "tls_blob")
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		blobsRaw, err := s.CLIBlobs(ctx, "list", nil)
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		tb, _ := json.Marshal(tlsRaw)
		var tls struct {
			CurrentMode   string            `json:"current_mode"`
			CurrentBlob   string            `json:"current_blob"`
			ProfileBlobs  map[string]string `json:"profile_blobs"`
			ProfileModes  map[string]string `json:"profile_modes"`
			ProfileSNIs   map[string]string `json:"profile_snis"`
			ProfileSizes  map[string]string `json:"profile_sizes"`
			GlobalSize    string            `json:"global_clone_size"`
			AvailableTLS  []string          `json:"available_blobs"`
			AvailableQUIC []string          `json:"quic_blobs"`
		}
		_ = json.Unmarshal(tb, &tls)

		bb, _ := json.Marshal(blobsRaw)
		var bp struct {
			Dir         string `json:"dir"`
			ConfigDirty bool   `json:"config_dirty"`
			Blobs       []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				File string `json:"file"`
				Refs int    `json:"refs"`
			} `json:"blobs"`
			SNI *struct {
				Profiles []struct {
					ID     string `json:"id"`
					Title  string `json:"title"`
					Pin    string `json:"pin"`
					Pool   string `json:"pool"`
					Policy string `json:"policy"`
				} `json:"profiles"`
			} `json:"sni"`
		}
		_ = json.Unmarshal(bb, &bp)

		fmt.Fprintf(out, "\n%s\n", menuColor("96;1", "БЛОБЫ И SNI"))
		fmt.Fprintf(out, "Глобальный TLS: режим=%s файл=%s (global_clone_size=%s)\n", tls.CurrentMode, tls.CurrentBlob, tls.GlobalSize)
		fmt.Fprintln(out, "Карта профилей:")
		for _, pid := range []string{"1", "2", "3", "4", "5", "8"} {
			mode := tls.ProfileModes[pid]
			if pid == "5" {
				mode = "udp"
			}
			tag := "[blob]"
			if pid != "5" && mode == "clone" {
				if tls.ProfileSNIs[pid] != "" {
					tag = "[clone-solo]"
				} else {
					tag = "[clone-pool]"
				}
			}
			blobVal := tls.ProfileBlobs[pid]
			if blobVal == "" {
				blobVal = "(глобальный)"
			}
			fmt.Fprintf(out, "  П%-2s %-13s блоб=%-26s size=%s\n", pid, tag, blobVal, tls.ProfileSizes[pid])
		}
		if bp.SNI != nil {
			fmt.Fprintln(out, "SNI-пулы:")
			for _, p := range bp.SNI.Profiles {
				pin := p.Pin
				if pin == "" {
					pin = "—"
				}
				fmt.Fprintf(out, "  П%-2s pin=%-18s %s (%s)\n", p.ID, pin, p.Pool, p.Policy)
			}
		}
		fmt.Fprintf(out, "Свои блобы (%s, dirty=%v): %d шт.\n", bp.Dir, bp.ConfigDirty, len(bp.Blobs))
		fmt.Fprintln(out, "\n 1. Глобальный TLS-блоб\n 2. Блоб профиля (1,2,3,4,5 QUIC,8)\n 3. Режим профиля (classic/clone) и clone_size\n 4. SNI (pin / pool-set / pool-custom / pool-clear / policy / набор)\n 5. Свои блобы (добавить / удалить / применить)\n 0. Назад")
		choice, e := menuRead(r, out, "Выбор: ")
		if e != nil || choice == "0" {
			return menuBack
		}
		switch choice {
		case "1":
			fmt.Fprintf(out, "Доступны: fake_default_tls %s\n", strings.Join(tls.AvailableTLS, " "))
			val, _ := menuRead(r, out, "Значение: ")
			res, err := s.CLISetSetting(ctx, "tls_blob", map[string]string{"value": val})
			_ = menuResult(out, errw, res, err)
		case "2":
			prof, _ := menuRead(r, out, "Профиль (1,2,3,4,5,8): ")
			if prof == "5" {
				fmt.Fprintf(out, "QUIC-блобы: %s\n", strings.Join(tls.AvailableQUIC, " "))
			} else {
				fmt.Fprintf(out, "TLS-блобы: fake_default_tls %s\n", strings.Join(tls.AvailableTLS, " "))
			}
			val, _ := menuRead(r, out, "Файл (Enter — сброс к глобальному): ")
			res, err := s.CLISetSetting(ctx, "tls_blob_profile", map[string]string{"profile": prof, "value": val})
			_ = menuResult(out, errw, res, err)
		case "3":
			prof, _ := menuRead(r, out, "Профиль (1,2,3,4,8): ")
			mode, _ := menuRead(r, out, "Режим (classic/clone, Enter — не менять): ")
			if mode != "" {
				res, err := s.CLISetSetting(ctx, "fake_mode", map[string]string{"profile": prof, "value": mode})
				_ = menuResult(out, errw, res, err)
			}
			sz, _ := menuRead(r, out, "clone_size (64..1200 / global, Enter — не менять): ")
			if sz != "" {
				res, err := s.CLISetSetting(ctx, "clone_size", map[string]string{"profile": prof, "value": sz})
				_ = menuResult(out, errw, res, err)
			}
		case "4":
			sub, _ := menuRead(r, out, "Команда SNI (pin/pool-set/pool-custom/pool-clear/policy/set-write/set-delete): ")
			argStr, _ := menuRead(r, out, "Аргументы через пробел: ")
			args := append([]string{sub}, strings.Fields(argStr)...)
			_ = cmdSNI(args, out, errw)
		case "5":
			sub, _ := menuRead(r, out, "Команда блобов (list/add/edit/delete/apply): ")
			argStr, _ := menuRead(r, out, "Аргументы через пробел: ")
			args := append([]string{sub}, strings.Fields(argStr)...)
			_ = cmdBlobs(args, out, errw)
		default:
			fmt.Fprintln(out, "Некорректный выбор.")
		}
	}
}
