// Package syslist enumerates the operating systems and architectures Go
// supports and parses their string names into typed values.
package syslist

import "fmt"

// OsType is a validated operating-system name (as used by GOOS).
type OsType string

// Known operating-system values, mirroring Go's GOOS list.
const (
	OsTypeAix       OsType = "aix"
	OsTypeAndroid   OsType = "android"
	OsTypeDarwin    OsType = "darwin"
	OsTypeDragonfly OsType = "dragonfly"
	OsTypeFreebsd   OsType = "freebsd"
	OsTypeHurd      OsType = "hurd"
	OsTypeIllumos   OsType = "illumos"
	OsTypeIos       OsType = "ios"
	OsTypeJs        OsType = "js"
	OsTypeLinux     OsType = "linux"
	OsTypeNacl      OsType = "nacl"
	OsTypeNetbsd    OsType = "netbsd"
	OsTypeOpenbsd   OsType = "openbsd"
	OsTypePlan9     OsType = "plan9"
	OsTypeSolaris   OsType = "solaris"
	OsTypeWasip1    OsType = "wasip1"
	OsTypeWindows   OsType = "windows"
	OsTypeZos       OsType = "zos"
)

// ArchType is a validated CPU architecture name (as used by GOARCH).
type ArchType string

// Known CPU-architecture values, mirroring Go's GOARCH list.
const (
	ArchType386         ArchType = "386"
	ArchTypeAmd64       ArchType = "amd64"
	ArchTypeAmd64p32    ArchType = "amd64p32"
	ArchTypeArm         ArchType = "arm"
	ArchTypeArmbe       ArchType = "armbe"
	ArchTypeArm64       ArchType = "arm64"
	ArchTypeArm64be     ArchType = "arm64be"
	ArchTypeLoong64     ArchType = "loong64"
	ArchTypeMips        ArchType = "mips"
	ArchTypeMipsle      ArchType = "mipsle"
	ArchTypeMips64      ArchType = "mips64"
	ArchTypeMips64le    ArchType = "mips64le"
	ArchTypeMips64p32   ArchType = "mips64p32"
	ArchTypeMips64p32le ArchType = "mips64p32le"
	ArchTypePpc         ArchType = "ppc"
	ArchTypePpc64       ArchType = "ppc64"
	ArchTypePpc64le     ArchType = "ppc64le"
	ArchTypeRiscv       ArchType = "riscv"
	ArchTypeRiscv64     ArchType = "riscv64"
	ArchTypeS390        ArchType = "s390"
	ArchTypeS390x       ArchType = "s390x"
	ArchTypeSparc       ArchType = "sparc"
	ArchTypeSparc64     ArchType = "sparc64"
	ArchTypeWasm        ArchType = "wasm"
)

// GetOsTypeFromString validates osType and returns the matching OsType,
// or an error if it is not a recognized operating system.
func GetOsTypeFromString(osType string) (OsType, error) {
	switch OsType(osType) {
	case OsTypeAix, OsTypeAndroid, OsTypeDarwin, OsTypeDragonfly,
		OsTypeFreebsd, OsTypeHurd, OsTypeIllumos, OsTypeIos,
		OsTypeJs, OsTypeLinux, OsTypeNacl, OsTypeNetbsd,
		OsTypeOpenbsd, OsTypePlan9, OsTypeSolaris, OsTypeWasip1,
		OsTypeWindows, OsTypeZos:
		return OsType(osType), nil
	default:
		return "", fmt.Errorf("unknown OS type: %s", osType)
	}
}

// GetArchTypeFromString validates archType and returns the matching ArchType,
// or an error if it is not a recognized CPU architecture.
func GetArchTypeFromString(archType string) (ArchType, error) {
	switch ArchType(archType) {
	case ArchType386, ArchTypeAmd64, ArchTypeAmd64p32, ArchTypeArm,
		ArchTypeArmbe, ArchTypeArm64, ArchTypeArm64be, ArchTypeLoong64,
		ArchTypeMips, ArchTypeMipsle, ArchTypeMips64, ArchTypeMips64le,
		ArchTypeMips64p32, ArchTypeMips64p32le, ArchTypePpc, ArchTypePpc64,
		ArchTypePpc64le, ArchTypeRiscv, ArchTypeRiscv64, ArchTypeS390,
		ArchTypeS390x, ArchTypeSparc, ArchTypeSparc64, ArchTypeWasm:
		return ArchType(archType), nil
	default:
		return "", fmt.Errorf("unknown architecture type: %s", archType)
	}
}
