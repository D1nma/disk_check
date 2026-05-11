package scanner

import "time"

type Entry struct {
    Path     string
    Size     int64
    IsDir    bool
    ModTime  time.Time
}

type ScanResult struct {
    Entries []Entry
    Error   error
}
