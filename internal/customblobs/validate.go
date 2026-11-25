// Package customblobs управляет пользовательскими фейк-блобами (files/fake/custom_blob и custom_blobs.tsv)
// и их инъекцией в конфиг zapret2 между маркерами #Z2R_CUSTOM_BLOBS_BEGIN/END.
package customblobs

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/AloofLibra/zator/internal/config"
)

const (
	MaxNameLen    = 32
	MaxFileLen    = 64
	MaxCommentLen = 64
	MinBlobSize   = 1
	MaxBlobSize   = 256 * 1024
)

var (
	ErrInvalid   = errors.New("некорректные параметры блоба")
	ErrNotFound  = errors.New("блоб не найден")
	ErrNameTaken = errors.New("имя блоба уже занято")
	ErrBlobInUse = errors.New("блоб используется в пользовательских стратегиях")
	ErrTooLarge  = errors.New("размер файла блоба вне допустимого диапазона")
)

var (
	blobNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	blobFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// BuiltinBlobNames — встроенные имена блобов, доступные nfqws2 без объявления в custom_blobs.tsv.
var BuiltinBlobNames = []string{
	"fake_default_tls",
	"fake_default_http",
	"fake_default_quic",
	"clone_vk",
	"clone_hcaptcha",
	"clone_vk_fmd",
	"clone_hcaptcha_fms",
}

// ValidateBlobName проверяет формат имени блоба (буква/подчёркивание, до 32 символов, не 0x...).
func ValidateBlobName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > MaxNameLen || strings.HasPrefix(name, "0x") || !blobNameRe.MatchString(name) {
		return fmt.Errorf("%w: Имя блоба: буква/подчёркивание, дальше буквы/цифры/подчёркивание (до 32 символов).", ErrInvalid)
	}
	return nil
}

// ValidateBlobFileName проверяет имя файла внутри custom_blob/ (без путей и ..).
func ValidateBlobFileName(name string) error {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return fmt.Errorf("%w: Некорректное имя файла.", ErrInvalid)
	}
	if len(name) > MaxFileLen {
		return fmt.Errorf("%w: Имя файла длиннее 64 символов.", ErrInvalid)
	}
	if !blobFileRe.MatchString(name) {
		return fmt.Errorf("%w: Имя файла: старт с буквы/цифры, допустимы буквы, цифры, . _ - (без пробелов).", ErrInvalid)
	}
	return nil
}

// DefaultBlobNameFromFile строит имя блоба по умолчанию из имени файла (как _custom_blobs_name_from_file).
func DefaultBlobNameFromFile(fileName string) string {
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	var b strings.Builder
	for i := 0; i < len(base); i++ {
		c := base[i]
		isWord := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
		if isWord {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	auto := b.String()
	switch {
	case auto == "" || auto[0] == '_':
		auto = "blob_1"
	case auto[0] >= '0' && auto[0] <= '9':
		auto = "blob_" + auto
	}
	if len(auto) > MaxNameLen {
		auto = auto[:MaxNameLen]
	}
	return auto
}

// SanitizeComment очищает комментарий от табуляций/переводов строк и обрезает до 64 символов.
func SanitizeComment(comment string) string {
	r := strings.NewReplacer("\t", " ", "\r", " ", "\n", " ", "\x01", " ")
	c := strings.TrimSpace(r.Replace(comment))
	if utf8.RuneCountInString(c) > MaxCommentLen {
		runes := []rune(c)
		c = string(runes[:MaxCommentLen])
	}
	return c
}

func collectTakenNames(cfg *config.Config, existing []BlobEntry, includeCustomCfg bool) map[string]bool {
	taken := make(map[string]bool)
	if cfg != nil {
		for _, n := range cfg.DeclaredBlobNames(includeCustomCfg) {
			taken[n] = true
		}
	}
	for _, n := range BuiltinBlobNames {
		taken[n] = true
	}
	for _, e := range existing {
		taken[e.Name] = true
	}
	return taken
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
