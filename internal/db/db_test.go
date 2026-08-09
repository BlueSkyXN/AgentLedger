package db

import (
	"net/url"
	"path/filepath"
	"testing"
)

func TestSQLiteFileURIWindowsPaths(t *testing.T) {
	query := url.Values{"mode": {"ro"}, "_busy_timeout": {"5000"}}
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{
			name: "drive path with reserved and unicode characters",
			path: `C:\Users\test\Agent Ledger\#notes?账本.db`,
			want: "file:///C:/Users/test/Agent%20Ledger/%23notes%3F%E8%B4%A6%E6%9C%AC.db?_busy_timeout=5000&mode=ro",
		},
		{
			name: "UNC path",
			path: `\\server\share\Agent Ledger\ledger.db`,
			want: "file:////server/share/Agent%20Ledger/ledger.db?_busy_timeout=5000&mode=ro",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sqliteFileURI(test.path, '\\', query); got != test.want {
				t.Fatalf("sqlite URI = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSQLiteOpenFunctionsSupportReservedCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Agent Ledger #?账本.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	readOnly, err := OpenReadOnlyV3(path)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatalf("close read-only: %v", err)
	}

	readWrite, err := OpenReadWriteV3(path)
	if err != nil {
		t.Fatalf("open read-write: %v", err)
	}
	if err := readWrite.Close(); err != nil {
		t.Fatalf("close read-write: %v", err)
	}
}
