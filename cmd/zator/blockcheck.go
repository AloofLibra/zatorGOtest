package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/AloofLibra/zator/internal/app"
	"github.com/AloofLibra/zator/internal/service"
	"github.com/AloofLibra/zator/internal/sweep"
)

func cmdBlockcheck(args []string, stdout, stderr io.Writer) (exitCode int) {
	if len(args) != 2 || args[0] != "-y" {
		fmt.Fprintln(stderr, "Blockcheck2 temporarily stops zapret2; run: zator blockcheck2 -y <domain>")
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	url, _, ok := sweep.NormalizeTestURL(args[1])
	if !ok {
		fmt.Fprintln(stderr, "некорректный адрес проверки")
		return 2
	}
	blockcheck := filepath.Join(a.Paths.ZapretRoot, "blockcheck2.sh")
	if st, err := os.Stat(blockcheck); err != nil || !st.Mode().IsRegular() {
		fmt.Fprintf(stderr, "Blockcheck2 не найден: %s\n", blockcheck)
		return 1
	}
	testDir := filepath.Join(a.Paths.ZapretRoot, "blockcheck2.d", "z4r")
	for _, name := range []string{"10-list.sh", "list_https_tls12.txt", "list_https_tls13.txt"} {
		if st, err := os.Stat(filepath.Join(testDir, name)); err != nil || st.IsDir() || st.Size() == 0 {
			fmt.Fprintf(stderr, "файл Blockcheck2 отсутствует или пуст: %s\n", filepath.Join(testDir, name))
			return 1
		}
	}
	bash, err := findBash()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctl, err := service.New(service.Config{Platform: a.Platform, Paths: a.Paths})
	if err != nil {
		fmt.Fprintf(stderr, "управление zapret2 недоступно: %v\n", err)
		return 1
	}
	wasRunning := service.IsNfqws2Running()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if wasRunning {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = ctl.Stop(stopCtx, service.UnitZapret2)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "не удалось безопасно остановить zapret2: %v\n", err)
			return 1
		}
		if service.IsNfqws2Running() {
			fmt.Fprintln(stderr, "zapret2 всё ещё запущен после stop; Blockcheck2 не будет запущен")
			return 1
		}
		defer func() {
			restartCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := ctl.Restart(restartCtx, service.UnitZapret2); err != nil {
				fmt.Fprintf(stderr, "ОШИБКА: Blockcheck2 завершён, но zapret2 не восстановлен: %v\n", err)
				exitCode = 1
			}
		}()
	}
	logDir := filepath.Join(os.TempDir(), "zapret2", "cache", "blockcheck2")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "не удалось создать каталог лога Blockcheck2: %v\n", err)
		return 1
	}
	logPath := filepath.Join(logDir, "zator-blockcheck2-"+time.Now().Format("20060102_150405")+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(stderr, "не удалось создать лог Blockcheck2: %v\n", err)
		return 1
	}
	defer logFile.Close()

	cmd := exec.CommandContext(ctx, bash, blockcheck)
	cmd.Env = withEnv(os.Environ(), map[string]string{
		"TEST": "z4r", "DOMAINS": url, "TESTDIR": testDir,
		"CURL_HTTPS_GET": "1", "BATCH": "1", "ENABLE_HTTP": "0",
		"ENABLE_HTTPS_TLS12": "1", "ENABLE_HTTPS_TLS13": "1", "ENABLE_HTTP3": "0",
		"ZAPRET_BASE": a.Paths.ZapretRoot,
	})
	cmd.Stdout, cmd.Stderr = io.MultiWriter(stdout, logFile), io.MultiWriter(stderr, logFile)
	fmt.Fprintf(stdout, "запускаю Blockcheck2 для %s (TLS 1.2/1.3)…\n", url)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "Blockcheck2 отменён")
		} else {
			fmt.Fprintf(stderr, "Blockcheck2 завершился с ошибкой: %v\n", err)
		}
		exitCode = 1
	}
	if err := logFile.Close(); err != nil {
		fmt.Fprintf(stderr, "ошибка закрытия лога Blockcheck2: %v\n", err)
		return 1
	}
	summaryPath := filepath.Join(a.Paths.ZapretRoot, "blockcheck2_summary.txt")
	if !writeBlockcheckSummary(logPath, summaryPath) {
		fmt.Fprintln(stderr, "Blockcheck2 завершился, но SUMMARY файл не найден")
		return 1
	}
	fmt.Fprintf(stdout, "SUMMARY сохранён: %s\n", summaryPath)
	summaryFile, err := os.Open(summaryPath)
	if err != nil {
		fmt.Fprintf(stderr, "не удалось прочитать SUMMARY: %v\n", err)
		return 1
	}
	_, copyErr := io.Copy(stdout, summaryFile)
	closeErr := summaryFile.Close()
	if copyErr != nil || closeErr != nil {
		fmt.Fprintf(stderr, "ошибка вывода SUMMARY: %v\n", errors.Join(copyErr, closeErr))
		return 1
	}
	return exitCode
}

func findBash() (string, error) {
	for _, name := range []string{"/opt/bin/bash", "/bin/bash", "/usr/bin/bash"} {
		if st, err := os.Stat(name); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return name, nil
		}
	}
	if name, err := exec.LookPath("bash"); err == nil {
		return name, nil
	}
	return "", errors.New("bash не найден")
}

func withEnv(env []string, overrides map[string]string) []string {
	out := make([]string, 0, len(env)+len(overrides))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, replaced := overrides[key]; !replaced {
			out = append(out, item)
		}
	}
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	return out
}

func writeBlockcheckSummary(logPath, summaryPath string) bool {
	f, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer f.Close()
	out, err := os.Create(summaryPath)
	if err != nil {
		return false
	}
	defer out.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	inSummary, printed := false, false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "* SUMMARY") {
			inSummary, printed = true, true
		}
		if inSummary && (strings.HasPrefix(line, "* COMMON") || strings.HasPrefix(line, "Please note this SUMMARY") || strings.HasPrefix(line, "Understanding how strategies work")) {
			break
		}
		if inSummary {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return false
			}
		}
	}
	return printed && sc.Err() == nil
}
