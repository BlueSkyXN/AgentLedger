package db

import (
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestCachedTimeLocationPreservesHistoricalDSTBuckets(t *testing.T) {
	timeLocationCache = sync.Map{}
	beforeDST, err := timeBucket(time.Date(2026, time.March, 8, 4, 30, 0, 0, time.UTC).UnixMilli(), "America/New_York", "daily")
	if err != nil {
		t.Fatal(err)
	}
	afterDST, err := timeBucket(time.Date(2026, time.July, 8, 4, 30, 0, 0, time.UTC).UnixMilli(), "America/New_York", "daily")
	if err != nil {
		t.Fatal(err)
	}
	if beforeDST != "2026-03-07" || afterDST != "2026-07-08" {
		t.Fatalf("cached historical buckets are wrong: before=%s after=%s", beforeDST, afterDST)
	}
	first, ok := timeLocationCache.Load("America/New_York")
	if !ok {
		t.Fatal("expected IANA location to be cached")
	}
	second, err := cachedTimeLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("expected cached location pointer to be reused")
	}
}
