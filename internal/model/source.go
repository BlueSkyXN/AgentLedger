package model

type Source struct {
	ID       string
	Agent    string
	Channel  string
	BasePath string
}

type SourceFile struct {
	Rowid         int64
	SourceID      string
	FilePath      string
	FileSize      int64
	FileMtimeMs   int64
	ContentSHA256 string
	ImportStatus  string // "pending", "imported", "failed", "skipped"
	CleanupStatus string // "none", "eligible", "quarantined", "deleted"
	LastImportMs  int64
}
