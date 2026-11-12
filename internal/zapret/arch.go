package zapret

import (
	"fmt"

	"github.com/AloofLibra/zator/internal/platform"
)

// ArchDirs возвращает имена каталогов binaries/ в архиве zapret2 для
// архитектуры платформы в порядке предпочтения (первый — основной).
// Для 64-битных MIPS запасным вариантом служит 32-битный (o32) бинарник,
// который ядро mips64 способно исполнять.
func ArchDirs(a platform.Arch) ([]string, error) {
	switch a {
	case platform.ArchARM:
		return []string{"linux-arm"}, nil
	case platform.ArchARM64:
		return []string{"linux-arm64"}, nil
	case platform.ArchMIPS:
		return []string{"linux-mips"}, nil
	case platform.ArchMIPSEL:
		return []string{"linux-mipsel"}, nil
	case platform.ArchMIPS64:
		return []string{"linux-mips64", "linux-mips"}, nil
	case platform.ArchMIPSEL64:
		return []string{"linux-mipsel64", "linux-mipsel"}, nil
	case platform.ArchPPC:
		return []string{"linux-ppc"}, nil
	case platform.ArchX86:
		return []string{"linux-x86"}, nil
	case platform.ArchX86_64:
		return []string{"linux-x86_64"}, nil
	case platform.ArchRISCV64:
		return []string{"linux-riscv64"}, nil
	case platform.ArchLexra:
		return []string{"linux-lexra"}, nil
	}
	return nil, fmt.Errorf("zapret: неподдерживаемая архитектура %q", string(a))
}

// ResolveArch сопоставляет архитектуру платформы с основным именем
// каталога бинарников апстрима (например x86_64 → linux-x86_64).
func ResolveArch(a platform.Arch) (string, error) {
	dirs, err := ArchDirs(a)
	if err != nil {
		return "", err
	}
	return dirs[0], nil
}
