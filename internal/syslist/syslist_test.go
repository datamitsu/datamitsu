package syslist

import "testing"

func TestGetOsTypeFromString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    OsType
		wantErr bool
	}{
		{
			name:    "darwin",
			input:   "darwin",
			want:    OsTypeDarwin,
			wantErr: false,
		},
		{
			name:    "linux",
			input:   "linux",
			want:    OsTypeLinux,
			wantErr: false,
		},
		{
			name:    "windows",
			input:   "windows",
			want:    OsTypeWindows,
			wantErr: false,
		},
		{
			name:    "freebsd",
			input:   "freebsd",
			want:    OsTypeFreebsd,
			wantErr: false,
		},
		{
			name:    "openbsd",
			input:   "openbsd",
			want:    OsTypeOpenbsd,
			wantErr: false,
		},
		{
			name:    "invalid",
			input:   "invalidOS",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetOsTypeFromString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetOsTypeFromString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetOsTypeFromString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetArchTypeFromString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ArchType
		wantErr bool
	}{
		{
			name:    "amd64",
			input:   "amd64",
			want:    ArchTypeAmd64,
			wantErr: false,
		},
		{
			name:    "arm64",
			input:   "arm64",
			want:    ArchTypeArm64,
			wantErr: false,
		},
		{
			name:    "386",
			input:   "386",
			want:    ArchType386,
			wantErr: false,
		},
		{
			name:    "arm",
			input:   "arm",
			want:    ArchTypeArm,
			wantErr: false,
		},
		{
			name:    "riscv64",
			input:   "riscv64",
			want:    ArchTypeRiscv64,
			wantErr: false,
		},
		{
			name:    "invalid",
			input:   "invalidArch",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetArchTypeFromString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetArchTypeFromString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetArchTypeFromString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
